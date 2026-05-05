CREATE TABLE public.users (
    id UUID PRIMARY KEY REFERENCES auth.users(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    display_name TEXT
);

CREATE TABLE public.events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    host_user_id UUID NOT NULL REFERENCES public.users(id) ON DELETE CASCADE,
    title TEXT NOT NULL,
    description TEXT NOT NULL,
    category TEXT NOT NULL CHECK (category IN (
        'Sports', 'Food/Drink', 'Music', 'Outdoors', 'Games',
        'Social', 'Creative', 'Wellness', 'Networking', 'Other'
    )),
    start_at TIMESTAMPTZ NOT NULL,
    duration_minutes INTEGER NOT NULL CHECK (duration_minutes > 0),
    cap SMALLINT NOT NULL CHECK (cap > 0),
    center_lat DOUBLE PRECISION NOT NULL CHECK (center_lat BETWEEN -90 AND 90),
    center_lon DOUBLE PRECISION NOT NULL CHECK (center_lon BETWEEN -180 AND 180),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX events_start_at_idx ON public.events (start_at);

CREATE INDEX events_geog_idx ON public.events
    USING GIST ((ST_SetSRID(ST_MakePoint(center_lon, center_lat), 4326)::geography));

CREATE TABLE public.commits (
    event_id UUID NOT NULL REFERENCES public.events(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES public.users(id) ON DELETE CASCADE,
    committed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (event_id, user_id)
);
