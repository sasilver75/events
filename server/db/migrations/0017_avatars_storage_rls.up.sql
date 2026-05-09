-- Storage bucket + RLS for user avatars (#88).
--
-- Mirrors the event-banners posture from migration 0015:
--   - SELECT: public-anonymous. An avatar is meant to be visible alongside
--     a display_name in friend search, attendee lists, etc. Public-read
--     also lets the Supabase CDN cache, which signed URLs would defeat.
--   - INSERT/UPDATE/DELETE: only the authenticated owner of the
--     containing folder. Object keys are "{user_id}/{rest...}", so the
--     first folder segment must match auth.uid().
--
-- The bucket is also declared in supabase/config.toml.template for fresh
-- `supabase start` invocations, but we create it here too so the migration
-- is the single source of truth across all environments. Idempotent via
-- ON CONFLICT.

INSERT INTO storage.buckets (id, name, public, file_size_limit, allowed_mime_types)
VALUES (
    'avatars',
    'avatars',
    true,
    2097152, -- 2 MiB
    ARRAY['image/jpeg', 'image/png', 'image/heic', 'image/webp']
)
ON CONFLICT (id) DO UPDATE SET
    public = EXCLUDED.public,
    file_size_limit = EXCLUDED.file_size_limit,
    allowed_mime_types = EXCLUDED.allowed_mime_types;

CREATE POLICY avatars_public_read
    ON storage.objects
    FOR SELECT
    TO public
    USING (bucket_id = 'avatars');

CREATE POLICY avatars_owner_insert
    ON storage.objects
    FOR INSERT
    TO authenticated
    WITH CHECK (
        bucket_id = 'avatars'
        AND (storage.foldername(name))[1] = auth.uid()::text
    );

CREATE POLICY avatars_owner_update
    ON storage.objects
    FOR UPDATE
    TO authenticated
    USING (
        bucket_id = 'avatars'
        AND (storage.foldername(name))[1] = auth.uid()::text
    )
    WITH CHECK (
        bucket_id = 'avatars'
        AND (storage.foldername(name))[1] = auth.uid()::text
    );

CREATE POLICY avatars_owner_delete
    ON storage.objects
    FOR DELETE
    TO authenticated
    USING (
        bucket_id = 'avatars'
        AND (storage.foldername(name))[1] = auth.uid()::text
    );
