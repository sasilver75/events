# Migration tool: golang-migrate

Database schema migrations are managed by **`golang-migrate/migrate`** (`github.com/golang-migrate/migrate/v4`), with migration files written as **plain SQL** (`NNNN_name.up.sql` / `NNNN_name.down.sql`) and applied via the **standalone CLI** as a separate step in deploy/CI, not embedded in the server binary. `pressly/goose` and Supabase's own migration tooling are rejected.

## Why

[ADR 0005](./0005-supabase-data-plane-go-server-business-logic.md) line 35 commits migrations to live with the Go server and run against Supabase Postgres — Supabase's own migration tooling is explicitly not the source of truth. The choice here is between the two leading idiomatic-Go tools and between embedded vs. external execution.

Three forces shape the decision.

The first is **portability of the migration files themselves**. Migrations encode the *schema*, which is the part of the system most likely to outlive any Go-stack choice we make. SQL-only `.up.sql` / `.down.sql` files are runnable in `psql` directly, reviewable as DDL (Data Definition Language) by anyone who reads SQL, and trivially convertible if we ever swap migration tools or leave Go entirely. `goose`'s Go-function migrations give up that portability — a Go-as-migration ties the schema's history to the goose API and the Go runtime that compiled it. v0 has zero need for migrations to run application code, so the flexibility is pure cost.

The second is **operational separation of "deploy code" from "change schema"**. Embedding migrations into the server binary (so `fly deploy` runs migrations as a side effect of starting the new machine) is convenient until two machines try to migrate at once, or until a migration fails halfway and the binary refuses to start, or until we want to apply a migration without shipping new code. Running migrations as a separate `migrate up` step in the deploy pipeline keeps "change the database" as its own observable action with its own success/failure signal — the kind of boundary a learning project benefits from making explicit.

The third is **CLI ergonomics for development**. `migrate create -ext sql -dir db/migrations -seq <name>` produces the next-numbered up/down pair; `migrate up` / `migrate down 1` / `migrate goto N` cover the local development loop without writing any Go. `goose` has comparable commands. The two are roughly even on this dimension; the SQL-portability axis breaks the tie.

## Shape

- **Tool:** `github.com/golang-migrate/migrate/v4` (the v4 line) with the `postgres` database driver and the `file` source driver.
- **Migration files:** `/server/db/migrations/NNNN_name.up.sql` and `NNNN_name.down.sql`, four-digit zero-padded sequence (`0001_init.up.sql`, `0002_users.up.sql`, …).
- **First migration:** `0001_init` is a no-op for this issue — `migrate` itself creates `schema_migrations` automatically on first run, so `0001_init.up.sql` can be a single SQL comment. Real schema lands in [Issue #7](https://github.com/sasilver75/events/issues/7).
- **Local development:** `migrate -database $DATABASE_URL -path server/db/migrations up`, with `DATABASE_URL` pointing at the Supabase CLI's local Postgres (default `postgres://postgres:postgres@127.0.0.1:54322/postgres`; see `server/.env.example` and [ADR 0014](./0014-secret-management.md)). Wrapped in a `make migrate-up` / `make migrate-down` target in `/server/Makefile` so contributors don't need to remember flags.
- **CI / deploy:** migrations apply as a discrete step before the server is rolled forward. Concretely on Fly: `fly ssh console -C "migrate -database $DATABASE_URL -path /app/db/migrations up"` (or a dedicated migration job) — exact wiring lands when CI is set up in [Issue #6](https://github.com/sasilver75/events/issues/6).
- **Down migrations:** authored alongside every up migration. Used in development; not run in production except as part of a deliberate rollback. v0 does not auto-rollback on migration failure.
- **Connection:** uses the same `DATABASE_URL` the server reads, but the migration tool runs as its own process with its own short-lived connection — never sharing the server's `pgx` pool.
- **Locking:** `golang-migrate` takes a Postgres advisory lock during `up`/`down`, so concurrent runs (e.g., two CI jobs) serialize safely.

## Considered alternatives

- **`pressly/goose`.** Rejected on two counts. Go-as-migrations adds a portability tax we don't need (every schema change ties to the Go API). The single-binary embed story is goose's real edge, but we want migrations as a separate deploy step regardless, which neutralizes it. Both tools support SQL-only files; the differentiator is the parts we *don't* want.
- **Supabase migrations (`supabase db push` / SQL files in `supabase/migrations/`).** Rejected per ADR 0005 line 35 — Supabase's tooling is not the source of truth. The Go server owns schema; running two competing migration histories against the same database invites drift. The Supabase CLI is still used for *running the local Postgres stack* (per the local-dev workflow in `server/README.md`), but migrations against that Postgres go through `golang-migrate`, not `supabase db push`.
- **Atlas (`ariga/atlas`).** Rejected: declarative-schema model is powerful but heavier than v0 needs, and the operational story (HCL config, schema-diff vs. migration files, optional cloud component) introduces concepts we'd be learning instead of shipping.
- **Hand-applied SQL via `psql`.** Rejected: no migration history table means no idempotency guarantee and no way to verify "did this run on staging?" without inspection. Migration tooling pays for itself the moment a second environment exists.
- **Embedded migrations (run on server boot via `migrate.WithInstance`).** Rejected as an *execution model*, not as a tool — `golang-migrate` supports embedding, we're choosing not to use it. Reasoning above: separation of concerns between "deploy code" and "change schema."
- **ORM-bundled migrations (`gorm` auto-migrate, `bun` migrations).** Rejected: ADR 0005 commits to `pgx` and SQL — no ORM in the stack to bundle migrations into.

## Consequences

- **Two binaries to install for development.** Contributors need both Go (for the server) and the `migrate` CLI (for migrations) on their machines. Documented in `/server/README.md`. Acceptable; both are one-line installs.
- **Schema and code can drift in time.** Because migrations run separately, it's possible to deploy a new server version against an old schema (or vice versa). v0 handles this with deploy-order discipline (migrate first, then deploy code, for additive changes; reverse for removals) rather than tooling. Documented in `/server/README.md`.
- **Down migrations are real code we maintain.** Every up needs a corresponding down. Worth the effort at v0 — losing local-development reversibility would slow the feedback loop more than writing the inverse SQL costs.
- **No declarative schema diffing.** When the schema gets large enough that hand-writing migrations from "what changed since last week?" becomes painful, revisit Atlas or `sqlc`-style generation. Not v0's problem.
- **`schema_migrations` table appears in the database.** Created automatically by `migrate` on first run. Mentioned here so it isn't mistaken for an application table during schema review.
- **Distribution gate.** Before shipping to a real audience, revisit: automated rollback on failed migration, dry-run/lint step in CI, and per-environment migration history checks. None earn their keep at v0 scale.
