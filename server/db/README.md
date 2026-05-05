# server/db

Schema migrations for the Spur Postgres database, applied via
[`golang-migrate`](https://github.com/golang-migrate/migrate) per
[ADR 0017](../../docs/adr/0017-migration-tool-golang-migrate.md).

Migrations are plain SQL pairs (`NNNN_name.up.sql` / `NNNN_name.down.sql`)
under `migrations/`, applied via the `migrate` CLI as a discrete deploy step.
The Go server does **not** run migrations on boot — see ADR 0017 §Shape.

## Running migrations

Local development (against the Supabase CLI's Postgres on `:54322`):

```bash
make migrate-up        # apply all pending
make migrate-down      # roll back one
make migrate-create name=<snake_case_name>
```

`DATABASE_URL` is read from `server/.env` (see `.env.example`). For hosted
environments, `DATABASE_URL` is set in the deploy platform's secret store
([ADR 0014](../../docs/adr/0014-secret-management.md)).

## Wave-1 schema (issue #7)

Three tables underpin the Wave-1 browse + Commit/Withdraw loop. Vocabulary
follows [`CONTEXT.md`](../../CONTEXT.md) verbatim.

### `public.users`

Mirrors the row in `auth.users` that Supabase Auth creates on phone-OTP
sign-up. The mirror is populated by a **Postgres `AFTER INSERT` trigger**
on `auth.users` (migration `0006_mirror_auth_users`,
[ADR 0022](../../docs/adr/0022-auth-users-mirror-trigger.md)), which fires
inside Supabase Auth's own transaction. This guarantees that the moment an
`auth.users` row exists, the matching `public.users` row exists too — no
half-state can be observed by RLS policies, FK targets, or the Go server.

The trigger is the only PL/pgSQL the project ships at v0. It fits ADR
0005's "DB triggers acceptable for mechanical concerns" carve-out: pure
row mirroring on the same identifier, no domain rules, no conditionals
beyond `ON CONFLICT DO NOTHING`. ADR 0022 records the decision and defines
what counts as "mechanical" for future judgment calls.

A previous draft of issue #9 had the Go middleware do a lazy upsert on
every authenticated request. ADR 0022 reversed that.

| Column         | Type          | Notes                                |
| -------------- | ------------- | ------------------------------------ |
| `id`           | `UUID PK`     | FK to `auth.users(id)` ON DELETE CASCADE |
| `created_at`   | `TIMESTAMPTZ` | Default `now()`                      |
| `display_name` | `TEXT`        | Nullable for v0; profile UX deferred |

### `public.events`

A single Wave-1 Event has fixed Cap, fixed start time, a real geographic
center, and one Category from the v0 fixed taxonomy.

**Exact `center_lat` / `center_lon` are stored.** The fuzz that pre-Commit
viewers see (per CONTEXT.md §Fuzzed center) is computed in the Go API at
read time, deterministic per `(event_id, viewer_user_id)`, never persisted.
This keeps the DB layer free of business logic and lets the fuzz algorithm
evolve without a schema change.

`category` is enforced by `CHECK` constraint against the v0 taxonomy. We
prefer a `CHECK` to a Postgres `ENUM` because expanding an `ENUM` requires
a migration; expanding a `CHECK` is one `ALTER TABLE` line.

Indexes:

- `events_start_at_idx` — btree on `start_at`, for "upcoming" filtering.
- `events_geog_idx` — GIST on
  `ST_SetSRID(ST_MakePoint(center_lon, center_lat), 4326)::geography`.
  The cast to `geography` lets `ST_DWithin` accept distances in meters
  rather than degrees, which is what the Wave-1 `near=lat,lon&radius_m=…`
  endpoint (issue #10) needs.

### `public.commits`

A row per (Event, user) pair. Composite primary key `(event_id, user_id)`
gives idempotent INSERT-on-Commit and a single index that covers both
"this user's commits" and "this event's commits" lookups (the PK btree
serves prefix queries on `event_id`).

**Cap is enforced in the Go Commit handler, not in Postgres.** A static
`CHECK` cannot express "row count for this `event_id` does not exceed
`events.cap`" — the rule crosses rows. Issue #11 lands the Go-side
transactional enforcement (row-locking the parent `events` row, counting,
inserting iff under cap).

## Row-Level Security

RLS is enabled on all three tables. Policies are deliberately minimal for
Wave 1 — write operations all flow through the Go server, which connects
as `postgres` (RLS-bypassing) and enforces business rules itself. RLS only
governs **direct** iOS → Supabase reads (PostgREST, Realtime).

| Table     | Policy                                       |
| --------- | -------------------------------------------- |
| `users`   | A user may `SELECT` their own row only.      |
| `events`  | Any authenticated user may `SELECT` any row. |
| `commits` | A user may `SELECT` their own commits only.  |

Co-attendee visibility on `commits` (so Committed Attendees can see who
else committed) is deferred to the friends wave. Public profile projection
on `users` is deferred to the same.

`auth.uid()` returns the JWT subject, set by Supabase from the iOS
client's bearer token.

## Curated event seed (issue #8)

A small set of hand-curated α-Events lives in `seeds/` and is applied via
`make seed` (from `/server/`). The seed populates `events` rows with
`source = 'curated'` and a stable `seed_key`, hosted by a dedicated
**Spur Seed** Supabase Auth user (`spur-seed@spur.local`).

The runner is a Go program because provisioning the Spur Seed Auth user
requires the Supabase Auth Admin API, not raw SQL. On each invocation it:

1. Looks up `spur-seed@spur.local` in `auth.users` via the Admin API; creates
   the user (with `email_confirm: true`) if absent. Captures the UUID.
2. Upserts the corresponding `public.users` row (`display_name = 'Spur Seed'`).
3. Upserts each curated Event keyed by `seed_key`, refreshing time-sensitive
   fields like `start_at` so re-running the seed keeps Events in the upcoming
   72-hour window.

The maintainer does not get in-app affordances over seed Events — to change
them, edit `seeds/events.go` and re-run. This is intentional: seed Events are
platform content. Singular `host_user_id` is the deliberate v0 choice — see
[ADR 0018](../../docs/adr/0018-singular-host-defer-multi-host.md).

```bash
make seed     # idempotent; runs locally and against staging
```

Required env: `DATABASE_URL`, `SUPABASE_URL`, `SUPABASE_SERVICE_ROLE_KEY`
(see `/server/.env.example`).

## Integration test

`schema_test.go` connects to Postgres via `pgx`, verifies PostGIS is
installed, the three tables exist with RLS enabled, each has at least one
SELECT policy, and the spatial index is in place. Skips when
`DATABASE_URL` is unset (so `go test ./...` is safe in environments
without a database — CI sets the var per issue #6).
