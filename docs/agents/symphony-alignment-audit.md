# Symphony Alignment Audit

Date: 2026-05-14

This audit maps the current Spur harness against OpenAI's Symphony draft spec.
The repo is Symphony-like rather than a drop-in reference implementation
because Spur uses Tart macOS VMs for iOS/Xcode work.

## Implemented

| Requirement | Evidence | Status |
| --- | --- | --- |
| Repo-owned workflow contract | [`WORKFLOW.md`](../../WORKFLOW.md) contains tracker, polling, workspace, hooks, Codex, credentials, and prompt config. | Implemented |
| Linear tracker control plane | [`tracker/linear`](../../orchestrator/internal/tracker/linear) fetches candidates, refreshes running states, fetches terminal issues, exposes `linear_graphql`, and performs narrow `Needs Human` escalation. | Implemented |
| Per-issue isolation | [`workspace/tart`](../../orchestrator/internal/workspace/tart) creates, reuses, boots, stops, and deletes Tart VMs keyed by issue identifier. | Implemented |
| Lifecycle hooks | `WORKFLOW.md` defines `after_create`, `before_run`, `after_run`, and `before_remove`; [`worker.go`](../../orchestrator/internal/orchestrator/worker.go) invokes them. | Implemented |
| Codex production runner | [`agent/codex`](../../orchestrator/internal/agent/codex) initializes app-server, starts/resumes threads, advertises dynamic tools, starts turns, normalizes completion/errors, handles dynamic tool calls, and captures token/rate-limit telemetry. | Implemented |
| Host-held Linear credential path | `credentials.linear_access: host_proxy` is the production default; worker omits `LINEAR_API_KEY`, passes empty `SPUR_LINEAR_TOKEN` to hooks, and advertises host-side `linear_graphql`. | Implemented |
| Codex smoke check | `go run ./cmd/spur-orchestrator --codex-smoke --workflow ../WORKFLOW.md` verifies app-server startup, initialize, and `thread/start` with `linear_graphql`. | Implemented |
| One-shot and daemon dispatch | `RunOnce` and `RunDaemon` share the same eligibility gate and worker path. | Implemented |
| Dynamic workflow reload | `Orchestrator.EnableWorkflowReload` and daemon ticks reload valid `WORKFLOW.md` prompt/config for future runs. | Implemented |
| Operator status | Optional JSON snapshots include runner, credential mode, running/retrying/needs-human work, recent runs, session/thread/turn IDs, token totals, and Codex rate-limit telemetry. | Implemented |
| Successful-loop guard | Repeated successful active turns move the issue into `needs_human` status and trigger Linear escalation. | Implemented |

## Verification Commands

```sh
cd orchestrator
go test ./...
go test -race ./internal/agent/codex ./internal/orchestrator ./internal/tracker/linear ./internal/workflow ./cmd/spur-orchestrator
go run ./cmd/spur-orchestrator --validate --workflow ../WORKFLOW.md
go run ./cmd/spur-orchestrator --codex-smoke --workflow ../WORKFLOW.md
go run ./cmd/spur-orchestrator --once --preflight --workflow ../WORKFLOW.md
go run ./cmd/spur-orchestrator --once --issue SAM-54 --preflight --workflow ../WORKFLOW.md
go run ./cmd/spur-orchestrator --once --issue SAM-54 --workflow ../WORKFLOW.md --status-file /tmp/spur-orchestrator/SAM-54-codex.json
go run ./cmd/spur-orchestrator --codex-canary-verify-status --issue SAM-54 --workflow ../WORKFLOW.md --status-file /tmp/spur-orchestrator/SAM-54-codex.json
```

The `--codex-canary-*` flag names remain as compatibility aliases for old
scripts, but Codex is now the production runner.

## Prior Real-Issue Evidence

- Linear issue: [SAM-54](https://linear.app/samcorp/issue/SAM-54/codex-canary-add-harness-smoke-note)
- GitHub PR: [#102](https://github.com/sasilver75/events/pull/102)
- Status proof: `/tmp/spur-orchestrator/SAM-54-codex.json`
- Result: `status=succeeded`, `runner=codex`, `linear_access=host_proxy`
- Linear handoff: pickup comment, closeout comment, and `In Review`
- GitHub handoff: PR #102 open against `main`

## Remaining Gaps

1. Implement orchestrator-level stall detection based on last Codex event timestamp.
2. Parse/pass through Codex approval and sandbox config instead of hard-coding the current high-trust policy.
3. Add a dashboard only if JSON snapshots are not enough for operations.
4. Rotate the Linear and GitHub keys used during the SAM-54 proof run if they have not already been rotated.
