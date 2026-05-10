DROP POLICY IF EXISTS avatars_owner_delete ON storage.objects;
DROP POLICY IF EXISTS avatars_owner_update ON storage.objects;
DROP POLICY IF EXISTS avatars_owner_insert ON storage.objects;
DROP POLICY IF EXISTS avatars_public_read ON storage.objects;

SET LOCAL storage.allow_delete_query = 'true';

DELETE FROM storage.objects WHERE bucket_id = 'avatars';
DELETE FROM storage.buckets WHERE id = 'avatars';
