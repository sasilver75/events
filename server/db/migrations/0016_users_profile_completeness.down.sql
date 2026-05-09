-- Reverse 0016. Recreates the mirror trigger from 0006 and drops the
-- profile columns. TRUNCATE is irreversible — the down migration leaves
-- public.users empty and assumes the seed runner repopulates it.

ALTER TABLE public.users
    DROP CONSTRAINT IF EXISTS users_dob_eighteen_chk,
    DROP CONSTRAINT IF EXISTS users_handle_display_lower_chk,
    DROP CONSTRAINT IF EXISTS users_handle_format_chk,
    DROP CONSTRAINT IF EXISTS users_handle_unique;

ALTER TABLE public.users
    DROP COLUMN IF EXISTS avatar_path,
    DROP COLUMN IF EXISTS tos_version,
    DROP COLUMN IF EXISTS tos_accepted_at,
    DROP COLUMN IF EXISTS dob,
    ALTER COLUMN display_name DROP NOT NULL,
    DROP COLUMN IF EXISTS handle_display,
    DROP COLUMN IF EXISTS handle;

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
