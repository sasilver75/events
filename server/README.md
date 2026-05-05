# server/

Go module root for the Spur HTTP server.

The server owns every write that carries rules — Commit, Withdraw, create
event, check-in, rate, friend — and talks to Supabase Postgres via `pgx`.
Reads that are pure data fetch go straight from iOS to Supabase under RLS;
they don't pass through here. See
[ADR 0005](../docs/adr/0005-supabase-data-plane-go-server-business-logic.md).

Populated by [issue #2](https://github.com/sasilver75/events/issues/2): Go
server stack — ADRs (deploy target, HTTP router, migration tool) and a
deployed `/healthz`.

Business logic lives in Go, never in PL/pgSQL — see
[`CLAUDE.md`](../CLAUDE.md).

## Local development

The Go server reads its database and Supabase credentials from environment
variables. For local development those values come from the Supabase CLI's
local stack — Postgres, Auth, Storage, Realtime, and Studio all running in
Docker.

### First-time setup

1. **Install Docker Desktop** — required by the Supabase CLI to run the
   local stack. https://docs.docker.com/desktop/install/mac-install/
2. **Install the Supabase CLI**:
   ```sh
   brew install supabase/tap/supabase
   ```
3. **Boot the local stack** from the repo root:
   ```sh
   supabase start
   ```
   First run pulls images (~1 GB) and takes a few minutes. Subsequent runs
   are seconds. The CLI prints the local URLs and credentials when it
   finishes — copy them into `server/.env` (see `server/.env.example`).
4. **Verify the stack is reachable**:
   ```sh
   ./scripts/check-supabase-connection.sh
   ```
5. **Open Studio** at http://127.0.0.1:54323 to inspect the local DB.

### Day-to-day

- `supabase start` — boot the local stack (idempotent)
- `supabase stop` — shut it down
- `supabase status` — re-print local URLs and credentials
- `supabase db reset` — wipe local Postgres and re-apply migrations + seeds

### Promoting migrations

Migrations live under `db/` (populated by issue #7). Once a migration is
verified locally, promote to the hosted staging project with
`supabase db push`. There is no separate production project for v0 — see
the waiver on [issue #4](https://github.com/sasilver75/events/issues/4).

### Staging

Server runtime secrets (`DATABASE_URL`, `SUPABASE_SERVICE_ROLE_KEY`, etc.)
are provided by the deploy platform's secret store — never by a checked-in
`.env`. See [issue #2](https://github.com/sasilver75/events/issues/2) for
the deploy-target ADR.
