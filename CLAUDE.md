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

- **Issues** → Linear, Samcorp workspace, Spur project (team key `SAM`,
  IDs look like `SAM-12`). PRs continue to live in GitHub at
  `sasilver75/events`. `/to-issues` for vertical slices, `/triage` for
  state-machine moves. **Always triage before starting work**: move an
  issue out of `Backlog` into `Ready` with the right pickup label (`AFK`
  for agent-claimable, `HITL` for human-required), or `Canceled` if
  rejected. The previous GitHub Issues tracker is frozen — existing
  issues remain there as a record; nothing new is filed against it.
- **PRDs** → `/to-prd` publishes to Linear (Spur project, Backlog state).
- **Branches** → one per Linear issue. PRs target `main`. Branch name
  convention: `sam-<N>-<slug>` (e.g. `sam-12-feedback-flow`).
- **PR titles** → append the Linear ID to the title (e.g.
  `feat: post-event feedback flow (SAM-12)`). Linear's GitHub integration
  auto-detects the SAM-id and links the PR to the issue.
- **Commits** → conventional prose; WHY in the message body.
- **Closeouts** → On every issue close, leave a comment. "Shipped per spec —
  <commit>" when the AC was met as written; a short drift list (what diverged
  from the spec and why) when not. The diff and commit messages don't capture
  decisions made under pressure — the closeout comment does.
- **Tear down at issue close.** If you started services with
  `scripts/spur-up.sh`, run `scripts/spur-down.sh` before declaring the
  issue done — orphan Supabase stacks accumulate (~13 containers each,
  with `supabase_analytics` pinning a CPU core even when idle) and starve
  the host. Skip the teardown only when the user has explicitly asked
  you to leave the stack up.

## Multi-session coordination

Two Claude Code sessions can run in parallel against different worktrees.
Without partitioning, they will clobber each other's iOS simulator state and
build artifacts. Rules:

- **Pin one simulator per worktree.** Each worktree root holds a gitignored
  `.spur-sim` file with `SPUR_SIM_UDID`, `SPUR_SIM_NAME`,
  `SPUR_DERIVED_DATA`, and `SPUR_SIM_LAT` / `SPUR_SIM_LON`. Always read it
  before any `simctl` / `xcodebuild` / XcodeBuildMCP call. **Never** target
  `booted` or `generic/platform=iOS Simulator` — they are shared.
- **Create `.spur-sim` if missing.** Run `xcrun simctl list devices available`
  to find an `iPhone 17*` UDID, cross-check sibling worktrees'
  `.spur-sim` (`cat ../*/.spur-sim 2>/dev/null`) so you don't pick a UDID
  another session has already claimed, then write the file. Default
  `SPUR_SIM_LAT=34.0522` / `SPUR_SIM_LON=-118.2437` (LA — matches the
  curated seeds) unless the worktree is testing a different region. Boot
  the simulator with that UDID before installing.
- **Pin the sim's GPS, not just its UDID.** The iOS app's browse query is
  GPS-driven (`near=<lat>,<lon>` from `CoreLocation`); the map's region
  and the "Spur — Los Angeles" header are cosmetic and don't constrain
  the query. Sims default GPS to Apple Park (SF Bay) and persist that
  across reboots, so seeded LA fixtures vanish from browse and you waste
  cycles debugging "no events nearby". The iOS app caches its first
  CoreLocation fix at launch, so the GPS must be in place **before** the
  app starts — setting it afterwards requires killing and relaunching
  the app. Run `scripts/spur-sim-set-location.sh` (boots the sim if
  needed, applies the `.spur-sim` pin) BEFORE every `build_run_sim` /
  `launch_app_sim`. Idempotent.
- **Worktree-local DerivedData.** Pass `-derivedDataPath ./.build/derived-data`
  to every `xcodebuild` invocation (and the equivalent parameter to
  XcodeBuildMCP). `.build/` is already gitignored.
- **Use XcodeBuildMCP for sim interaction.** Tap, swipe, screenshot, and
  accessibility-tree dumps go through XcodeBuildMCP — not raw simctl, not
  AppleScript, not pixel coordinates. Pass the worktree's pinned UDID and
  derived-data path explicitly to every MCP call. Note: SwiftUI toolbar
  buttons inside `ToolbarItem(placement: .confirmationAction)` and
  `.cancellationAction` don't propagate `accessibilityIdentifier` to the
  AX tree — tap by `--label` (e.g. "Create", "Cancel") or by coordinates
  derived from the navbar's `AXFrame`. Identifier-based targeting only
  works for buttons in the view body itself.
- **Pin host-level services per worktree.** Two workers `supabase start`-ing
  (or any other host-level service starting) against the same default ports
  collide. Each worktree is assigned a single `SPUR_OFFSET` and every
  networked service derives its ports from that offset. The script renders
  Supabase config, the Go server's listen port, and the iOS-build override;
  future networked services (Redis, etc.) extend the same pin and the same
  template-rendering pattern. Run `./scripts/spur-services-init.sh` once
  per worktree (idempotent — pass `--force` to regenerate; an existing
  `SPUR_OFFSET` is preserved across regens). The script picks the lowest
  free offset by reading sibling `.spur-services` pins and probing for
  already-bound ports (Supabase API/DB and the Go server's `8080+offset`),
  then renders `supabase/config.toml` from `supabase/config.toml.template`
  (gitignored) with project_id set to the worktree directory name, and
  renders `ios/Spur/Local.generated.xcconfig` (gitignored) from its template
  so the iOS build's `SUPABASE_URL` and `SERVER_URL` point at the worktree's
  Supabase API port and Go server port respectively. The `.spur-services`
  pin declares `SPUR_OFFSET`, `SPUR_PROJECT_ID`, `SPUR_SUPABASE_*` URLs,
  `SPUR_SERVER_PORT`, and `SPUR_SERVER_URL`; `source .spur-services` to
  point shells, tests, and Make targets at the worktree's instances.
  **Never edit `ios/Spur/Local.xcconfig` to change `SUPABASE_URL` or
  `SERVER_URL`** — re-run the init script instead; the committed file
  `#include`s the rendered override.
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
- **Use `scripts/spur-up.sh` to boot a worktree's stack.** It handles the
  full sequence in the right order: detect orphan Supabase stacks (deleted
  worktrees whose containers are still running) and offer to clean them →
  run `spur-services-init.sh` if missing → `supabase start` → migrate-up →
  seed → start the Go server in the background. Idempotent. After it
  finishes, the iOS app can build_run_sim against the worktree's stack.
  `scripts/spur-down.sh` is the inverse.
- **Always tear down before deleting a worktree.** Orphan Supabase stacks
  silently starve Postgres in the *active* worktree — symptoms look like
  inexplicable per-query latency (sub-ms queries balloon to seconds,
  intermittently). Each idle stack is ~13 containers; `supabase_analytics_*`
  (Logflare) in particular pegs a CPU core. Before deleting a worktree,
  run `scripts/spur-down.sh`. If a worktree is already gone, the next
  `scripts/spur-up.sh` in any other worktree will detect the orphan and
  prompt to clean it up.

## Agent skills

### Issue tracker

Issues live in Linear (Samcorp workspace, Spur project, team key `SAM`).
PRs live in GitHub. See
[`docs/agents/issue-tracker.md`](./docs/agents/issue-tracker.md).

### Triage labels

Canonical role names map to a combination of Linear workflow state and
label (e.g. `ready-for-agent` → state `Ready` + label `AFK`). See
[`docs/agents/triage-labels.md`](./docs/agents/triage-labels.md).

### Domain docs

Single-context: [`CONTEXT.md`](./CONTEXT.md) + [`docs/adr/`](./docs/adr/) at
the repo root. See [`docs/agents/domain.md`](./docs/agents/domain.md).

### Harness (VM isolation for unattended agents)

Tickets labeled `AFK` in Linear's `Ready` state are picked up by
`scripts/spur-agent <SAM-id>`, which spawns a Tart macOS VM, drops the
agent into it with `--dangerously-skip-permissions`, and reaps the VM
when work is done. The multi-session coordination rules above (`.spur-sim`,
`.spur-services`, port offsets) don't apply inside the VM — there's only
one of each. See [`docs/agents/harness.md`](./docs/agents/harness.md) for
the architecture and credentials model.
