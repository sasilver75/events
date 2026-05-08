-- Friend graph foundation (#63, ADR 0010). Pending requests live in
-- friendship_requests (single row per direction). Accepted friendships live
-- in friendships as mirrored two-row pairs: a friendship between A and B is
-- stored as both (A, B) and (B, A). The mirror invariant is enforced by the
-- Go accept and unfriend handlers in a single transaction; no PL/pgSQL
-- trigger backstops it (per CLAUDE.md, no business logic in PL/pgSQL).
--
-- The mirrored representation lets friend-graph RLS use the natural
-- WHERE user_id = auth.uid() AND friend_id = $other pattern with no
-- LEAST/GREATEST canonicalization, materially shrinking the surface area
-- for silent RLS bugs (ADR 0010 §"Why mirrored over canonical-pair").
--
-- ON DELETE CASCADE on both columns of both tables aligns with PRD §Account
-- deletion (hard-delete friendships and pending requests when a user is
-- erased per ADR 0013).

CREATE TABLE public.friendship_requests (
    requester  UUID NOT NULL REFERENCES public.users(id) ON DELETE CASCADE,
    recipient  UUID NOT NULL REFERENCES public.users(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (requester, recipient),
    CHECK (requester <> recipient)
);

CREATE INDEX friendship_requests_recipient_idx
    ON public.friendship_requests (recipient);

CREATE TABLE public.friendships (
    user_id    UUID NOT NULL REFERENCES public.users(id) ON DELETE CASCADE,
    friend_id  UUID NOT NULL REFERENCES public.users(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, friend_id),
    CHECK (user_id <> friend_id)
);

CREATE INDEX friendships_friend_id_idx
    ON public.friendships (friend_id);

ALTER TABLE public.friendship_requests ENABLE ROW LEVEL SECURITY;
ALTER TABLE public.friendships         ENABLE ROW LEVEL SECURITY;

CREATE POLICY friendship_requests_select_either_side
    ON public.friendship_requests
    FOR SELECT
    TO authenticated
    USING ((SELECT auth.uid()) IN (requester, recipient));

CREATE POLICY friendships_select_own
    ON public.friendships
    FOR SELECT
    TO authenticated
    USING (user_id = (SELECT auth.uid()));
