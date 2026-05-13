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
    ├── orchestrator/           Spec §7-8: poll loop, state machine,
    │                           dispatch, retry, reconciliation.
    └── obs/                    Structured logging (spec §13).
```

## Quick commands

```sh
go build ./...
go test ./...
go run ./cmd/spur-orchestrator --validate --workflow ../WORKFLOW.md
```

## Status

Implementation is in progress, tracked under the agent harness task series. Currently complete: module bootstrap, domain types, WORKFLOW.md loader, typed ServiceConfig, Liquid prompt renderer. Next: Linear tracker adapter, Tart Workspace Manager, Claude Code Agent Runner, orchestrator state machine.
