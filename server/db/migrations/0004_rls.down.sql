DROP POLICY IF EXISTS commits_select_own ON public.commits;
DROP POLICY IF EXISTS events_select_all ON public.events;
DROP POLICY IF EXISTS users_select_own ON public.users;

ALTER TABLE public.commits DISABLE ROW LEVEL SECURITY;
ALTER TABLE public.events DISABLE ROW LEVEL SECURITY;
ALTER TABLE public.users DISABLE ROW LEVEL SECURITY;
