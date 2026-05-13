DROP POLICY IF EXISTS spatial_ref_sys_public_read ON public.spatial_ref_sys;
ALTER TABLE public.spatial_ref_sys DISABLE ROW LEVEL SECURITY;
