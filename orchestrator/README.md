# spur-orchestrator

Go implementation of [OpenAI's Symphony spec](https://github.com/openai/symphony/blob/main/SPEC.md) adapted for Spur's environment.

For the architecture, design rationale, and Spur-specific adaptations, see [`docs/agents/harness.md`](../docs/agents/harness.md) at the repo root. The agent prompt + runtime config the orchestrator consumes lives at [`WORKFLOW.md`](../WORKFLOW.md) at the repo root.

## Module layout

```
orchestrator/
├── cmd/spur-orchestrator/      Binary entry point.
└── internal/
    ├── domain/                 Symphony §4 normalized data model — Issue,
    │                           Workspace, RunAttempt, LiveSession,
    │                           RetryEntry, OrchestratorRuntimeState.
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
```

## Status

The first usable harness pass is implemented and landed in the `0.1.0`
agent-harness release. The current implementation includes:

- `WORKFLOW.md` parsing, typed runtime config, validation, and prompt rendering.
- A Linear tracker reader for candidate fetch, running-issue state refresh, and
  terminal-state lookup support.
- Spur-specific eligibility filtering: `Ready`/`In Progress`, `AFK` present,
  `HITL` absent, blockers `Done`.
- Tart VM workspace creation/reuse keyed by Linear issue identifier.
- Host-side lifecycle hooks from `WORKFLOW.md`, including terminal
  `before_remove` cleanup.
- Claude Code headless runner over SSH into the VM.
- Polling orchestrator state machine with dispatch, running-issue state refresh,
  retry bookkeeping, cancellation, continuation resume, and `agent.max_turns`
  enforcement.

This is **Symphony-like**, not a drop-in reference implementation. The largest
intentional differences are documented in
[`docs/agents/harness.md`](../docs/agents/harness.md#spur-vs-symphony).
