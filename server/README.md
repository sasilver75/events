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
