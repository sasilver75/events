-- Event banner image (#41).
--
-- banner_path stores the Supabase Storage object key (e.g.
-- "{user_id}/{event_id}.jpg"), not a URL — the iOS client constructs
-- public URLs at read time via the storage SDK. Nullable: banner is
-- optional and α-/β-Events both render a category-color fallback.
--
-- The bucket and storage RLS for event-banners are in
-- 0015_event_banners_storage_rls (the bucket itself is created via
-- INSERT into storage.buckets there, with ON CONFLICT DO UPDATE so
-- it's idempotent across fresh and existing volumes).

ALTER TABLE public.events
    ADD COLUMN banner_path TEXT;
