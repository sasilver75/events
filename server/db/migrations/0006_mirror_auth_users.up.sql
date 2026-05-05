-- Mirror auth.users → public.users atomically inside Supabase Auth's
-- own transaction. See ADR 0022 for the mechanical-trigger boundary.

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
