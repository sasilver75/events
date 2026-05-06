ALTER TABLE public.events
    ADD COLUMN display_geom geometry(Point, 4326),
    ADD COLUMN fuzz_radius_m INTEGER,
    ADD COLUMN location_visibility TEXT NOT NULL DEFAULT 'fuzzed'
        CHECK (location_visibility IN ('fuzzed', 'public'));
