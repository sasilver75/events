-- checkins captures the Show signal from accuracy-aware geofence taps
-- (#33, ADR 0011). One row per (event_id, user_id) — repeat taps from the
-- same Attendee on the same Event are absorbed by the PK and the handler
-- treats them as idempotent.
--
-- Server-only writes: the Go check-in handler validates Live state,
-- Committed-Attendee status, and the accuracy-aware distance rule before
-- inserting. RLS allows authenticated users to read their own rows so a
-- future client surface can render "you're checked in" without re-querying
-- through a Go endpoint; writes remain server-mediated.

CREATE TABLE public.checkins (
    event_id    UUID NOT NULL REFERENCES public.events(id) ON DELETE CASCADE,
    user_id     UUID NOT NULL REFERENCES public.users(id) ON DELETE CASCADE,
    recorded_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (event_id, user_id)
);

ALTER TABLE public.checkins ENABLE ROW LEVEL SECURITY;

CREATE POLICY checkins_select_own ON public.checkins
    FOR SELECT
    TO authenticated
    USING (user_id = (SELECT auth.uid()));
