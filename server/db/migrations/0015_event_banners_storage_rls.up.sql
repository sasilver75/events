-- Storage bucket + RLS for event banners (#41).
--
-- Bucket access model (locked in during triage):
--   - SELECT: public-anonymous. Banners are deliberately promotional;
--     location privacy is handled by display_geom on the row, not by
--     gating the banner. Public-read also lets the Supabase CDN cache,
--     which signed URLs would defeat.
--   - INSERT/UPDATE/DELETE: only the authenticated owner of the
--     containing folder. Object keys are "{user_id}/{rest...}", so the
--     first folder segment must match auth.uid().
--
-- The bucket is also declared in supabase/config.toml.template for
-- fresh `supabase start` invocations, but we create it here too so
-- the migration is the single source of truth across all environments
-- (existing local volumes, hosted staging, future prod). Idempotent
-- via ON CONFLICT.

INSERT INTO storage.buckets (id, name, public, file_size_limit, allowed_mime_types)
VALUES (
    'event-banners',
    'event-banners',
    true,
    2097152, -- 2 MiB
    ARRAY['image/jpeg', 'image/png', 'image/heic', 'image/webp']
)
ON CONFLICT (id) DO UPDATE SET
    public = EXCLUDED.public,
    file_size_limit = EXCLUDED.file_size_limit,
    allowed_mime_types = EXCLUDED.allowed_mime_types;

CREATE POLICY event_banners_public_read
    ON storage.objects
    FOR SELECT
    TO public
    USING (bucket_id = 'event-banners');

CREATE POLICY event_banners_owner_insert
    ON storage.objects
    FOR INSERT
    TO authenticated
    WITH CHECK (
        bucket_id = 'event-banners'
        AND (storage.foldername(name))[1] = auth.uid()::text
    );

CREATE POLICY event_banners_owner_update
    ON storage.objects
    FOR UPDATE
    TO authenticated
    USING (
        bucket_id = 'event-banners'
        AND (storage.foldername(name))[1] = auth.uid()::text
    )
    WITH CHECK (
        bucket_id = 'event-banners'
        AND (storage.foldername(name))[1] = auth.uid()::text
    );

CREATE POLICY event_banners_owner_delete
    ON storage.objects
    FOR DELETE
    TO authenticated
    USING (
        bucket_id = 'event-banners'
        AND (storage.foldername(name))[1] = auth.uid()::text
    );
