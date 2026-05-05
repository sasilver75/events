# Singular `host_user_id`, defer multi-host to `event_hosts` join table

The `events` table carries a single **`host_user_id UUID NOT NULL`** column. Every host-side affordance — edit, cancel, view Committers, message Committers — targets this one user. Co-hosting (multiple users with host-level rights over the same Event) is a recognized future feature but is not designed for in the v0 schema. When co-hosting becomes a scoped feature, schema migrates to an `event_hosts` join table; until then, singular.

## Why

Multi-host is plausible — small-Cap Events run by friend-pairs, partner-curated Events with multiple staff, future "co-organized" social shapes. None of these are in [PRD-v0.md](../../PRD-v0.md) Wave 1. Designing a join table now is designing for a hypothetical.

The cost of singular is that whatever queries and code paths assume one host (the `host_user_id` column reference, the `is_host = (event.host_user_id = auth.uid())` permission check pattern, the host-only mutation handlers) need updating when multi-host lands. That update is mechanical and bounded — it spans schema + a known set of queries, not a re-architecture. The migration recipe is straightforward:

```sql
CREATE TABLE event_hosts (
    event_id UUID NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    user_id  UUID NOT NULL REFERENCES users(id)  ON DELETE CASCADE,
    role     TEXT NOT NULL DEFAULT 'primary' CHECK (role IN ('primary', 'co')),
    PRIMARY KEY (event_id, user_id)
);
INSERT INTO event_hosts (event_id, user_id, role)
    SELECT id, host_user_id, 'primary' FROM events;
ALTER TABLE events DROP COLUMN host_user_id;
```

No data loss, no semantic ambiguity, advisory-locked under `golang-migrate` ([ADR 0017](./0017-migration-tool-golang-migrate.md)).

The cost of *premature* multi-host is harder to undo: every query, every permission check, every host-affordance handler in v0 ships with multi-host shape baked in, even though we don't yet know what role semantics we want (`primary` + `co`? equal peers? `creator` + `host` split?). Locking that shape against an unknown product question is the worse risk.

## Considered alternatives

- **`event_hosts` join table from day one.** Rejected: designs against a hypothetical. Forces an arbitrary role-semantics choice (peer co-hosts vs. primary+co, who can cancel, who receives notifications) ahead of any concrete co-hosting feature spec. The migration when multi-host is real is small enough that paying the cost now is unjustified.
- **Nullable secondary column (`host_user_id_2`).** Rejected: ad-hoc, doesn't scale past two hosts, and embeds the same permission-check refactor cost without the clean data model of a join table.
- **Polymorphic `hosted_by` (UUID + type discriminator covering "user" or "team").** Rejected: same as above plus introduces a teams/orgs concept v0 has no use for.

## Consequences

- **Singular host is a load-bearing assumption in v0 code.** Permission checks, RLS policies ([0004_rls.up.sql](../../server/db/migrations/0004_rls.up.sql)), host-action handlers, and host-facing iOS views all assume one host. They will need targeted updates when multi-host lands.
- **Trigger for revisiting:** the first scoped co-hosting feature ticket. At that point, this ADR is superseded by a new ADR that decides role semantics (peer vs. primary/co), notification routing among co-hosts, and which actions require unanimity vs. any-host-acts.
- **β-Events are unaffected.** [ADR 0001](./0001-seeders-cannot-cancel.md) establishes that β-Event Seeders are deliberately *not* hosts. Multi-host concerns the α-Event shape only.
