ALTER TABLE public.events
    ADD COLUMN source TEXT NOT NULL DEFAULT 'user'
        CHECK (source IN ('user', 'curated')),
    ADD COLUMN seed_key TEXT UNIQUE;
