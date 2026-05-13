-- Drop the auth-mirror trigger introduced in 0006. See ADR 0025: with
-- public.users now carrying typed profile fields the user has to provide
-- (handle, dob, ToS), the trigger can no longer populate the row, and a
-- row's existence stops implying signup completion. POST /users/me/profile
-- becomes the sole insert path.

DROP TRIGGER IF EXISTS mirror_auth_user_to_public_trg ON auth.users;
DROP FUNCTION IF EXISTS public.mirror_auth_user_to_public();

-- display_name was added in 0003 as nullable. With profile completion now
-- enforced at the API boundary, every public.users row must carry it.
ALTER TABLE public.users
    ALTER COLUMN display_name SET NOT NULL;

ALTER TABLE public.users
    ADD COLUMN handle           TEXT        NOT NULL,
    ADD COLUMN handle_display   TEXT        NOT NULL,
    ADD COLUMN dob              DATE        NOT NULL,
    ADD COLUMN tos_accepted_at  TIMESTAMPTZ NOT NULL,
    ADD COLUMN tos_version      TEXT        NOT NULL,
    ADD COLUMN avatar_path      TEXT;

ALTER TABLE public.users
    ADD CONSTRAINT users_handle_format_chk
        CHECK (handle ~ '^[a-z0-9_]{3,20}$'),
    ADD CONSTRAINT users_handle_display_lower_chk
        CHECK (lower(handle_display) = handle),
    ADD CONSTRAINT users_dob_adult_chk
        CHECK (dob <= current_date - interval '18 years');

-- Named explicitly so the server can identify the violated constraint
-- (SQLSTATE 23505 + constraint_name) and translate to 409 handle_taken.
CREATE UNIQUE INDEX users_handle_key ON public.users (handle);
