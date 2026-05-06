-- Lifecycle state derivation per PRD-v0 §274–278 and #31.
--
-- State is computed, not stored: a SQL function reads timestamps + commit
-- count and returns one of 'Open' | 'Filling' | 'Tipped' | 'Live' | 'Done' |
-- 'Cancelled'. The function is the single canonical source of state for both
-- browse and detail reads.
--
-- This migration adds the two timestamp columns the function depends on.
-- The columns are nullable; their write paths land in their owning slices:
--   - tipped_at: set by the Commit handler when count reaches tip_threshold
--     in the β-Event slice (#32).
--   - cancelled_at: set by the cancel slice (β auto-cancel poller in #32 and
--     the Host α-cancel surface in a later slice).
--
-- Until those slices land, both columns stay NULL and 'Tipped'/'Cancelled'
-- are unreachable in production data — see server/README.md.

ALTER TABLE public.events
    ADD COLUMN tipped_at TIMESTAMPTZ,
    ADD COLUMN cancelled_at TIMESTAMPTZ;

-- event_state(events_row) returns the lifecycle state derived from the row's
-- timestamps and commit count. Order of branches encodes precedence:
--   Cancelled wins over everything (a cancelled β-Event past start is still
--     'Cancelled', not 'Live'/'Done').
--   Done is pure time-passage on end_time.
--   Live is pure time-passage on [start_time, end_time).
--   Tipped is β-only and only meaningful pre-start (post-start, the row reads
--     'Live'); the branch ordering ensures Live wins after start.
--   Filling is any pre-start row with at least one Commit (relevant to α with
--     Cap and to β before Tip).
--   Open is the default — no commits, no Tip, pre-start.
--
-- STABLE: result is fixed within a single statement at the snapshot's view
-- of now() and the commits subquery. Callers project event_state(e) as a
-- column on browse / detail reads.
CREATE FUNCTION public.event_state(e public.events) RETURNS TEXT
LANGUAGE SQL STABLE AS $$
    SELECT CASE
        WHEN e.cancelled_at IS NOT NULL THEN 'Cancelled'
        WHEN e.end_time <= now() THEN 'Done'
        WHEN e.start_time <= now() THEN 'Live'
        WHEN e.tipped_at IS NOT NULL THEN 'Tipped'
        WHEN EXISTS (SELECT 1 FROM public.commits c WHERE c.event_id = e.id) THEN 'Filling'
        ELSE 'Open'
    END
$$;
