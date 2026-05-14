# Agent harness

How agents safely run Spur work end-to-end without supervision.

The harness is a Symphony-inspired implementation adapted for Spur's macOS/iOS
environment. [Symphony](https://github.com/openai/symphony/blob/main/SPEC.md)
defines a long-running service that reads work from Linear, creates an
isolated workspace for each issue, and runs a coding-agent session inside that
workspace. Spur uses the same control-plane shape, with Tart VMs as the
workspace primitive.

The two main artifacts are:

- [`WORKFLOW.md`](../../WORKFLOW.md): repo-owned prompt template and runtime config.
- [`orchestrator/`](../../orchestrator): Go scheduler/runner implementation.

## Spur vs Symphony

| Symphony/reference shape | Spur harness |
| --- | --- |
| Workspace is a filesystem directory. | Workspace is a Tart VM clone of `spur-base`, named `spur-ticket-<sanitized-issue-id>`. |
| Agent runner launches `codex app-server`. | Spur launches `codex app-server` inside the per-issue VM over SSH. |
| `WORKFLOW.md` has a `codex` section. | Spur uses the `codex` section as the production runner config. |
| Hooks run with `cwd = workspace_path`. | Hooks run on the host and use `tart`, `ssh`, and `scp` against the VM. |
| `max_concurrent_agents` default is 10. | Spur caps at 2 because Apple's macOS guest license allows two concurrent guests. |
| Active states default to `Todo` / `In Progress`. | Spur picks up `Ready` / `In Progress` only. |
| Candidate eligibility is active-state plus scheduler availability. | Spur also requires `AFK`, excludes `HITL`, requires blockers to be `Done`, and only claims unassigned issues or issues assigned to the Linear API user. |

## Implementation Status

Implemented:

- `WORKFLOW.md` loader, YAML front matter parser, typed config, validation, and Liquid prompt rendering.
- Linear tracker reader for candidate fetch, running-issue refresh, paginated terminal-state lookup, current-user lookup, and narrow `Needs Human` escalation.
- Eligibility filter for `AFK`/`HITL` labels, blockers, and assignee ownership.
- Tart workspace manager: clone/reuse, boot, SSH readiness, stop/delete.
- Host hook runner for `after_create`, `before_run`, `after_run`, and `before_remove`.
- Codex app-server runner over SSH, including initialize, thread start/resume, turn start, completion/error normalization, dynamic tool calls, token usage, and rate-limit capture.
- Host-side `linear_graphql` dynamic tool so Linear credentials stay on the host in `host_proxy` mode.
- Codex smoke check via `spur-orchestrator --codex-smoke`.
- Preflight via `spur-orchestrator --once --preflight` or `--once --issue <SAM-N> --preflight`.
- Polling orchestrator state machine with bounded dispatch, retries, continuation resume, cancellation, terminal cleanup, and `agent.max_turns`.
- Dynamic `WORKFLOW.md` reload for prompt text, hooks, limits, active/terminal states, and Codex command/timeout settings.
- Optional JSON status snapshots via `--status-file`, including running/retrying/needs-human work, recent runs, Codex token totals, and rate-limit telemetry.
- Successful-continuation loop guard that records `needs_human`, posts an escalation comment, and moves the Linear issue to `Needs Human`.

Not implemented:

- Dedicated dashboard. Current operator visibility is structured logs plus optional JSON snapshots.
- Full stall detection based on last Codex event timestamp. This remains a conformance/hardening item.
- General-purpose host-side Linear mutations. Normal comments/state changes are still performed by the agent per `WORKFLOW.md`.

## Lifecycle

1. **Poll tick.** Reconcile running issues, then fetch Linear candidates in active states.
2. **Eligibility.** Keep issues with `AFK`, without `HITL`, without open blockers, and with no assignee or an assignee matching the Linear API user.
3. **Dispatch.** Sort by priority, creation time, and identifier; claim up to `max_concurrent_agents`.
4. **Workspace prep.** Clone or boot `spur-ticket-<id>` from `spur-base` and wait for SSH.
5. **Hooks.** Run `after_create` on first creation and `before_run` before each attempt. In `host_proxy`, `SPUR_LINEAR_TOKEN` is intentionally empty.
6. **Codex run.** Launch `codex app-server` in `/Users/admin/events`, advertise `linear_graphql`, and run the rendered issue prompt.
7. **Artifacts.** Run `after_run` and copy Codex logs plus issue snapshots into the host run-log directory.
8. **Reconciliation.** Terminal tracker states cancel the worker, run `before_remove`, and delete the VM. Non-active states cancel without deletion.

## Credentials

Long-lived credentials live on the host. See [credentials-setup.md](./credentials-setup.md).

| Credential | Purpose | Boundary |
| --- | --- | --- |
| Linear API key | Tracker reads and `linear_graphql` | Host only in production. |
| GitHub PAT | Clone, push, PR creation | Injected into the VM as `GITHUB_TOKEN`. |
| Codex auth/config | Run Codex app-server | Either already in the VM or shipped from optional `SPUR_HARNESS_CODEX_DIR`. |

`tracker.api_key` follows Symphony's schema: it may be a literal token or a
`$VAR` reference. Spur still supports `LINEAR_API_KEY` as a compatibility
fallback, but only when `tracker.api_key` is omitted. Explicit YAML values are
authoritative: a literal token is used as-is, and a `$VAR` reference must be set
in that exact environment variable.

## End-of-Run Publication

The publication contract lives in `WORKFLOW.md`, not the orchestrator. The
agent is responsible for:

1. Creating a PR against `main` with the Linear issue identifier in the title.
2. Posting a Linear closeout comment with PR link, AC table, drift list, and test evidence.
3. Moving the issue from `In Progress` to `In Review`.

## Scope

The harness exists for `Ready` + `AFK` issues. It does not pick up `HITL` or
`Needs Human` work, and it is not used for local triage, PRD drafting, or
manual code reading.
