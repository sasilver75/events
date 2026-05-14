# Agent harness

How agents safely run Spur work end-to-end without supervision.

The harness is a **Symphony-inspired** implementation adapted for Spur's
environment, not a drop-in copy of OpenAI's reference shape.
[Symphony](https://github.com/openai/symphony/blob/main/SPEC.md) defines a
long-running automation service that reads work from Linear, creates an
isolated workspace for each issue, and runs a coding-agent session inside that
workspace. The spec also draws an important boundary: Symphony is a
scheduler/runner and tracker reader; ticket writes are normally performed by
the agent according to the repo-owned `WORKFLOW.md`.

The two artifacts are:

- **[`WORKFLOW.md`](../../WORKFLOW.md)** at the repo root — the per-issue prompt template + runtime config. Workflow-author concern.
- **`orchestrator/`** — Go implementation of the scheduler/runner. Orchestrator-author concern.

The two are kept apart deliberately. WORKFLOW.md is policy ("what the agent does for an issue"); `orchestrator/` is mechanism ("how issues get from Linear to a running agent").

## Spur vs Symphony

| Symphony/reference shape | Spur harness today |
| --- | --- |
| Workspace = filesystem directory under a workspace root. | Workspace = **Tart VM clone** of `spur-base`. Per-issue VM named `spur-ticket-<sanitized-issue-id>` (e.g. `spur-ticket-SAM-12`). |
| Hooks run with `cwd = workspace_path`. | Hooks run on the host with `SPUR_VM_NAME`, `SPUR_VM_IP`, `SPUR_ISSUE_ID`, and credentials in env. Hooks use `tart`, `ssh`, and `scp` to affect the VM. |
| Agent Runner launches `codex app-server` and speaks the Codex app-server protocol. | Agent lifecycle code now uses a shared runner contract. The default concrete runner is **Claude Code headless** over SSH; the Codex runner has a minimal app-server stdio client, but remains experimental until exercised on real issues. |
| `WORKFLOW.md` front matter has a `codex` section. | Spur defaults to `agent.runner: claudecode` and a `claudecode` extension section. The typed config also parses Symphony's `codex` section for the future Codex runner. The rest of the file follows the Symphony front-matter/prompt split. |
| `max_concurrent_agents` default is 10. | Capped at **2** by Apple's macOS-guest license. Start at 1 for early operator runs. |
| `active_states` default to `Todo` / `In Progress`. | Pickup states are **`Ready` / `In Progress`**. Backlog/Todo are not pickup-eligible. |
| Candidate eligibility is primarily active-state plus not running/claimed. | Same base idea plus Spur policy: label `AFK` present, label `HITL` absent, all blockers in `Done`, and issue is in the Spur project. |
| Workspace cleanup on terminal state removes filesystem workspaces. | Terminal-state cleanup stops and deletes the Tart VM, after running `before_remove` to collect final artifacts. |
| Dynamic `WORKFLOW.md` reload is part of the full spec shape. | Daemon ticks reload `WORKFLOW.md` when its mtime changes, refreshing prompt text and typed config without restarting the process. |
| Status surface is optional. | No dashboard yet. Operator visibility is structured logs, per-run copied artifacts, and an optional JSON status snapshot. |

The practical alignment is strong at the architectural layer: repo-owned
workflow policy, Linear as control plane, bounded dispatch, isolated per-issue
workspaces, hooks, retries/reconciliation, and agent-owned handoff comments/PRs.
The implementation deliberately diverges at the execution layer because Spur
needs Xcode, iOS Simulator, Docker/Supabase, and macOS tooling inside each
workspace.

For a point-in-time evidence checklist, see
[`symphony-alignment-audit.md`](./symphony-alignment-audit.md).

## Implementation status

Implemented in the current Go orchestrator:

- `WORKFLOW.md` loader: YAML front matter + Markdown prompt body.
- Typed config layer for tracker, polling, workspace, hooks, agent, and
  `claudecode`.
- Liquid prompt rendering with the normalized issue model.
- Linear tracker reader: candidate fetch, running-issue state refresh, and
  terminal-state lookup.
- Eligibility filter for `AFK`/`HITL` labels and blocked-by state.
- Tart workspace manager: clone-or-reuse, boot, SSH readiness, stop/delete.
- Host hook runner for `after_create`, `before_run`, `after_run`, and
  `before_remove`.
- Shared agent runner contract under `internal/agent`, with Claude Code and
  Codex packages behind the same worker-facing interface.
- Claude Code stream runner over SSH.
- Codex app-server runner over SSH, including initialize, thread start/resume,
  turn start, turn completion/error normalization, unsupported request
  rejection, token-usage notification capture, and rate-limit notification
  capture. Host dynamic tools are advertised on both thread start and resume so
  continuation turns keep access to `linear_graphql`.
- Local Codex app-server smoke check via `spur-orchestrator --codex-smoke`.
  This verifies that the configured app-server command starts, answers the
  initialize request, and accepts `thread/start` with the `linear_graphql`
  dynamic tool advertised before an operator tries a real issue dispatch.
- Single-issue Codex canary mode via
  `spur-orchestrator --once --issue <SAM-N> --codex-canary`. This forces
  `agent.runner: codex` and `credentials.linear_access: host_proxy` for that
  one run without changing the production `WORKFLOW.md` defaults.
- Canary preflight via
  `spur-orchestrator --once --issue <SAM-N> --codex-canary --preflight`. This
  checks host credentials, GitHub token presence, Linear candidate fetch,
  target issue eligibility, and the Codex app-server handshake before any Tart
  VM, hook, or agent turn is started. Omitting `--issue` prints eligible and
  rejected active candidates without dispatching anything and only requires
  Linear access plus the Codex handshake. Missing local readiness inputs are
  reported together before network access is attempted.
- Post-run Codex canary checklist via
  `spur-orchestrator --codex-canary-checklist --issue <SAM-N>`, covering the
  Linear comments/state, GitHub PR evidence, logs, telemetry, and production
  switch decision.
- Post-run Codex canary status verification via
  `spur-orchestrator --codex-canary-verify-status --issue <SAM-N> --status-file ...`.
  This validates the machine-readable proof file for `agent_runner: codex`,
  `linear_access: host_proxy`, a succeeded recent run, Codex session/thread ID,
  token telemetry, and aggregate Codex totals without needing tracker
  credentials.
- Host-side Codex dynamic tool support, including a `linear_graphql` tool that
  lets `credentials.linear_access: host_proxy` keep the Linear token on the
  host for Codex runs.
- Orchestrator loop with single-authority in-memory state, bounded dispatch,
  running-issue state refresh, completion integration, and retry bookkeeping.
- One-shot dispatch (`--once --issue`) uses the same eligibility filter as
  daemon dispatch, so targeted Codex canaries cannot skip `AFK`/`HITL`/blocker
  policy.
- Active cancellation when a running issue becomes terminal or otherwise
  leaves the active state set.
- Terminal workspace cleanup on daemon startup and after terminal-state
  cancellation.
- `agent.max_turns` enforcement for retries/continuation turns.
- Continuation turns resume the prior Claude Code session when a `SessionID`
  was emitted by the previous run.
- Dynamic `WORKFLOW.md` reload on daemon ticks. Changed prompt text, hooks,
  agent limits, active/terminal state lists, and command/timeout settings for
  the already-selected runner apply to newly started runs. Runner-class changes
  (`claudecode` <-> `codex`) are deliberately skipped during live reload so
  the config cannot drift away from the concrete runner instance; use
  `--codex-canary` or restart the daemon for a runner switch.
- Optional operator JSON status snapshots via `--status-file`, including
  running issues, retry queue, issues needing human review, claim counts,
  workflow reload metadata, completed-turn token totals, runtime totals, and
  the latest Codex rate-limit snapshot. The same flag works for `--once`
  canary runs, so the one-shot proof path can leave structured telemetry and a
  `recent_runs` record with the issue identifier and final status. In `--once`
  mode, a requested status snapshot write failure makes the run fail instead of
  becoming a warning-only daemon concern. Snapshots also include `agent_runner`
  and `linear_access` so Codex canary artifacts prove their runner and
  credential mode, plus `recent_runs[].session_id` for the resumable handle,
  `recent_runs[].thread_id` and `recent_runs[].turn_id` for Codex provenance,
  per-run token counts, and per-run rate-limit telemetry for the specific
  canary attempt when Codex emits it.
- Successful-continuation loop guard. If an agent repeatedly returns success
  while the issue remains active, the orchestrator stops scheduling new turns,
  keeps the issue claimed, and records a `needs_human` status entry for the
  operator.
- Narrow host-side Linear escalation for that loop guard. The orchestrator
  posts an escalation comment and moves the issue to `Needs Human` after
  `agent.max_unproductive_successes` successful active turns.

Not implemented or intentionally incomplete:

- Production-proven Codex app-server operation. A `codex` runner package and
  `agent.runner: codex` config path exist and now speak the basic stdio
  protocol, but the path has not been exercised on real Spur issues yet.
  Unsupported app-server requests are rejected.
- A dedicated dashboard. The current status surface is structured logs plus
  the optional JSON snapshot file.
- General-purpose host-side Linear mutations. By design, normal issue state
  changes and comments are still performed by the agent per `WORKFLOW.md`; the
  orchestrator has only the narrow stuck-loop escalation writer.
- Strong secret isolation for the production runner. `credentials.linear_access:
  host_proxy` exists for `agent.runner: codex`, but Claude Code remains on
  `vm_env`, so the default production mode still injects Linear credentials
  into the VM.

## Why VMs and not Docker

Docker on macOS runs Linux containers in a hidden VM — fine for backend Go/SQL work, but Xcode, the iOS Simulator, UIKit/SwiftUI, and `codesign` are macOS-only and not redistributable inside a Linux image. Spur needs both halves. The only mechanism that gives both is macOS-in-macOS via Apple's Virtualization.framework, exposed by [Tart](https://tart.run). Inside a Tart VM the agent has a full Mac: native Xcode, simulators, Docker (via colima), Go, Supabase CLI.

Tradeoffs we accept:

- Apple Silicon host required.
- Apple's licensing permits 2 concurrent macOS guests per physical host. Hard ceiling on `max_concurrent_agents`.
- Base image is large (~80 GB Tahoe + Xcode); each per-ticket VM clone is copy-on-write.

## Lifecycle of a ticket

Driven by the orchestrator following Symphony §7 and §8. Summarized here for the reader who hasn't internalized the spec:

1. **Poll tick** (every `polling.interval_ms`). Reconcile running issues against tracker state, then fetch candidates from Linear matching `state ∈ active_states AND label = AFK AND label ≠ HITL AND blockers all Done`.
2. **Dispatch.** Sort by `(priority asc, created_at oldest first, identifier tiebreak)`. Claim issues up to `max_concurrent_agents`.
3. **Workspace prep.** `tart clone --copy-on-write spur-base spur-ticket-<id>`, `tart run`, wait for SSH. If VM already exists (continuation run), boot it.
4. **`after_create` hook** (first run only). Clone or refresh the repo inside the VM and leave it on `main`.
5. **`before_run` hook** (every run). Inject credentials according to
   `credentials.linear_access`. In `host_proxy` mode, the Linear token is not
   written into the VM. If `SPUR_HARNESS_CODEX_DIR` points at a filtered Codex
   snapshot, the hook also ships it into the VM as `~/.codex/` for Codex
   canaries.
6. **Agent Runner.** Launch the configured runner inside the VM with the
   rendered WORKFLOW.md prompt: Claude Code for the production path, or Codex
   app-server for targeted canaries. The prompt instructs the agent to create
   the issue branch and to run `scripts/spur-up.sh` only for tickets that touch
   `server/`, `supabase/`, `ios/`, or migrations. Stream events back to the
   orchestrator.
7. **`after_run` hook.** Collect logs, sync any artifacts the agent left in the VM out to the orchestrator's per-run log dir.
8. **Reconciliation.** Each subsequent tick re-fetches tracker state for running issues. If an issue reaches a terminal state, the orchestrator cancels the worker, runs `before_remove`, and deletes the Tart VM. If an issue leaves the active state set without becoming terminal, the orchestrator cancels the worker and releases the claim.

## Credentials

The VM is ephemeral; long-lived credentials live in the host's macOS Keychain and are injected per run. The base image contains no secrets. See [`credentials-setup.md`](./credentials-setup.md) for the operator checklist.

`WORKFLOW.md` currently declares `credentials.linear_access: vm_env`. In this
production mode, the orchestrator passes `LINEAR_API_KEY` into the in-VM agent
environment and writes it to `~/.config/spur/credentials.env` during
`before_run`.

`credentials.linear_access: host_proxy` is implemented only for
`agent.runner: codex`. In that mode, the worker omits `LINEAR_API_KEY` from the
agent subprocess environment, passes an empty `SPUR_LINEAR_TOKEN` to hooks so
`credentials.env` cannot contain the token, and advertises a host-side
`linear_graphql` dynamic tool to Codex app-server. Claude Code cannot use
Codex app-server dynamic tools, so
`host_proxy` with `agent.runner: claudecode` fails validation.

Codex canaries may also set `SPUR_HARNESS_CODEX_DIR` to a filtered snapshot
created by `scripts/spur-snapshot-codex.sh`. This is separate from Linear
secret isolation: it provides Codex app-server identity inside the VM while the
Linear token remains host-held.

| Credential                | Purpose                                 | Injection                                                      | Scope                                                                                                                                                                                                                              |
| ------------------------- | --------------------------------------- | -------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Anthropic / Claude Code   | Running Claude Code inside the VM       | Copy `~/.spur/claude-harness/` (a snapshot of your personal `~/.claude/`) into the VM | **Your personal Anthropic identity.** Anthropic doesn't sell a "harness only" tier, and a separate seat costs real money without buying meaningful isolation for a single-author project. |
| GitHub access             | `git clone`, `git push`, `gh pr create` | Inject a fine-grained PAT as `GITHUB_TOKEN` env var            | Fine-grained PAT scoped **only** to `sasilver75/events` with `Contents: write` + `Pull requests: write` + `Metadata: read`. 30-day expiration. GitHub's fine-grained PATs are cheap to scope, so we do. |
| Linear access             | Read ticket, post closeout              | `vm_env`: inject as `LINEAR_API_KEY`; `host_proxy`: keep on host behind `linear_graphql` | **Your personal Linear API key.** Linear's keys are workspace-scoped (no per-project keys), so a "bot" key would still have full Samcorp access — and you're the only member of Samcorp, so a bot offers no real isolation. |

**Residual risk we accept in `vm_env`:** a compromised per-issue VM could (in principle) call the Anthropic API as you, read/write your Samcorp workspace in Linear, and push to `sasilver75/events`. Mitigations: VMs are ephemeral; rotate the Linear key or GitHub PAT if anything looks suspicious; the GitHub blast radius is genuinely contained by the fine-grained PAT.

**Upgrade path:** first run
`spur-orchestrator --once --codex-canary --preflight` to list eligible
candidates. Then run
`spur-orchestrator --once --issue <SAM-N> --codex-canary --preflight`, then
prove `agent.runner: codex` on the same real issue with
`spur-orchestrator --once --issue <SAM-N> --codex-canary --status-file /tmp/spur-orchestrator/<SAM-N>-codex.json`.
Verify the status proof with
`spur-orchestrator --codex-canary-verify-status --issue <SAM-N> --status-file /tmp/spur-orchestrator/<SAM-N>-codex.json`,
then use `spur-orchestrator --codex-canary-checklist --issue <SAM-N>` to audit
the handoff before moving the production workflow from `vm_env` to `host_proxy`
so the Linear token no longer enters the VM.

## End-of-run publication contract

The publication contract lives in **`WORKFLOW.md`**, not here — Symphony §11.5 deliberately keeps tracker writes inside the agent's tool surface, not the orchestrator. The agent reads WORKFLOW.md and follows it.

At a high level, the current contract requires the agent to produce:

1. A **PR against `main`** with title `<type>: <summary> (SAM-N)`.
2. A **Linear comment** on the issue with:
    - PR link.
    - Self-assessed AC table.
    - For UI tickets: a recorded simulator video (failure-then-fix for bugs, walkthrough for features) via `record_sim_video`.
    - A drift list if anything diverged from the spec.
3. A **state transition** moving the Linear issue from `In Progress` to `In Review`.

The exact wording the agent uses to drive this lives in WORKFLOW.md and is versioned alongside the codebase.

## Known limitations

First three real dispatches (SAM-49 / SAM-50 / SAM-51 on 2026-05-13) surfaced two follow-ups worth knowing about. Both are in the Linear backlog:

- **[SAM-52](https://linear.app/samcorp/issue/SAM-52) — Tighten WORKFLOW.md bookkeeping enforcement.** SAM-50's agent produced a clean PR but skipped the pickup comment, closeout comment, and `In Review` transition. SAM-49 and SAM-51 followed the bookkeeping perfectly. Hypothesis: denser AC lists push the bookkeeping out of the agent's attention. Mitigation likely structural changes to the prompt template (a "required artifacts before exit" checklist surfaced earlier in the prompt).
- **[SAM-53](https://linear.app/samcorp/issue/SAM-53) — Orchestrator: escalate to `Needs Human` after N unproductive continuation turns.** When an agent finishes the work but skips the state transition (as in SAM-50), Symphony §7.1's continuation logic re-dispatches indefinitely up to `max_turns`. The orchestrator now stops after `agent.max_unproductive_successes` successful active turns, exposes the issue in status as `needs_human`, posts an escalation comment, and moves the Linear issue to `Needs Human`.

Neither blocks first-run usage of the harness; together they close the loop on the SAM-50 failure mode.

## When this harness does NOT apply

- Triaging itself (`/triage`) — runs locally, not in a VM.
- Spec drafting (`/to-issues`, `/to-prd`) — local.
- Reading and reasoning about code without changes — local is fine.
- `HITL`-labeled tickets — orchestrator's pickup filter excludes them; humans pick them up directly.
- `Needs Human` state — if an agent mid-flight discovers a decision only a human can make, it transitions to `Needs Human` and exits; orchestrator releases the claim.

The harness exists for the **`Ready` + `AFK`** subset only.
