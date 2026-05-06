# Spur

A spontaneous-events iOS app: discover and Commit to nearby Events run by
strangers, with a reputation-shaped trust system so stranger-meeting feels
safer than the alternative.

**v0 is a personal/learning project, not a commercial launch** — a few hundred
users at most. Trade safety/scale work that doesn't earn its keep at this
scale; flag deferrals with explicit "upgrade before distribution" notes. See
[`PRD-v0.md`](./PRD-v0.md) §Out of scope.

## Source of truth

- [`PRD-v0.md`](./PRD-v0.md) — hybrid PRD + technical posture. Product spec,
  user stories, functional requirements, and load-bearing technical decisions.
- [`CONTEXT.md`](./CONTEXT.md) — domain vocabulary. Use these terms verbatim
  in code, issues, commits, and tests. Don't drift to synonyms.
- [`docs/adr/`](./docs/adr/) — hard-to-reverse + surprising + real-trade-off
  decisions. When code or specs contradict an ADR, surface it explicitly
  rather than silently override.
- [`docs/glossary.md`](./docs/glossary.md) — external/technical terms
  (Twilio, A2P, JWKS, ATS, etc.). Distinct from `CONTEXT.md` (product
  domain) and ADRs (project decisions). Add an entry when you introduce
  or encounter a term a future reader might not know.

## Architecture at a glance

iOS-first native (Swift/SwiftUI) → Supabase directly for Auth, Realtime,
Storage, RLS-protected reads. iOS → Go HTTP server for any write that carries
rules (Commit, Withdraw, create event, check-in, rate, friend). Go →
Supabase Postgres via `pgx`. See
[ADR 0005](./docs/adr/0005-supabase-data-plane-go-server-business-logic.md).

**Business logic lives in Go, never in PL/pgSQL.** DB triggers acceptable
only for mechanical concerns (NOTIFY emission, denormalized counts). RLS is
fine — declarative access control isn't business logic.

Android deferred ([ADR 0003](./docs/adr/0003-ios-first-native.md)).

## Style

Default to **no comments**. Add one only when WHY is non-obvious: a hidden
constraint, a subtle invariant, a workaround for a specific bug. Never narrate
WHAT — well-named identifiers do that. No "added for X flow" or "used by Y" —
that's PR-description content.

Don't add features, refactors, or abstractions beyond the task. A bug fix
doesn't need surrounding cleanup. Three similar lines is better than a
premature abstraction. No half-finished implementations.

Don't add error handling, fallbacks, or validation for scenarios that can't
happen. Trust internal code and framework guarantees. Validate at system
boundaries only (user input, external APIs).

Per-language style is enforced by formatter, not prose:

- Go: `gofmt`, `golangci-lint`. Standard Go idioms.
- Swift: `swift-format` with project config. SwiftUI-first.

## Testing / TDD

Use `/tdd` for non-trivial features: red → green → refactor.

- **Integration tests hit a real Postgres**, not mocks. Mocked DBs let
  migrations and RLS regressions through.
- Tests live next to code (Go `_test.go`, Swift `*Tests.swift` targets).
- Test names use [`CONTEXT.md`](./CONTEXT.md) vocabulary.
- Skipping a flaky test is not a fix. Diagnose with `/diagnose`.

## Working flow

- **Issues** → GitHub Issues at `sasilver75/events`. `/to-issues` for vertical
  slices, `/triage` for state-machine moves. **Always triage before starting
  work**: replace `needs-triage` with the right role label (`ready-for-agent`,
  `ready-for-human`, `needs-info`, or `wontfix`). A closed issue must never
  still carry `needs-triage`.
- **PRDs** → `/to-prd` writes them to GitHub.
- **Branches** → one per issue. PRs target `main`.
- **PR titles** → when the work originates from a GitHub issue, append
  `(#N)` to the title (e.g. `auth: Phase 1 iOS … (#9)`). GitHub auto-links
  it and `gh pr list` becomes scannable for which PR addressed which issue.
- **Commits** → conventional prose; WHY in the message body.
- **Closeouts** → On every issue close, leave a comment. "Shipped per spec —
  <commit>" when the AC was met as written; a short drift list (what diverged
  from the spec and why) when not. The diff and commit messages don't capture
  decisions made under pressure — the closeout comment does.

## Multi-session coordination

Two Claude Code sessions can run in parallel against different worktrees.
Without partitioning, they will clobber each other's iOS simulator state and
build artifacts. Rules:

- **Pin one simulator per worktree.** Each worktree root holds a gitignored
  `.spur-sim` file with `SPUR_SIM_UDID`, `SPUR_SIM_NAME`, and
  `SPUR_DERIVED_DATA`. Always read it before any `simctl` / `xcodebuild` /
  XcodeBuildMCP call. **Never** target `booted` or
  `generic/platform=iOS Simulator` — they are shared.
- **Create `.spur-sim` if missing.** Run `xcrun simctl list devices available`
  to find an `iPhone 17*` UDID, cross-check sibling worktrees'
  `.spur-sim` (`cat ../*/.spur-sim 2>/dev/null`) so you don't pick a UDID
  another session has already claimed, then write the file. Boot the
  simulator with that UDID before installing.
- **Worktree-local DerivedData.** Pass `-derivedDataPath ./.build/derived-data`
  to every `xcodebuild` invocation (and the equivalent parameter to
  XcodeBuildMCP). `.build/` is already gitignored.
- **Use XcodeBuildMCP for sim interaction.** Tap, swipe, screenshot, and
  accessibility-tree dumps go through XcodeBuildMCP — not raw simctl, not
  AppleScript, not pixel coordinates. Pass the worktree's pinned UDID and
  derived-data path explicitly to every MCP call.
- **Pin host-level services per worktree.** Two workers `supabase start`-ing
  (or any other host-level service starting) against the same default ports
  collide. Each worktree is assigned a single `SPUR_OFFSET` and every
  networked service derives its ports from that offset. Today the script
  only renders Supabase config; future networked services (Go server,
  Redis, etc.) extend the same pin and the same template-rendering pattern.
  Run `./scripts/spur-services-init.sh` once per worktree (idempotent — pass
  `--force` to regenerate). The script picks the lowest free offset by
  reading sibling `.spur-services` pins and probing for already-bound
  ports, then renders `supabase/config.toml` from
  `supabase/config.toml.template` (gitignored) with project_id set to the
  worktree directory name. The `.spur-services` pin declares `SPUR_OFFSET`,
  `SPUR_PROJECT_ID`, and `SPUR_SUPABASE_*` URLs; `source .spur-services`
  to point shells, tests, and Make targets at the worktree's instances.
- **Don't `supabase start` without running the init script first.** A
  hard-coded port collision will fail loudly; a stale `project_id` collision
  (two stacks reusing the same docker container names) will fail silently
  by reusing whichever volume Docker grabbed first. The init script
  derives `project_id` from the worktree directory name, so as long as it
  runs, container names stay unique.
- **Anon / service-role keys for tests** come from the worktree's stack via
  `supabase status -o env --workdir <worktree-root>`. The pin file
  intentionally omits them — they're shared across local Supabase CLI
  installs and easy to refetch.

## Agent skills

### Issue tracker

Issues live in GitHub Issues at `sasilver75/events`. See
[`docs/agents/issue-tracker.md`](./docs/agents/issue-tracker.md).

### Triage labels

Default mapping — canonical role names equal the label strings. See
[`docs/agents/triage-labels.md`](./docs/agents/triage-labels.md).

### Domain docs

Single-context: [`CONTEXT.md`](./CONTEXT.md) + [`docs/adr/`](./docs/adr/) at
the repo root. See [`docs/agents/domain.md`](./docs/agents/domain.md).
