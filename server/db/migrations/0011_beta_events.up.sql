-- β-Events (Seeded with Tip threshold) per PRD-v0 §Event creation and #32.
--
-- A β-Event has both tip_threshold and tip_deadline set; an α-Event has
-- both NULL. The pair-NULL-or-pair-set invariant is enforced in the DB so
-- the create handler can't accidentally land a half-β row.
--
-- The tipped_at and cancelled_at timestamp columns themselves were added
-- ahead of this slice in 0009 (alongside the event_state function). This
-- migration adds the threshold + deadline that drive them, plus the
-- relational constraints between cap, threshold, and deadline.
--
-- Validation of the deadline's temporal bounds — after now(), at least
-- 15min before start_time — lives in the create handler. DB CHECKs cannot
-- reference now(), so the DB enforces only the start-time relation; the
-- "creation_time floor" is the natural consequence of inserting at now().

ALTER TABLE public.events
    ADD COLUMN tip_threshold SMALLINT,
    ADD COLUMN tip_deadline  TIMESTAMPTZ;

ALTER TABLE public.events
    ADD CONSTRAINT events_tip_threshold_min CHECK (
        tip_threshold IS NULL OR tip_threshold >= 2
    ),
    -- Pair invariant: α has neither, β has both.
    ADD CONSTRAINT events_tip_pair CHECK (
        (tip_threshold IS NULL AND tip_deadline IS NULL)
        OR (tip_threshold IS NOT NULL AND tip_deadline IS NOT NULL)
    ),
    -- Deadline must precede start_time (handler enforces the 15-min margin).
    ADD CONSTRAINT events_tip_deadline_before_start CHECK (
        tip_deadline IS NULL OR tip_deadline < start_time
    ),
    -- A Tip threshold above Cap would be unreachable by definition.
    ADD CONSTRAINT events_cap_ge_tip_threshold CHECK (
        cap IS NULL OR tip_threshold IS NULL OR cap >= tip_threshold
    ),
    -- Defense against bugs: tipped_at can only be set on β rows.
    ADD CONSTRAINT events_tipped_only_for_beta CHECK (
        tipped_at IS NULL OR tip_threshold IS NOT NULL
    );
