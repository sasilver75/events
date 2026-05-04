# Friendships are stored as mirrored two-row pairs, not canonical-pair single rows

The `friendships` table stores **one row per directed half** of each mutual friendship. A friendship between users A and B is represented by two rows: `(A, B)` and `(B, A)`. The Go server writes both rows in a single transaction on accept, deletes both on unfriend.

```sql
friendships (
  user_id    UUID NOT NULL REFERENCES users,
  friend_id  UUID NOT NULL REFERENCES users,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (user_id, friend_id)
)
```

Pending requests live in a separate, single-row table — there is no `status` column on `friendships`:

```sql
friendship_requests (
  requester  UUID NOT NULL REFERENCES users,
  recipient  UUID NOT NULL REFERENCES users,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (requester, recipient)
)
```

Lifecycle:
- **Request:** insert into `friendship_requests`.
- **Accept:** in one transaction, delete from `friendship_requests` and insert two mirrored rows into `friendships`.
- **Reject / withdraw request:** delete from `friendship_requests`.
- **Unfriend:** in one transaction, delete both `(A, B)` and `(B, A)` from `friendships`.

## Why mirrored over canonical-pair

The natural alternative (A) stores one row per friendship with canonicalized IDs (`CHECK user_a < user_b`). That's structurally cleaner — one row per friendship, no consistency invariant to maintain. We chose mirrored (B) anyway, primarily for **RLS correctness surface**.

A friend-graph-aware RLS policy under canonical-pair must canonicalize the pair on every evaluation:

```sql
-- Canonical-pair (A):
USING (EXISTS (
  SELECT 1 FROM friendships
  WHERE user_a = LEAST(auth.uid(), row_owner)
    AND user_b = GREATEST(auth.uid(), row_owner)
))
```

Forget the `LEAST/GREATEST` and the policy returns wrong row sets — silently. RLS failures do not throw; they just leak rows or hide legitimate ones. The bug class is hard to test exhaustively (negative test coverage in particular is weak) and easy to introduce when adding a new policy or refactoring an old one.

Under mirrored (B), the equivalent policy is the most ordinary SQL pattern, with no canonicalization to forget:

```sql
-- Mirrored (B):
USING (EXISTS (
  SELECT 1 FROM friendships
  WHERE user_id = auth.uid() AND friend_id = row_owner
))
```

The product depends on friend-graph RLS for at least:

- Friends-only Event gating (a non-friend cannot Commit to a friends-only Event)
- Attend-privately friend-graph hiding (the user's avatar is hidden from their own friend graph in the context of the Event)
- DM eligibility (you can DM only friends)
- Bring-a-friend invitation eligibility (the invited user must be a mutual Friend of the inviter)

A wrong RLS policy in any of these is a privacy or safety failure. Reducing the surface area for that bug class is worth the modest costs the mirrored representation carries.

Performance is roughly a wash. At v0 scale, both schemas execute single-row checks as one PK index seek (microseconds). Multi-row queries are ~2× faster on mirrored — fewer index hits, no `OR` branch — but both are sub-millisecond at any realistic v0 dataset. The deciding factor is correctness, not speed.

## Costs of mirrored

- **Storage:** ~30 bytes per friendship row, doubled. At a hypothetical 10K users with 100 friends each, total = 2M rows × 30 bytes = 60MB. Negligible.
- **Write fan-out:** friendship-accept writes 2 rows; unfriend deletes 2 rows. Trivial.
- **Mirror invariant:** every state change must touch both rows. Enforced in the Go friendship-accept and unfriend handlers, transactionally. No PL/pgSQL trigger is needed. (Per the no-business-logic-in-PL/pgSQL principle business rules belong in Go; mirror maintenance is mechanical and could legitimately be a trigger as defense-in-depth, but a transactional Go write is the simpler primary mechanism.)

## Considered alternatives

- **A — Canonical-pair single row** (`PRIMARY KEY (user_a, user_b) CHECK user_a < user_b`). Rejected for RLS correctness reasons above. Storage and write costs are slightly lower but negligible. The secondary index on `user_b` required for reverse-direction queries reclaims most of the storage saving.
- **C — Single row with directional `requester`/`recipient` retained on `friendships`.** Rejected: preserves request history that we cleanly handle in a separate `friendship_requests` table, and inherits all of A's canonicalization problems on friend-graph queries.
- **D — Status column on `friendships` (`pending` | `accepted` | `blocked`) instead of separate request table.** Rejected: muddles the lifecycle. Pre-accept the row's mirror does not exist; post-accept it does. Asymmetric state on a table that is otherwise symmetric is harder to reason about than two simple tables.

## Consequences

- The `friendship-accept` handler in the Go server writes two rows in a single transaction. The `unfriend` handler deletes two rows in a single transaction. Both must be tested for the case where one row exists without its mirror — treat as a corrupted state (log + heal, or error). Defer the exact handling until tangible.
- RLS policies reasoning about friend-graph membership use the natural `WHERE user_id = $A AND friend_id = $B` pattern. No `LEAST` / `GREATEST` canonicalization is required anywhere.
- Mutual-friend lookups (bring-a-friend candidate list, profile mutual-friends count) are a clean self-join on `friend_id`.
- Blocking semantics are not addressed by this ADR — see PRD §Open Questions. A separate `blocks` table is the natural fit (asymmetric, single row per block) and is independent of the friendship representation.
- A future contributor reading the schema may wonder why friendships are stored as duplicated rows. This ADR explains: it is a deliberate denormalization for RLS correctness, not a mistake or oversight.
