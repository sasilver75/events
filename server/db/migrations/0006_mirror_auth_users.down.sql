DROP TRIGGER IF EXISTS mirror_auth_user_to_public_trg ON auth.users;
DROP FUNCTION IF EXISTS public.mirror_auth_user_to_public();
