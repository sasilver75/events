ALTER TABLE public.users ENABLE ROW LEVEL SECURITY;
ALTER TABLE public.events ENABLE ROW LEVEL SECURITY;
ALTER TABLE public.commits ENABLE ROW LEVEL SECURITY;

CREATE POLICY users_select_own ON public.users
    FOR SELECT
    TO authenticated
    USING (id = (SELECT auth.uid()));

CREATE POLICY events_select_all ON public.events
    FOR SELECT
    TO authenticated
    USING (true);

CREATE POLICY commits_select_own ON public.commits
    FOR SELECT
    TO authenticated
    USING (user_id = (SELECT auth.uid()));
