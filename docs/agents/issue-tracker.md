# Issue tracker: Linear

Spur issues and PRDs live in **Linear**, under the **Samcorp** workspace, in the **Spur** project (team key `SAM`, so issue IDs look like `SAM-12`). PRs continue to live in GitHub at `sasilver75/events` — the tracker is Linear, the code host is GitHub.

The previous tracker (GitHub Issues at `sasilver75/events`) is **frozen**: existing issues stay where they are as a historical record, but all new Spur work originates in Linear.

## How to talk to Linear

Use the Linear MCP tools (`mcp__plugin_linear_linear__*`). Never go through `gh issue *` for Spur work.

| Operation                       | Tool                                            |
| ------------------------------- | ----------------------------------------------- |
| Create or update an issue       | `save_issue`                                    |
| Read an issue (with comments)   | `get_issue` (set `includeComments: true`)       |
| List issues (filtered)          | `list_issues` with `project: "Spur"` and `state`/`label` filters |
| Comment on an issue             | `save_comment`                                  |
| List available workflow states  | `list_issue_statuses` with `team: "Samcorp"`    |
| List labels                     | `list_issue_labels` with `team: "Samcorp"`      |
| Read a project                  | `get_project`                                   |
| Read or update a project        | `save_project`                                  |

Always pass `team: "Samcorp"` and `project: "Spur"` explicitly. Don't rely on workspace defaults — the Samcorp workspace contains the System Design Tutor project too, and we don't want cross-talk.

## When a skill says "publish to the issue tracker"

Create a Linear issue via `save_issue` with `team: "Samcorp"`, `project: "Spur"`, and `state: "Backlog"`. New issues enter Backlog, get refined into Ready via `/triage`.

## When a skill says "fetch the relevant ticket"

`get_issue` with the SAM-id (e.g. `SAM-12`) and `includeComments: true`.

## PR convention

PRs target `main` on GitHub. To link a PR back to its Linear issue, include the SAM-id in the PR title:

```
feat: post-event feedback flow (SAM-12)
```

Linear's GitHub integration auto-detects the SAM-id and links the PR to the issue, mirroring open/merged/closed state. Don't use `(#N)` GitHub-issue style anymore — those numbers are now ambiguous (GitHub's old issue numbers vs Linear's `SAM-N`).

Branch naming (optional but recommended for human readability):

```
sam-12-feedback-flow
```
