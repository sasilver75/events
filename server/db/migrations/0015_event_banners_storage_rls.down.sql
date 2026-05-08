DROP POLICY IF EXISTS event_banners_owner_delete ON storage.objects;
DROP POLICY IF EXISTS event_banners_owner_update ON storage.objects;
DROP POLICY IF EXISTS event_banners_owner_insert ON storage.objects;
DROP POLICY IF EXISTS event_banners_public_read ON storage.objects;

-- storage.protect_delete trigger blocks direct DELETE on storage.* by
-- default. The bypass flag is the documented escape hatch for
-- migration-style rollbacks.
SET LOCAL storage.allow_delete_query = 'true';

DELETE FROM storage.objects WHERE bucket_id = 'event-banners';
DELETE FROM storage.buckets WHERE id = 'event-banners';
