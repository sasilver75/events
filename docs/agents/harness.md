# Agent harness

How agents safely run Spur work end-to-end without supervision.

The harness implements [OpenAI's Symphony spec](https://github.com/openai/symphony/blob/main/SPEC.md) adapted for Spur's environment. Symphony is "a long-running automation service that continuously reads work from an issue tracker (Linear), creates an isolated workspace for each issue, and runs a coding agent session for that issue inside the workspace" (spec §1).

The two artifacts are:

- **[`WORKFLOW.md`](../../WORKFLOW.md)** at the repo root — the per-issue prompt template + runtime config. Workflow-author concern.
- **`orchestrator/`** — Go implementation of the scheduler/runner. Orchestrator-author concern.

The two are kept apart deliberately. WORKFLOW.md is policy ("what the agent does for an issue"); `orchestrator/` is mechanism ("how issues get from Linear to a running agent").

## Adaptations from Symphony

| Spec assumption                                | Spur reality                                                                                                                                |
| ---------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------- |
| Workspace = filesystem directory               | Workspace = **Tart VM clone** of `spur-base`. Per-issue VM named `spur-ticket-<sanitized-issue-id>` (e.g. `spur-ticket-SAM-12`).            |
| Hooks run on host with `cwd = workspace_path`  | Hooks run on host with `SPUR_VM_NAME`, `SPUR_VM_IP`, `SPUR_ISSUE_ID` env vars. They orchestrate VM interactions (`tart`, `ssh`) themselves. |
| Agent Runner uses Codex App Server JSON-RPC    | Agent Runner uses **Claude Code headless mode** (`claude --print --output-format=stream-json` or the Agent SDK).                            |
| `max_concurrent_agents` default 10             | Capped at **2** by Apple's macOS-guest license. Start at 1 for first runs.                                                                  |
| `active_states: [Todo, In Progress]` default   | **`[Ready, In Progress]`**. Backlog/Todo are not pickup-eligible.                                                                            |
| Pickup = "in active state, not running/claimed" | Same plus: label `AFK` present, label `HITL` absent, all blockers in `Done` state.                                                          |
| Workspace cleanup on terminal state            | **Tart VM stopped and deleted when issue reaches `Done`/`Canceled`/`Duplicate`** (spec §9.1: workspaces persist across runs for the same issue). |

Everything else from the spec is implemented verbatim: §4 domain model, §5 WORKFLOW.md contract, §7 orchestration state machine, §8 poll loop, §9.5 safety invariants, §11 Linear integration.

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
4. **`after_create` hook** (first run only). Clone repo into VM, check out a fresh branch `sam-<id>-<slug>`, run `scripts/spur-up.sh` to boot Supabase + Go server inside the VM.
5. **`before_run` hook** (every run). Inject credentials (per "Credentials" section below). Fetch latest Linear ticket body via the orchestrator's Linear adapter. Write to `/tmp/issue.json` inside the VM.
6. **Agent Runner.** Launch Claude Code headless inside the VM with the rendered WORKFLOW.md prompt. Stream events back to the orchestrator. Continuation turns per spec §7.1 if the issue is still active after a clean exit.
7. **`after_run` hook.** Collect logs, sync any artifacts the agent left in the VM out to the orchestrator's per-run log dir.
8. **Reconciliation.** Each subsequent tick re-fetches tracker state for running issues. If state moves to terminal, orchestrator kills the worker and runs `before_remove` hook + `tart delete`.

## Credentials

The VM is ephemeral; long-lived credentials live in the host's macOS Keychain and are injected per run. The base image contains no secrets. See [`credentials-setup.md`](./credentials-setup.md) for the operator checklist.

| Credential                | Purpose                                 | Injection                                                      | Scope                                                                                                                                                                                                                              |
| ------------------------- | --------------------------------------- | -------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Anthropic / Claude Code   | Running Claude Code inside the VM       | Copy `~/.spur/claude-harness/` (a snapshot of your personal `~/.claude/`) into the VM | **Your personal Anthropic identity.** Anthropic doesn't sell a "harness only" tier, and a separate seat costs real money without buying meaningful isolation for a single-author project. |
| GitHub access             | `git clone`, `git push`, `gh pr create` | Inject a fine-grained PAT as `GITHUB_TOKEN` env var            | Fine-grained PAT scoped **only** to `sasilver75/events` with `Contents: write` + `Pull requests: write` + `Metadata: read`. 30-day expiration. GitHub's fine-grained PATs are cheap to scope, so we do. |
| Linear access             | Read ticket, post closeout              | Inject as `LINEAR_API_KEY` env var                             | **Your personal Linear API key.** Linear's keys are workspace-scoped (no per-project keys), so a "bot" key would still have full Samcorp access — and you're the only member of Samcorp, so a bot offers no real isolation. |

**Residual risk we accept:** a compromised per-issue VM could (in principle) call the Anthropic API as you, read/write your Samcorp workspace in Linear, and push to `sasilver75/events`. Mitigations: VMs are ephemeral; rotate the Linear key or GitHub PAT if anything looks suspicious; the GitHub blast radius is genuinely contained by the fine-grained PAT.

**Upgrade path (deferred):** Symphony's `linear_graphql` dynamic-tool pattern (spec §10.5) lets the orchestrator hold the Linear key on the host and expose a GraphQL proxy to the in-VM agent — the token never leaves the host. Worth adopting after the core loop works, not before.

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
- **[SAM-53](https://linear.app/samcorp/issue/SAM-53) — Orchestrator: escalate to `Needs Human` after N unproductive continuation turns.** When an agent finishes the work but skips the state transition (as in SAM-50), Symphony §7.1's continuation logic re-dispatches indefinitely up to `max_turns`. The orchestrator should detect "PR exists + state still `In Progress`" as a stuck loop and escalate to `Needs Human` instead of burning more turns.

Neither blocks first-run usage of the harness; together they close the loop on the SAM-50 failure mode.

## When this harness does NOT apply

- Triaging itself (`/triage`) — runs locally, not in a VM.
- Spec drafting (`/to-issues`, `/to-prd`) — local.
- Reading and reasoning about code without changes — local is fine.
- `HITL`-labeled tickets — orchestrator's pickup filter excludes them; humans pick them up directly.
- `Needs Human` state — if an agent mid-flight discovers a decision only a human can make, it transitions to `Needs Human` and exits; orchestrator releases the claim.

The harness exists for the **`Ready` + `AFK`** subset only.
