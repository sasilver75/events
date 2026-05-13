-- golang-migrate auto-creates public.schema_migrations without RLS, which
-- Supabase's rls_disabled_in_public linter flags as a critical issue: the
-- table is exposed via PostgREST to anon/authenticated roles. The Go server
-- connects directly via pgx as the DB owner and bypasses RLS, so enabling
-- RLS with no policies blocks PostgREST without breaking migrations.
ALTER TABLE public.schema_migrations ENABLE ROW LEVEL SECURITY;
