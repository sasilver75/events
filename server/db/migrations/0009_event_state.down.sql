DROP FUNCTION IF EXISTS public.event_state(public.events);

ALTER TABLE public.events
    DROP COLUMN IF EXISTS cancelled_at,
    DROP COLUMN IF EXISTS tipped_at;
