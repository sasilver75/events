-- Per-Event chat (#65, PRD §At-event, ADR 0006). Unlocks at create (α)
-- or Tip (β) — that gate lives in the Go POST handler, not in the schema,
-- because RLS can read `tipped_at` for the read side but the write path is
-- server-mediated and pre-validates lock state alongside attendance.
--
-- SSE fan-out per ADR 0006: an AFTER INSERT trigger emits
-- NOTIFY chat_message, '{"event_id":<uuid>,"message_id":<bigint>}'.
-- A Postgres LISTEN consumer in the Go server routes per (event_id) into
-- per-stream subscriber sets.
--
-- `id BIGSERIAL` is the SSE `Last-Event-ID` cursor. Clients reconnecting
-- include it; the replay query is
--   SELECT … FROM event_messages WHERE event_id = $1 AND id > $2 ORDER BY id.
-- The sequence is global (not per-event), which is fine because a single
-- SSE connection only subscribes to one event's stream — monotonicity per
-- stream is what matters, not per-row density.
--
-- Server-only writes: the Go handler validates Committed-Attendee status,
-- α-vs-β unlock state, and message kind before inserting. RLS denies all
-- authenticated writes; reads are scoped to Committed Attendees so the
-- iOS client can fetch history and stream directly through Supabase if a
-- future surface wants to (today everything goes through the Go server).

CREATE TABLE public.event_messages (
    id        BIGSERIAL  PRIMARY KEY,
    event_id  UUID       NOT NULL REFERENCES public.events(id) ON DELETE CASCADE,
    -- Nullable so system messages have no sender; the
    -- event_messages_sender_matches_kind CHECK below pairs nullability
    -- with kind. We don't ON DELETE SET NULL on user messages — account
    -- deletion (#72) will rewrite sender_id to the tombstone user; raw
    -- referential cleanup would erase attribution prematurely.
    sender_id UUID       REFERENCES public.users(id),
    body      TEXT       NOT NULL CHECK (length(body) BETWEEN 1 AND 2000),
    sent_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    kind      TEXT       NOT NULL CHECK (kind IN ('user', 'system')),
    CONSTRAINT event_messages_sender_matches_kind CHECK (
        (kind = 'user'   AND sender_id IS NOT NULL)
     OR (kind = 'system' AND sender_id IS NULL)
    )
);

-- Primary query shape: replay-by-cursor and history-paginate, both
-- WHERE event_id = $1 AND id > $2 ORDER BY id. The composite index serves
-- both. (PK alone would force a sort + filter for the common path.)
CREATE INDEX event_messages_event_id_id_idx
    ON public.event_messages (event_id, id);

ALTER TABLE public.event_messages ENABLE ROW LEVEL SECURITY;

-- Read scope: only Committed Attendees of the Event can read its messages.
-- Strangers, friends-of-attendees, and curious non-Committed users get
-- nothing. The check is against the commits table directly because the
-- Withdraw path hard-deletes rows there (no soft-delete column).
CREATE POLICY event_messages_select_attendee ON public.event_messages
    FOR SELECT
    TO authenticated
    USING (
        EXISTS (
            SELECT 1
            FROM public.commits c
            WHERE c.event_id = event_messages.event_id
              AND c.user_id  = (SELECT auth.uid())
        )
    );

-- NOTIFY fan-out for SSE per ADR 0006. The trigger fires for both user
-- and system inserts so the check-in handler's first-presence system
-- message (issue AC) lands on connected SSE streams without the handler
-- having to remember a separate NOTIFY call.
CREATE OR REPLACE FUNCTION public.event_messages_notify()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    PERFORM pg_notify(
        'chat_message',
        json_build_object(
            'event_id',   NEW.event_id,
            'message_id', NEW.id
        )::text
    );
    RETURN NEW;
END;
$$;

CREATE TRIGGER event_messages_notify_after_insert
    AFTER INSERT ON public.event_messages
    FOR EACH ROW
    EXECUTE FUNCTION public.event_messages_notify();
