-- Storage bucket + RLS for user avatars (#88).
--
-- Bucket access model (mirrors event-banners from 0015):
--   - SELECT: public-anonymous. Avatars are surfaced on attendee lists,
--     friend search results, and event detail; gating them behind signed
--     URLs would defeat CDN caching for no privacy gain (the user already
--     consented to being seen).
--   - INSERT/UPDATE/DELETE: only the authenticated owner of the
--     containing folder. Object keys are "{user_id}/{rest...}", so the
--     first folder segment must match auth.uid().
--
-- Migrations are the source of truth for bucket existence; the comment in
-- supabase/config.toml.template tracks the inventory but does not provision.
-- Idempotent via ON CONFLICT.

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
