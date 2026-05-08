-- Post-event feedback flow + reputation cache (#35, PRD §Post-event +
-- §Reputation system + ADR 0008).
--
-- Three tables:
--
--   feedback_signals   captures every 👍/👎/skip cast in the post-event flow,
--                      keyed by (event_id, voter_id, target_user_id).
--                      Powers bundled-feedback display (PRD §Bundled
--                      feedback to self) and is the audit trail for
--                      ratings the user submitted. PRIMARY KEY on the
--                      triple makes resubmit-to-overwrite a pure
--                      ON CONFLICT DO UPDATE.
--
--   flags              records hard-flag 👎s only — those carrying at
--                      least one of the hard reasons defined in ADR 0008
--                      ('would_not_attend_with_again' | 'concerning_behavior'
--                      | 'made_me_uncomfortable'). Soft 👎s with reason
--                      'just_didnt_like_them' do NOT land here; they live
--                      only in feedback_signals for bundled display. The
--                      surrogate id keeps the table mutable for
--                      future backfill / curation tooling, and the
--                      UNIQUE(event_id, voter_id, target_user_id) makes
--                      resubmit-overwrite (and resubmit-as-not-flag-anymore)
--                      mechanical.
--
--   reputation         the denorm cache for RecomputeReputation per
--                      PRD §Reputation system §Storage. Source of truth
--                      stays in attendance_outcomes + flags; this table
--                      is a precomputed view rebuilt on every Done
--                      fan-out, hard-flag submit, and the daily batch.
--                      attendee_score is 0..100 (Bayes-smoothed,
--                      multiplied by flag_factor); host_score is reserved
--                      for the host-side rep slice and stays NULL today.
--
-- Server-only writes (RLS denies all unauthenticated writes; the Go
-- handlers connect with the unrestricted role). Authenticated reads:
-- a user can read their own reputation row (profile surface in #36 will
-- consume it directly from Supabase) and their own cast signals/flags
-- so a future client surface can render "you submitted feedback" without
-- a server round-trip.

CREATE TABLE public.feedback_signals (
    event_id       UUID NOT NULL REFERENCES public.events(id) ON DELETE CASCADE,
    voter_id       UUID NOT NULL REFERENCES public.users(id)  ON DELETE CASCADE,
    target_user_id UUID NOT NULL REFERENCES public.users(id)  ON DELETE CASCADE,
    signal         TEXT NOT NULL CHECK (signal IN ('up', 'down', 'skip')),
    -- Reasons captured on 👎 only — empty array on 👍/skip and on
    -- reasonless 👎 (the latter is allowed at the schema level; the
    -- handler enforces the 👎-implies-≥1-reason rule per PRD §171, but
    -- a permissive schema lets us evolve the flow without a migration).
    reasons        TEXT[] NOT NULL DEFAULT '{}'::TEXT[],
    cast_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (event_id, voter_id, target_user_id),
    CONSTRAINT feedback_signals_no_self CHECK (voter_id <> target_user_id)
);

ALTER TABLE public.feedback_signals ENABLE ROW LEVEL SECURITY;

CREATE POLICY feedback_signals_select_own ON public.feedback_signals
    FOR SELECT
    TO authenticated
    USING (voter_id = (SELECT auth.uid()));

CREATE TABLE public.flags (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    voter_id       UUID NOT NULL REFERENCES public.users(id)  ON DELETE CASCADE,
    target_user_id UUID NOT NULL REFERENCES public.users(id)  ON DELETE CASCADE,
    event_id       UUID NOT NULL REFERENCES public.events(id) ON DELETE CASCADE,
    reasons        TEXT[] NOT NULL,
    cast_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- Resubmit-overwrite key: at most one flag row per (voter, target,
    -- event). The handler does ON CONFLICT … DO UPDATE on this key.
    UNIQUE (event_id, voter_id, target_user_id),
    CONSTRAINT flags_no_self CHECK (voter_id <> target_user_id),
    CONSTRAINT flags_reasons_nonempty CHECK (array_length(reasons, 1) >= 1)
);

CREATE INDEX flags_target_user_id_idx ON public.flags (target_user_id);

ALTER TABLE public.flags ENABLE ROW LEVEL SECURITY;

-- Anonymous to the flagged user (PRD §US 37). Voters can read their own
-- flags so a future "you flagged this person" surface is available
-- without a server round-trip.
CREATE POLICY flags_select_own ON public.flags
    FOR SELECT
    TO authenticated
    USING (voter_id = (SELECT auth.uid()));

CREATE TABLE public.reputation (
    user_id              UUID PRIMARY KEY REFERENCES public.users(id) ON DELETE CASCADE,
    attendee_score       SMALLINT NOT NULL CHECK (attendee_score BETWEEN 0 AND 100),
    host_score           SMALLINT          CHECK (host_score IS NULL OR host_score BETWEEN 0 AND 100),
    attendee_event_count INTEGER  NOT NULL DEFAULT 0 CHECK (attendee_event_count >= 0),
    host_event_count     INTEGER  NOT NULL DEFAULT 0 CHECK (host_event_count >= 0),
    last_recomputed_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX reputation_last_recomputed_at_idx
    ON public.reputation (last_recomputed_at);

ALTER TABLE public.reputation ENABLE ROW LEVEL SECURITY;

CREATE POLICY reputation_select_own ON public.reputation
    FOR SELECT
    TO authenticated
    USING (user_id = (SELECT auth.uid()));
