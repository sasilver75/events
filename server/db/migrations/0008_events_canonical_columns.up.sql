-- Align public.events column names with PRD-v0 §Geo data model and the
-- vocabulary used in CONTEXT.md and the ADRs: host_id, start_time, end_time,
-- geom. The drift was introduced in 0003 (#7) and #10's Browse handler
-- shipped against the drifted names; this migration reconciles the schema
-- with the canonical names so future code can stop translating.

ALTER TABLE public.events
    RENAME COLUMN host_user_id TO host_id;
ALTER TABLE public.events
    RENAME COLUMN start_at TO start_time;

-- Replace the stored duration with an explicit end_time TIMESTAMPTZ so
-- lifecycle-state derivation (PRD §275, #31) can read both endpoints
-- directly without arithmetic.
ALTER TABLE public.events
    ADD COLUMN end_time TIMESTAMPTZ;
UPDATE public.events
    SET end_time = start_time + make_interval(mins => duration_minutes);
ALTER TABLE public.events
    ALTER COLUMN end_time SET NOT NULL,
    ADD CONSTRAINT events_end_after_start CHECK (end_time > start_time),
    DROP COLUMN duration_minutes;

-- Replace the lat/lon pair with a single geom Point in 4326. Reads cast to
-- geography for radius queries; writes set geom from the iOS-supplied lat/lon.
ALTER TABLE public.events
    ADD COLUMN geom geometry(Point, 4326);
UPDATE public.events
    SET geom = ST_SetSRID(ST_MakePoint(center_lon, center_lat), 4326);
ALTER TABLE public.events
    ALTER COLUMN geom SET NOT NULL,
    DROP COLUMN center_lat,
    DROP COLUMN center_lon;

-- Cap is optional (Hosts may seed open-attendance Events). Default the fuzz
-- radius to 200m — that's the v0 standard per PRD §Geo data model — so
-- inserts can omit it.
ALTER TABLE public.events
    ALTER COLUMN cap DROP NOT NULL,
    ALTER COLUMN fuzz_radius_m SET DEFAULT 200;

DROP INDEX IF EXISTS public.events_geog_idx;
DROP INDEX IF EXISTS public.events_start_at_idx;
CREATE INDEX events_geom_geog_idx ON public.events
    USING GIST ((geom::geography));
CREATE INDEX events_start_time_idx ON public.events (start_time);
