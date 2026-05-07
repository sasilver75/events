ALTER TABLE public.events
    DROP CONSTRAINT IF EXISTS events_tipped_only_for_beta,
    DROP CONSTRAINT IF EXISTS events_cap_ge_tip_threshold,
    DROP CONSTRAINT IF EXISTS events_tip_deadline_before_start,
    DROP CONSTRAINT IF EXISTS events_tip_pair,
    DROP CONSTRAINT IF EXISTS events_tip_threshold_min;

ALTER TABLE public.events
    DROP COLUMN IF EXISTS tip_deadline,
    DROP COLUMN IF EXISTS tip_threshold;
