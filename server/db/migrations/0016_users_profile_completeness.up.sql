-- Signup completeness (#88, ADR 0025).
--
-- Drops the auth.users → public.users mirror trigger introduced in 0006
-- (ADR 0022, now superseded by ADR 0025) and adds the real profile columns
-- the user fills in during signup. POST /users/me/profile is the sole
-- insert path for public.users from this migration forward.
--
-- public.users is wiped (TRUNCATE … CASCADE) before the schema change so the
-- new NOT NULL columns can be added without a backfill. Per the issue brief,
-- the only existing rows in dev are seed fixtures, which are reseeded after
-- migration; staging is recreated by the staging stack reset.

DROP TRIGGER  IF EXISTS mirror_auth_user_to_public_trg ON auth.users;
DROP FUNCTION IF EXISTS public.mirror_auth_user_to_public();

TRUNCATE TABLE public.users CASCADE;

ALTER TABLE public.users
    ADD  COLUMN handle          TEXT        NOT NULL,
    ADD  COLUMN handle_display  TEXT        NOT NULL,
    ALTER COLUMN display_name   SET NOT NULL,
    ADD  COLUMN dob             DATE        NOT NULL,
    ADD  COLUMN tos_accepted_at TIMESTAMPTZ NOT NULL,
    ADD  COLUMN tos_version     TEXT        NOT NULL,
    ADD  COLUMN avatar_path     TEXT;

ALTER TABLE public.users
    ADD CONSTRAINT users_handle_unique             UNIQUE (handle),
    ADD CONSTRAINT users_handle_format_chk         CHECK (handle ~ '^[a-z0-9_]{3,20}$'),
    ADD CONSTRAINT users_handle_display_lower_chk  CHECK (lower(handle_display) = handle),
    ADD CONSTRAINT users_dob_eighteen_chk          CHECK (dob <= (CURRENT_DATE - INTERVAL '18 years'));
