# Symphony Alignment Audit

Date: 2026-05-14

This audit maps the current Spur harness against the Symphony-inspired goals in
[`harness.md`](./harness.md). It is an evidence checklist, not a claim that the
repo is a drop-in Symphony implementation.

## Objective

Move the repository closer to Symphony where it makes sense for Spur's iOS
development environment:

- Keep Linear as the tracker/control plane.
- Keep per-issue isolation, but use Tart macOS VMs because Spur needs Xcode,
  iOS Simulator, codesign, Supabase, Docker/Colima, and Go.
- Add Codex app-server support alongside Claude Code, not as an unproven
  replacement.
- Support live `WORKFLOW.md` reload.
- Harden continuation loops and prompt bookkeeping.
- Improve operator status/telemetry.
- Reduce Linear secret exposure where Codex app-server makes that practical.

## Checklist

| Requirement | Evidence | Status |
| --- | --- | --- |
| Repo-owned workflow contract | [`WORKFLOW.md`](../../WORKFLOW.md) contains tracker, polling, workspace, hooks, agent, credentials, and runner config plus the per-issue prompt. | Implemented |
| Linear tracker control plane | [`tracker/linear`](../../orchestrator/internal/tracker/linear) fetches candidates, state refreshes, terminal issues, and narrow escalation writes. | Implemented |
| Per-issue isolation | [`workspace/tart`](../../orchestrator/internal/workspace/tart) creates, reuses, boots, stops, and deletes Tart VMs keyed by issue identifier. | Implemented |
| Lifecycle hooks | `WORKFLOW.md` defines `after_create`, `before_run`, `after_run`, and `before_remove`; [`worker.go`](../../orchestrator/internal/orchestrator/worker.go) invokes them. | Implemented |
| Claude Code current production runner | [`agent/claudecode`](../../orchestrator/internal/agent/claudecode) still exists behind the shared runner contract and remains `agent.runner: claudecode` in `WORKFLOW.md`. | Implemented |
| Shared runner contract | [`agent.go`](../../orchestrator/internal/agent/agent.go) defines normalized events, usage/rate-limit telemetry, dynamic tools, runner config, and runner interfaces. | Implemented |
| Codex app-server runner | [`agent/codex`](../../orchestrator/internal/agent/codex) initializes app-server, starts/resumes threads, advertises host dynamic tools on start and resume, starts turns, normalizes completion/errors, rejects unsupported app-server requests, handles dynamic tool calls, and captures token/rate-limit telemetry. SAM-54 proved this path through a Tart-backed issue run. | Implemented and canary-proven |
| Codex smoke check | `go run ./cmd/spur-orchestrator --codex-smoke --workflow ../WORKFLOW.md` starts real local `codex app-server`, initializes it, and starts an ephemeral thread with `linear_graphql` advertised. | Verified locally |
| Codex canary preflight | `scripts/spur-codex-canary preflight SAM-54` passed with `runner=codex`, `linear_access=host_proxy`, a real Codex app-server user agent, `codex_home`, platform, and thread ID. | Verified with SAM-54 |
| Codex canary path | `scripts/spur-codex-canary run SAM-54` completed a Tart-backed one-shot run, forced `agent.runner: codex` plus `credentials.linear_access: host_proxy`, and wrote `/tmp/spur-orchestrator/SAM-54-codex.json`. | Verified with SAM-54 |
| Codex canary status verifier | `scripts/spur-codex-canary verify SAM-54` validated the status proof: `status=succeeded`, `session_id=019e24ab-880f-71b2-b3ae-cbc4577d08e3`, `thread_id=019e24ab-880f-71b2-b3ae-cbc4577d08e3`, `turn_id=019e24ab-8851-74b3-b246-2e7b2b170b1b`, token telemetry, `runner=codex`, and `linear_access=host_proxy`. | Verified with SAM-54 |
| Codex canary evidence checklist | `scripts/spur-codex-canary checklist SAM-54` prints the concrete post-run evidence to inspect across logs, Linear comments/state, GitHub PR/CI, telemetry, secret handling, and the production-switch decision. | Verified with SAM-54 |
| Codex canary wrapper | [`scripts/spur-codex-canary`](../../scripts/spur-codex-canary) wraps doctor, smoke, discovery preflight, issue preflight, run, status verification, and checklist commands with one default status-file path. | Implemented |
| Optional Codex identity snapshot | [`scripts/spur-snapshot-codex.sh`](../../scripts/spur-snapshot-codex.sh) can create a filtered `~/.codex` snapshot for Codex canaries; `before_run` ships `SPUR_HARNESS_CODEX_DIR` into the VM as `~/.codex/` when configured. | Implemented, optional |
| Dynamic workflow reload | [`Orchestrator.EnableWorkflowReload`](../../orchestrator/internal/orchestrator/orchestrator.go) and daemon ticks reload changed `WORKFLOW.md` prompt/config for the already-selected runner; covered by `TestReloadWorkflowIfChangedUpdatesSchedulerAndWorker`. Runner-class changes are skipped and logged, covered by `TestReloadWorkflowIfChangedSkipsRunnerClassChange`, because the concrete runner instance is built at startup. | Implemented with guarded runner switches |
| One-shot eligibility guard | `RunOnce` now selects explicit `--issue` targets from the eligible set and returns the rejection reason instead of dispatching rejected candidates; covered by `TestRunOnce_ExplicitIssueMustBeEligible`. | Implemented |
| Prompt bookkeeping hardening | `WORKFLOW.md` includes a required handoff checklist and re-check before exit, covering pickup, PR, closeout comment, and `In Review`. | Implemented |
| Stuck-loop detection | `agent.max_unproductive_successes` is parsed and enforced; repeated successful active turns move the issue to `needs_human` status and trigger Linear escalation. | Implemented |
| Operator status | [`status.go`](../../orchestrator/internal/orchestrator/status.go) writes optional JSON snapshots containing runner/credential mode, running/retrying/needs-human work, recent run results with session IDs, per-run token counts, and per-run rate-limit telemetry, workflow reload metadata, aggregate token totals, runtime totals, and latest Codex rate limits; `RunOnce` also writes this snapshot when `--status-file` is set and treats write failure as a run error. | Implemented |
| Codex telemetry | Shared `Usage` and `RateLimitSnapshot` flow from Codex events through worker results to orchestrator state/status. | Implemented |
| Host-held Linear credential path | `credentials.linear_access: host_proxy` is valid only with `agent.runner: codex`; worker omits `LINEAR_API_KEY`, passes an empty `SPUR_LINEAR_TOKEN` to hooks, and advertises host-side `linear_graphql`. | Implemented for Codex |
| Real Codex issue dispatch | SAM-54 completed a Tart-backed Codex run from Ready to In Review. Linear has pickup and closeout comments, and the closeout links GitHub PR #102. | Verified with SAM-54 |
| Production secret isolation | Production `WORKFLOW.md` still uses `agent.runner: claudecode` and `credentials.linear_access: vm_env`. The Codex `host_proxy` path is now canary-proven, so the remaining work is an operator decision on when to switch defaults. | Decision pending |
| Dedicated dashboard | No interactive dashboard exists; current visibility is structured logs, artifacts, and JSON status snapshots. | Not implemented; optional |

## Verification Commands

Last known verification set:

```sh
cd orchestrator
go test ./...
go test -race ./internal/agent/codex ./internal/orchestrator ./internal/tracker/linear ./internal/workflow ./cmd/spur-orchestrator
go run ./cmd/spur-orchestrator --codex-smoke --workflow ../WORKFLOW.md
go run ./cmd/spur-orchestrator --once --codex-canary --preflight --workflow ../WORKFLOW.md
scripts/spur-codex-canary preflight SAM-54
scripts/spur-codex-canary run SAM-54
scripts/spur-codex-canary verify SAM-54
scripts/spur-codex-canary checklist SAM-54
../scripts/spur-codex-canary --help
../scripts/spur-codex-canary doctor
../scripts/spur-snapshot-codex.sh
```

The smoke check returned a real local Codex app-server user agent,
`codex_home`, platform, and ephemeral thread ID. It verifies local app-server
startup and the `thread/start` dynamic-tool surface.

The SAM-54 canary verifies model turn execution inside a Tart VM, host-held
Linear GraphQL access through the Codex dynamic tool, GitHub branch/PR creation,
Linear pickup and closeout comments, the In Review state transition, and the
machine-readable status artifact.

## SAM-54 Canary Evidence

- Linear issue: [SAM-54](https://linear.app/samcorp/issue/SAM-54/codex-canary-add-harness-smoke-note)
- GitHub PR: [#102](https://github.com/sasilver75/events/pull/102)
- Status proof: `/tmp/spur-orchestrator/SAM-54-codex.json`
- Run result: `status=succeeded`, `runner=codex`, `linear_access=host_proxy`.
- Codex provenance: `session_id=019e24ab-880f-71b2-b3ae-cbc4577d08e3`, `thread_id=019e24ab-880f-71b2-b3ae-cbc4577d08e3`, `turn_id=019e24ab-8851-74b3-b246-2e7b2b170b1b`.
- Linear handoff: pickup comment exists, closeout comment exists, issue state is `In Review`.
- GitHub handoff: PR #102 is open against `main`, mergeable, and changes only `docs/agents/codex-canary-smoke.md`.

## Remaining Gaps

1. Decide whether one successful SAM-54 canary is enough to move production
   defaults from `claudecode/vm_env` to `codex/host_proxy`, or whether to run a
   second canary against a real implementation ticket first.
2. Merge or close PR #102 after reviewing the canary artifact.
3. Rotate the Linear and GitHub keys used during the canary because they were
   handled during live setup, then clean old `/tmp/spur-orchestrator` run logs
   that predate the Codex `host_proxy` path.
4. Add a dashboard only if JSON snapshots are not enough for operations.
