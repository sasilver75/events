# spur-orchestrator

Go implementation of [OpenAI's Symphony spec](https://github.com/openai/symphony/blob/main/SPEC.md) adapted for Spur's Tart VM environment.

The repo-owned workflow contract lives at [`WORKFLOW.md`](../WORKFLOW.md). The
architecture and Spur-specific adaptations are documented in
[`docs/agents/harness.md`](../docs/agents/harness.md).

## Module Layout

```
orchestrator/
├── cmd/spur-orchestrator/      Binary entry point.
└── internal/
    ├── domain/                 Normalized issue, run, workspace, retry,
    │                           and orchestrator state models.
    ├── agent/                  Shared runner contract plus Codex app-server.
    ├── workflow/               WORKFLOW.md loader, typed config, renderer.
    ├── tracker/linear/         Linear GraphQL adapter and dynamic tool.
    ├── workspace/tart/         Tart VM workspace manager.
    └── orchestrator/           Poll loop, dispatch, retry, reconciliation.
```

## Quick Commands

```sh
go build ./...
go test ./...
go run ./cmd/spur-orchestrator --validate --workflow ../WORKFLOW.md
go run ./cmd/spur-orchestrator --codex-smoke --workflow ../WORKFLOW.md
go run ./cmd/spur-orchestrator --once --preflight --workflow ../WORKFLOW.md
go run ./cmd/spur-orchestrator --once --issue SAM-12 --preflight --workflow ../WORKFLOW.md
go run ./cmd/spur-orchestrator --once --issue SAM-12 --workflow ../WORKFLOW.md --status-file /tmp/spur-orchestrator/SAM-12-codex.json
go run ./cmd/spur-orchestrator --once --issue SAM-12 --review-pr 123 --workflow ../WORKFLOW.md --status-file /tmp/spur-orchestrator/SAM-12-review.json
go run ./cmd/spur-orchestrator --codex-canary-verify-status --issue SAM-12 --workflow ../WORKFLOW.md --status-file /tmp/spur-orchestrator/SAM-12-codex.json
go run ./cmd/spur-orchestrator --workflow ../WORKFLOW.md --status-file /tmp/spur-orchestrator/status.json
go run ./cmd/spur-dashboard --status-dir /tmp/spur-orchestrator
go run ./cmd/spur-dashboard --status-file /tmp/spur-orchestrator/status.json
```

From the repo root:

```sh
scripts/spur-snapshot-codex.sh   # optional if the VM does not already have Codex auth
scripts/spur-agent SAM-12
scripts/spur-dashboard
```

`spur-dashboard` is a local, read-only operator UI. By default it aggregates
every JSON snapshot in `/tmp/spur-orchestrator`, which is useful when dogfooding
supervised one-shot runs. Pass `--status-file` to follow one daemon
orchestrator snapshot exactly.

## Status

The orchestrator now uses Codex as the production runner:

- `WORKFLOW.md` defaults to `agent.runner: codex`.
- `credentials.linear_access: host_proxy` keeps the Linear key on the host.
- The worker advertises a host-side `linear_graphql` dynamic tool to Codex.
- The legacy non-Codex runner, snapshot script, and project hook have been removed.

Implemented:

- Workflow parsing, typed config, validation, prompt rendering, and dynamic reload.
- Linear candidate fetch, active-run refresh, terminal cleanup lookup, and narrow `Needs Human` escalation.
- Spur eligibility policy: `Ready`/`In Progress`, `AFK` present, `HITL` absent, blockers `Done`.
- Tart VM workspace creation/reuse keyed by Linear issue identifier.
- Host lifecycle hooks from `WORKFLOW.md`.
- Codex app-server initialize, thread start/resume, turn start, completion/error handling, dynamic tools, token telemetry, and rate-limit telemetry.
- Codex smoke check and issue preflight.
- Reviewer-agent one-shot mode for harness-created PRs, with structured review prompts, optional single implementer response turn, and Needs Human escalation for failed or ambiguous review states.
- Polling state machine with bounded dispatch, retries, cancellation, continuation resume, and `agent.max_turns`.
- Optional JSON status snapshots for daemon and one-shot runs.
- Local read-only status dashboard backed by the JSON status snapshot.

Still intentionally lightweight:

- The dashboard is operator visibility only; issue comments, PRs, and Linear state transitions remain agent-owned.
- Scheduler state is in memory; restart recovery is tracker/filesystem-driven.
- Normal Linear comments/state transitions remain agent-owned per `WORKFLOW.md`.
