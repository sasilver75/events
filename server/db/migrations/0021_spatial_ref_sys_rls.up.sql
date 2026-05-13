-- PostGIS installs spatial_ref_sys into public without RLS, which Supabase's
-- linter flags. The table holds static SRID reference data (~8500 rows) and
-- is read by PostGIS functions like ST_Transform — so we enable RLS with a
-- permissive SELECT policy rather than denying access, otherwise geo queries
-- running as anon/authenticated would fail when PostGIS reaches into it.
ALTER TABLE public.spatial_ref_sys ENABLE ROW LEVEL SECURITY;

CREATE POLICY spatial_ref_sys_public_read
  ON public.spatial_ref_sys
  FOR SELECT
  USING (true);
