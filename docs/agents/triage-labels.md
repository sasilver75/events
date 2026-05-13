# Triage Labels

The skills speak in terms of five canonical triage roles. This file maps those roles to Linear's primitives (workflow states + labels) for the Samcorp/Spur project.

Linear doesn't use a single "label" axis like GitHub did. Roles map to a combination of **workflow state** (where the issue is in its lifecycle) and **label** (orthogonal flag for agent-pickup eligibility).

| Canonical role (skills)  | Linear state   | Linear label | Meaning                                                              |
| ------------------------ | -------------- | ------------ | -------------------------------------------------------------------- |
| `needs-triage`           | `Backlog`      | *(none)*     | Captured, not yet refined. Default when a new issue is filed.        |
| `needs-info`             | `Backlog`      | `needs-info` | Waiting on reporter for more information.                            |
| `ready-for-agent`        | `Ready`        | `AFK`        | Fully specified, agent-claimable when all blockers are `Done`.       |
| `ready-for-human`        | `Ready`        | `HITL`       | Pickup-ready but requires human hands (creds, App Store, etc.).      |
| `wontfix`                | `Canceled`     | *(none)*     | Will not be actioned.                                                |

Plus two **category** labels (orthogonal — apply alongside the AFK/HITL gate):

| Canonical role | Linear label |
| -------------- | ------------ |
| `bug`          | `Bug`        |
| `enhancement` | `Feature` (or `Improvement` for incremental polish) |

Spur tickets should also carry one **area** label (orthogonal to everything above):

- `area-ios` — Swift/SwiftUI/Xcode work
- `area-server` — Go HTTP server
- `area-supabase` — migrations, RLS, Auth, Realtime, Storage
- `area-infra` — scripts, tooling, agent harness, CI

## Workflow

**Before work starts.** Every issue begins in `Backlog`. Triage moves it to a pickup state:

- HITL work (requires human hands) → state `Ready` + label `HITL`
- Fully specified for unattended agent execution → state `Ready` + label `AFK`
- Underspecified, awaiting reporter input → keep in `Backlog`, add label `needs-info`
- Rejected → state `Canceled`

Use the Linear MCP `save_issue` tool with the appropriate `state` and `addLabels`. Or run `/triage` if the right role is unclear.

**During work.** When an agent or human starts on a `Ready` issue, move it to `In Progress`. When the PR opens, move it to `In Review`. When merged, move it to `Done`. If mid-flight work needs a human decision, move it to `Needs Human` rather than back to `Ready`.

**After close.** The label persists as a record of how the work got done — an issue closed from `Ready`+`AFK` means "completed via AFK agent," `Ready`+`HITL` means "completed via HITL." Don't strip labels at close.

## Agent pickup criteria

For the `spur-agent` harness to claim a Linear issue automatically (or for a human to hand one off):

1. `project = Spur`
2. `state = Ready`
3. `label includes AFK` (and does NOT include `HITL`)
4. All `blocked-by` issues are in state `Done`
5. `assignee` is empty or `@me`
