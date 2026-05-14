# spur-orchestrator

Go implementation of [OpenAI's Symphony spec](https://github.com/openai/symphony/blob/main/SPEC.md) adapted for Spur's environment.

For the architecture, design rationale, and Spur-specific adaptations, see [`docs/agents/harness.md`](../docs/agents/harness.md) at the repo root. The agent prompt + runtime config the orchestrator consumes lives at [`WORKFLOW.md`](../WORKFLOW.md) at the repo root.
The current Symphony alignment evidence checklist is
[`docs/agents/symphony-alignment-audit.md`](../docs/agents/symphony-alignment-audit.md).

## Module layout

```
orchestrator/
├── cmd/spur-orchestrator/      Binary entry point.
└── internal/
    ├── domain/                 Symphony §4 normalized data model — Issue,
    │                           Workspace, RunAttempt, LiveSession,
    │                           RetryEntry, OrchestratorRuntimeState.
    ├── agent/                  Shared runner contract plus concrete
    │                           claudecode and codex integrations.
    ├── workflow/               Spec §5: WORKFLOW.md loader + ServiceConfig
    │                           typed view + Liquid prompt renderer.
    ├── tracker/linear/         Spec §11: Linear GraphQL adapter.
    ├── workspace/tart/         Spec §9 adapted: Tart VM lifecycle as
    │                           Workspace Manager.
    ├── agent/claudecode/       Spec §10 adapted: Claude Code headless mode
    │                           wrapped to match the spec's agent contract.
    └── orchestrator/           Spec §7-8: poll loop, state machine,
                                dispatch, retry, reconciliation.
```

## Quick commands

```sh
go build ./...
go test ./...
go run ./cmd/spur-orchestrator --validate --workflow ../WORKFLOW.md
go run ./cmd/spur-orchestrator --codex-smoke --workflow ../WORKFLOW.md
go run ./cmd/spur-orchestrator --once --codex-canary --preflight --workflow ../WORKFLOW.md
go run ./cmd/spur-orchestrator --once --issue SAM-12 --codex-canary --preflight --workflow ../WORKFLOW.md
go run ./cmd/spur-orchestrator --once --issue SAM-12 --codex-canary --workflow ../WORKFLOW.md --status-file /tmp/spur-orchestrator/SAM-12-codex.json
go run ./cmd/spur-orchestrator --codex-canary-verify-status --issue SAM-12 --workflow ../WORKFLOW.md --status-file /tmp/spur-orchestrator/SAM-12-codex.json
go run ./cmd/spur-orchestrator --codex-canary-checklist --issue SAM-12 --workflow ../WORKFLOW.md
go run ./cmd/spur-orchestrator --workflow ../WORKFLOW.md --status-file /tmp/spur-orchestrator/status.json
```

From the repo root, `scripts/spur-codex-canary` wraps the canary sequence with
the same defaults. `scripts/spur-snapshot-codex.sh` is optional, but useful when
the per-issue VM does not already have Codex app-server auth:

```sh
scripts/spur-snapshot-codex.sh
scripts/spur-codex-canary doctor
scripts/spur-codex-canary smoke
scripts/spur-codex-canary discover
scripts/spur-codex-canary preflight SAM-12
scripts/spur-codex-canary run SAM-12
scripts/spur-codex-canary verify SAM-12
scripts/spur-codex-canary checklist SAM-12
```

## Status

The first usable harness pass is implemented and landed in the `0.1.0`
agent-harness release. The current implementation includes:

- `WORKFLOW.md` parsing, typed runtime config, validation, and prompt rendering.
- Dynamic `WORKFLOW.md` reload for prompt/config changes that do not switch the
  concrete runner class; changing between Claude Code and Codex requires a
  restart or one-shot canary.
- A Linear tracker reader for candidate fetch, running-issue state refresh, and
  terminal-state lookup support.
- Spur-specific eligibility filtering: `Ready`/`In Progress`, `AFK` present,
  `HITL` absent, blockers `Done`.
- Tart VM workspace creation/reuse keyed by Linear issue identifier.
- Host-side lifecycle hooks from `WORKFLOW.md`, including terminal
  `before_remove` cleanup.
- Claude Code headless runner over SSH into the VM.
- Shared agent runner interface, with Claude Code as the default concrete
  runner and a Codex package/config path that speaks the basic
  `codex app-server` stdio protocol.
- Codex app-server smoke check via `--codex-smoke`, which verifies local
  app-server startup, initialize handshake, and `thread/start` with the
  `linear_graphql` dynamic tool advertised before any VM/issue dispatch.
- Codex issue canary mode via `--once --issue SAM-12 --codex-canary`, which
  forces `agent.runner: codex` and `credentials.linear_access: host_proxy` for
  one targeted run without changing the production `WORKFLOW.md` defaults.
- Canary preflight via `--preflight`, which checks host credentials, tracker
  access, GitHub token presence, target issue eligibility, and the Codex
  app-server handshake without creating a VM, running hooks, or launching an
  agent turn. If no `--issue` is provided, it prints eligible and rejected
  active candidates so an operator can pick a low-risk canary target. Local
  readiness checks report all missing required inputs before attempting tracker
  access.
- Post-run Codex canary checklist via `--codex-canary-checklist --issue SAM-12`,
  which prints the concrete Linear/GitHub/log evidence to inspect before
  deciding whether Codex should become the production runner.
- Post-run Codex canary status verifier via
  `--codex-canary-verify-status --issue SAM-12 --status-file ...`, which checks
  the JSON proof file for `codex`, `host_proxy`, a succeeded recent run,
  session ID, thread ID, token telemetry, and aggregate Codex totals without
  requiring Linear/GitHub credentials.
- Polling orchestrator state machine with dispatch, running-issue state refresh,
  retry bookkeeping, cancellation, continuation resume, and `agent.max_turns`
  enforcement.
- One-shot issue dispatch uses the same eligibility gate as daemon dispatch, so
  `--once --issue` and Codex canaries cannot bypass `AFK`/`HITL`/blocker
  policy.
- One-shot status snapshots: `--status-file` also works with `--once`, so a
  Codex canary can leave a machine-readable JSON record of token totals,
  latest rate-limit telemetry, completion counts, and recent run status. In
  `--once` mode, a requested status snapshot write failure makes the run fail
  so canary evidence cannot silently disappear. Snapshots include
  `agent_runner` and `linear_access` so the proof artifact records which runner
  and credential boundary produced it, and `recent_runs[].session_id`,
  `recent_runs[].thread_id`, and `recent_runs[].turn_id` so the artifact
  records the Codex resume handle plus thread/turn provenance. Each
  `recent_runs[]` entry also carries per-run token counts and per-run
  rate-limit telemetry when Codex emits it.
- Optional JSON status snapshots via `--status-file`, covering running issues,
  retry queue, issues needing human review, claimed/completed counts, workflow
  reload metadata, completed-turn token totals, runtime totals, and latest
  Codex rate-limit telemetry when available.
- Successful-continuation loop guard: after repeated successful turns that
  leave an issue active, the orchestrator keeps the claim and records a
  human-review status entry instead of burning all remaining turns.
- Narrow Linear escalation for that guard: post an escalation comment and move
  the issue to `Needs Human`.
- Explicit credential boundary config: `credentials.linear_access: vm_env` is
  the production default, while `host_proxy` is available for
  `agent.runner: codex` through a host-side `linear_graphql` dynamic tool.

This is **Symphony-like**, not a drop-in reference implementation. The largest
intentional differences are documented in
[`docs/agents/harness.md`](../docs/agents/harness.md#spur-vs-symphony).
