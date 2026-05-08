-- Event banner image (#41).
--
-- banner_path stores the Supabase Storage object key (e.g.
-- "{user_id}/{event_id}.jpg"), not a URL — the iOS client constructs
-- public URLs at read time via the storage SDK. Nullable: banner is
-- optional and α-/β-Events both render a category-color fallback.
--
-- The bucket itself (event-banners, public-read, 2 MiB cap) is
-- declared in supabase/config.toml.template; storage RLS lives in
-- 0013_event_banners_storage.

ALTER TABLE public.events
    ADD COLUMN banner_path TEXT;
