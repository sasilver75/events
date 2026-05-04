# Spur

A spontaneous-events iOS app: discover and Commit to nearby Events run by
strangers, with a reputation-shaped trust system so stranger-meeting feels
safer than the alternative.

v0 is a personal/learning project — a few hundred users at most. Trade
safety/scale work that doesn't earn its keep at this scale; flag deferrals
with explicit "upgrade before distribution" notes.

## Canonical docs

- [`PRD-v0.md`](./PRD-v0.md) — hybrid product spec + technical posture.
  User stories, functional requirements, and load-bearing technical decisions.
- [`CONTEXT.md`](./CONTEXT.md) — domain vocabulary. Use these terms verbatim
  in code, issues, commits, and tests.
- [`docs/adr/`](./docs/adr/) — architecture decision records. Hard-to-reverse
  + surprising + real-trade-off decisions.
- [`CLAUDE.md`](./CLAUDE.md) — project conventions for AI-assisted work.

## Layout

- [`ios/`](./ios/) — Xcode project root (Swift / SwiftUI)
- [`server/`](./server/) — Go HTTP server (business logic, writes)
- [`db/`](./db/) — Supabase Postgres migrations and seeds
- [`scripts/`](./scripts/) — repo-level dev scripts
- [`docs/`](./docs/) — ADRs and agent-skill pointer docs
- [`explainers/`](./explainers/) — long-form rationale documents

## Architecture in one paragraph

iOS-first native (Swift / SwiftUI) talks directly to Supabase for Auth,
Realtime, Storage, and RLS-protected reads. Any write that carries rules —
Commit, Withdraw, create event, check-in, rate, friend — goes through a Go
HTTP server that talks to Supabase Postgres via `pgx`. Business logic lives
in Go; PL/pgSQL is reserved for mechanical concerns. See
[ADR 0005](./docs/adr/0005-supabase-data-plane-go-server-business-logic.md).
