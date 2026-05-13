-- Reverse 0017. Drops the columns and constraints added in the up
-- migration, then recreates the auth-mirror trigger from 0006 so the
-- pre-0017 invariant ("row exists from the moment auth.users does")
-- is restored.

DROP INDEX IF EXISTS users_handle_key;

ALTER TABLE public.users
    DROP CONSTRAINT IF EXISTS users_dob_adult_chk,
    DROP CONSTRAINT IF EXISTS users_handle_display_lower_chk,
    DROP CONSTRAINT IF EXISTS users_handle_format_chk;

ALTER TABLE public.users
    DROP COLUMN IF EXISTS avatar_path,
    DROP COLUMN IF EXISTS tos_version,
    DROP COLUMN IF EXISTS tos_accepted_at,
    DROP COLUMN IF EXISTS dob,
    DROP COLUMN IF EXISTS handle_display,
    DROP COLUMN IF EXISTS handle;

ALTER TABLE public.users
    ALTER COLUMN display_name DROP NOT NULL;

CREATE FUNCTION public.mirror_auth_user_to_public()
RETURNS TRIGGER
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = public
AS $$
BEGIN
    INSERT INTO public.users (id) VALUES (NEW.id)
    ON CONFLICT (id) DO NOTHING;
    RETURN NEW;
END;
$$;

CREATE TRIGGER mirror_auth_user_to_public_trg
    AFTER INSERT ON auth.users
    FOR EACH ROW
    EXECUTE FUNCTION public.mirror_auth_user_to_public();
