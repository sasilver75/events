# server/

Go module root for the Spur HTTP server.

The server owns every write that carries rules — Commit, Withdraw, create
event, check-in, rate, friend — and talks to Supabase Postgres via `pgx`.
Reads that are pure data fetch go straight from iOS to Supabase under RLS;
they don't pass through here. See
[ADR 0005](../docs/adr/0005-supabase-data-plane-go-server-business-logic.md).

Stack ([ADR 0015](../docs/adr/0015-deploy-target-fly-io.md),
[0016](../docs/adr/0016-http-router-chi.md),
[0017](../docs/adr/0017-migration-tool-golang-migrate.md)):

- **Deploy:** Fly.io, region `sjc`
- **Router:** `go-chi/chi/v5` on stdlib `net/http`
- **Migrations:** `golang-migrate/migrate`, plain SQL files in `db/migrations/`
- **Driver:** `pgx` (added when first DB-backed endpoint lands)

Business logic lives in Go, never in PL/pgSQL — see
[`CLAUDE.md`](../CLAUDE.md).

## Layout

```
server/
├── cmd/server/         # main package, entrypoint
├── db/migrations/      # NNNN_name.up.sql / .down.sql (golang-migrate)
├── Dockerfile          # multi-stage build for Fly
├── fly.toml            # Fly app config (app, region, healthcheck)
├── Makefile            # run, build, test, migrate-*, deploy
├── go.mod / go.sum
├── .env.example        # template for local-dev env vars
└── README.md
```

## Prereqs

- Go 1.26+ (`brew install go`)
- Docker Desktop — required by the Supabase CLI to run the local stack
- Supabase CLI (`brew install supabase/tap/supabase`)
- `golang-migrate` CLI (`brew install golang-migrate`) — applies our SQL migrations
- `flyctl` (`brew install flyctl`) — only needed for deploy

## Local development

The Go server reads its database and Supabase credentials from environment
variables. For local development those values come from the Supabase CLI's
local stack — Postgres, Auth, Storage, Realtime, and Studio all running in
Docker. Env-var disposition (what's a secret, what's public, where each lives)
is governed by [ADR 0014](../docs/adr/0014-secret-management.md).

### First-time setup

1. **Boot the local stack** from the repo root:
   ```sh
   supabase start
   ```
   First run pulls images (~1 GB) and takes a few minutes. Subsequent runs
   are seconds. The CLI prints the local URLs and credentials when it
   finishes.
2. **Verify the stack is reachable**:
   ```sh
   ./scripts/check-supabase-connection.sh
   ```
3. **Copy the local credentials into `server/.env`**:
   ```sh
   cp server/.env.example server/.env
   # then fill SUPABASE_ANON_KEY / SUPABASE_SERVICE_ROLE_KEY from `supabase status`
   ```
4. **Apply our migrations** to the local Postgres:
   ```sh
   cd server && make migrate-up
   ```
5. **Open Studio** at <http://127.0.0.1:54323> to inspect the local DB.

### Day-to-day

Supabase CLI:

- `supabase start` — boot the local stack (idempotent)
- `supabase stop` — shut it down
- `supabase status` — re-print local URLs and credentials
- `supabase db reset` — wipe local Postgres (re-run `make migrate-up` after)

Go server:

```sh
cd server
make run                    # starts server on :8080
curl localhost:8080/healthz # → 200 {"status":"ok"}
```

`PORT` defaults to `8080`. Override with `PORT=9090 make run`.

### Migrations

Schema migrations live in `server/db/migrations/` as plain SQL files
(`NNNN_name.up.sql` / `NNNN_name.down.sql`) and are applied with
`golang-migrate`, not `supabase db push` (see
[ADR 0017](../docs/adr/0017-migration-tool-golang-migrate.md)). The Supabase
CLI is just our local Postgres host.

```sh
export DATABASE_URL="postgresql://postgres:postgres@127.0.0.1:54322/postgres"

make migrate-up                       # apply all pending migrations
make migrate-down                     # roll back one migration
make migrate-create name=add_events   # scaffold next NNNN_add_events.{up,down}.sql
```

After `supabase db reset`, re-run `make migrate-up` to reapply.

### Promoting to staging

Migrations promote to the hosted Supabase staging project the same way: run
`migrate -database $STAGING_DATABASE_URL -path db/migrations up` against the
staging pooler URL. There is no separate production project for v0 — see the
waiver on [issue #4](https://github.com/sasilver75/events/issues/4).

Migrations apply as a separate step from server deploy
(ADR 0017 §Consequences) — the binary does not self-migrate on startup.

## Deploy (Fly.io)

First time:

```bash
fly auth login
fly launch --no-deploy --copy-config --name spur-server --region sjc
fly secrets set DATABASE_URL=<supabase-staging-pooler-url> \
                SUPABASE_SERVICE_ROLE_KEY=<staging-service-role-key>
fly deploy
```

Subsequent deploys:

```bash
make deploy   # → fly deploy
```

Public env (`SUPABASE_URL`, `SUPABASE_ANON_KEY`, `PORT`) lives in
`fly.toml [env]`; sensitive env (`DATABASE_URL`,
`SUPABASE_SERVICE_ROLE_KEY`) is set via `fly secrets set` only — never
committed, never in `fly.toml`, never in a `.env` file. Disposition table:
[ADR 0014](../docs/adr/0014-secret-management.md).

## Operations

```bash
fly logs                    # tail production logs
fly status                  # machine state, region, healthcheck
fly releases                # deploy history, rollback target
fly ssh console             # exec into the running machine
fly machines list
```

Production log retention is whatever Fly's default is (~30 days at time of
writing). No external aggregator at v0 — revisit before distribution
([ADR 0015 §Consequences](../docs/adr/0015-deploy-target-fly-io.md)).

Fly platform status: <https://status.flyio.net>.

## Configuration

The Go process reads env vars via `os.Getenv` — no `.env` file is loaded in
production. Today's binary reads only `PORT`; `DATABASE_URL`,
`SUPABASE_URL`, `SUPABASE_ANON_KEY`, and `SUPABASE_SERVICE_ROLE_KEY` are
wired in by issues #4 / #7 / #9. The full disposition (which value is
public vs secret, and where each lives in each environment) is in
[ADR 0014](../docs/adr/0014-secret-management.md).

## Deploy-order discipline

Schema and code can drift in time because migrations run as a separate step
(ADR 0017 §Consequences). Rule of thumb:

- **Additive change** (new column, new table, new index): migrate first, then deploy.
- **Removal / rename** (drop column, rename table): deploy code that no longer
  references the old shape first, then migrate.
