-- attendance_outcomes records the resolved Show vs Ghost outcome for every
-- Attendee who was still Committed when their Event reached the Done state
-- (#34, PRD §At-event experience, ADR 0009). One row per (event_id, user_id)
-- — the Done poller in internal/lifecycle inserts on the first tick after
-- end_time and the PK absorbs any subsequent reruns.
--
-- Wave 2 vocabulary is {'show','ghost'} only. 'flake' lands when Withdraw
-- classification ships in a later slice — extend the CHECK constraint then.
--
-- Server-only writes: the lifecycle Runner is the sole writer. RLS allows
-- authenticated users to read their own rows so future profile surfaces
-- (#36) can render history directly from Supabase.

CREATE TABLE public.attendance_outcomes (
    event_id    UUID NOT NULL REFERENCES public.events(id) ON DELETE CASCADE,
    user_id     UUID NOT NULL REFERENCES public.users(id) ON DELETE CASCADE,
    outcome     TEXT NOT NULL CHECK (outcome IN ('show', 'ghost')),
    recorded_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (event_id, user_id)
);

ALTER TABLE public.attendance_outcomes ENABLE ROW LEVEL SECURITY;

CREATE POLICY attendance_outcomes_select_own ON public.attendance_outcomes
    FOR SELECT
    TO authenticated
    USING (user_id = (SELECT auth.uid()));
