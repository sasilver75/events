DROP INDEX IF EXISTS public.events_geom_geog_idx;
DROP INDEX IF EXISTS public.events_start_time_idx;

ALTER TABLE public.events
    ALTER COLUMN cap SET NOT NULL,
    ALTER COLUMN fuzz_radius_m DROP DEFAULT;

ALTER TABLE public.events
    ADD COLUMN center_lat DOUBLE PRECISION,
    ADD COLUMN center_lon DOUBLE PRECISION;
UPDATE public.events
    SET center_lat = ST_Y(geom),
        center_lon = ST_X(geom);
ALTER TABLE public.events
    ALTER COLUMN center_lat SET NOT NULL,
    ALTER COLUMN center_lon SET NOT NULL,
    ADD CONSTRAINT events_center_lat_check
        CHECK (center_lat BETWEEN -90 AND 90),
    ADD CONSTRAINT events_center_lon_check
        CHECK (center_lon BETWEEN -180 AND 180),
    DROP COLUMN geom;

ALTER TABLE public.events
    DROP CONSTRAINT IF EXISTS events_end_after_start,
    ADD COLUMN duration_minutes INTEGER;
UPDATE public.events
    SET duration_minutes = (EXTRACT(EPOCH FROM (end_time - start_time)) / 60)::int;
ALTER TABLE public.events
    ALTER COLUMN duration_minutes SET NOT NULL,
    ADD CONSTRAINT events_duration_minutes_check
        CHECK (duration_minutes > 0),
    DROP COLUMN end_time;

ALTER TABLE public.events
    RENAME COLUMN start_time TO start_at;
ALTER TABLE public.events
    RENAME COLUMN host_id TO host_user_id;

CREATE INDEX events_geog_idx ON public.events
    USING GIST ((ST_SetSRID(ST_MakePoint(center_lon, center_lat), 4326)::geography));
CREATE INDEX events_start_at_idx ON public.events (start_at);
