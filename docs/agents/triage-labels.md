# Triage Labels

The skills speak in terms of five canonical triage roles. This file maps those roles to the actual label strings used in this repo's issue tracker.

| Label in mattpocock/skills | Label in our tracker | Meaning                                  |
| -------------------------- | -------------------- | ---------------------------------------- |
| `needs-triage`             | `needs-triage`       | Maintainer needs to evaluate this issue  |
| `needs-info`               | `needs-info`         | Waiting on reporter for more information |
| `ready-for-agent`          | `ready-for-agent`    | Fully specified, ready for an AFK agent  |
| `ready-for-human`          | `ready-for-human`    | Requires human implementation            |
| `wontfix`                  | `wontfix`            | Will not be actioned                     |

When a skill mentions a role (e.g. "apply the AFK-ready triage label"), use the corresponding label string from this table.

Edit the right-hand column to match whatever vocabulary you actually use.

## Workflow

Every issue starts as `needs-triage` (GitHub default for new issues). **Before any substantive work begins**, transition the label to the appropriate role:

- HITL work (requires maintainer hands — credentials, hosted account setup, deploy platform, App Store submission, anything an unattended agent can't finish) → `ready-for-human`
- Fully specified for unattended agent execution → `ready-for-agent`
- Underspecified, awaiting reporter input → `needs-info`
- Rejected → `wontfix`

`gh issue edit N --remove-label needs-triage --add-label <role>`, or run `/triage` if the right role is unclear. A closed issue should never still be labeled `needs-triage` — that means the workflow was bypassed.
