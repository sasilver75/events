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
- [`WORKFLOW.md`](./WORKFLOW.md) — Symphony-style unattended-agent workflow
  contract consumed by the Spur orchestrator.

## Layout

- [`ios/`](./ios/) — Xcode project root (Swift / SwiftUI)
- [`server/`](./server/) — Go HTTP server (business logic, writes)
- [`db/`](./db/) — Supabase Postgres migrations and seeds
- [`scripts/`](./scripts/) — repo-level dev scripts
- [`docs/`](./docs/) — ADRs and agent-skill pointer docs
- [`explainers/`](./explainers/) — long-form rationale documents
- [`orchestrator/`](./orchestrator/) — Symphony-like Linear → Tart VM →
  Codex harness for unattended agent runs

## Architecture in one paragraph

iOS-first native (Swift / SwiftUI) talks directly to Supabase for Auth,
Realtime, Storage, and RLS-protected reads. Any write that carries rules —
Commit, Withdraw, create event, check-in, rate, friend — goes through a Go
HTTP server that talks to Supabase Postgres via `pgx`. Business logic lives
in Go; PL/pgSQL is reserved for mechanical concerns. See
[ADR 0005](./docs/adr/0005-supabase-data-plane-go-server-business-logic.md).

## Local development

The local stack is the Supabase CLI running Postgres + Auth + Storage +
Realtime + Studio in Docker. One command boots everything.

```sh
brew install supabase/tap/supabase   # one-time
supabase start                       # boot the stack
./scripts/check-supabase-connection.sh
```

Copy `server/.env.example` to `server/.env` and fill in the values that
`supabase start` prints. `.env` is gitignored.

Full walkthrough — including Docker prerequisites, day-to-day commands, and
how migrations promote from local → staging → production — lives in
[`server/README.md`](./server/README.md#local-development).

### Pre-commit format hooks

`.pre-commit-config.yaml` runs `gofmt` + `goimports` on staged Go files
and `swift-format` on staged Swift files. If a formatter rewrites a file,
the commit aborts; re-stage and re-commit.

One-time setup per clone:

```sh
brew install pre-commit swift-format
go install golang.org/x/tools/cmd/goimports@latest
pre-commit install
```

To check the whole tree before opening a PR:

```sh
pre-commit run --all-files
```

A CI check (issue #6) will guard against contributors who skip
`pre-commit install`.
