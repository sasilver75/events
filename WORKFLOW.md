---
# Symphony workflow contract for Spur.
# See https://github.com/openai/symphony/blob/main/SPEC.md §5 for schema.
# Adaptations for Spur are documented in docs/agents/harness.md.

tracker:
  kind: linear
  endpoint: https://api.linear.app/graphql
  api_key: $LINEAR_API_KEY
  project_slug: spur-c956b9432c2f
  active_states:
    - Ready
    - In Progress
  terminal_states:
    - Done
    - Canceled
    - Duplicate

polling:
  interval_ms: 30000

workspace:
  # Per-issue Tart VM cloned from this base. The Workspace Manager uses the
  # sanitized issue identifier as the VM name (e.g. SAM-12 → spur-ticket-SAM-12).
  base_image: spur-base
  root: ~/.tart/vms

hooks:
  # Hooks run on the host with these env vars available:
  #   SPUR_VM_NAME, SPUR_VM_IP, SPUR_ISSUE_ID, SPUR_ISSUE_IDENTIFIER,
  #   SPUR_ISSUE_JSON, SPUR_GITHUB_TOKEN, SPUR_LINEAR_TOKEN,
  #   SPUR_RUN_LOG_DIR, SPUR_SSH_KEY, SPUR_HARNESS_CODEX_DIR
  #
  # SSH connections always pass -i "$SPUR_SSH_KEY" so the harness key is used
  # explicitly (not whatever default key the host happens to have).

  after_create: |
    # Runs once when the per-issue VM is first cloned from spur-base.
    # Auth via token-in-URL — avoids fighting the cirruslabs base image's
    # default credential.helper=osxkeychain.
    SSH_OPTS=(-i "$SPUR_SSH_KEY" -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR)
    ssh "${SSH_OPTS[@]}" admin@"$SPUR_VM_IP" 'bash -s' <<EOS
    set -euo pipefail
    git config --global user.name 'spur-agent'
    git config --global user.email 'spur-agent@local'

    cd "\$HOME"
    if [ ! -d events ] || [ ! -d events/.git ]; then
      rm -rf events
      git clone "https://x-access-token:${SPUR_GITHUB_TOKEN}@github.com/sasilver75/events.git"
    fi
    cd events
    git remote set-url origin "https://x-access-token:${SPUR_GITHUB_TOKEN}@github.com/sasilver75/events.git"
    git fetch --all --prune
    git checkout main
    git pull
    EOS

  before_run: |
    # Runs before each agent attempt. Injects credentials, the current issue
    # snapshot, and optional agent identity snapshots.
    SSH_OPTS=(-i "$SPUR_SSH_KEY" -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR)

    # 1. Optionally ship a filtered ~/.codex/ snapshot into the VM. Leave
    #    SPUR_HARNESS_CODEX_DIR empty to rely on whatever Codex identity
    #    already exists in the VM.
    if [ -n "${SPUR_HARNESS_CODEX_DIR:-}" ] && [ -d "$SPUR_HARNESS_CODEX_DIR" ]; then
      ssh "${SSH_OPTS[@]}" admin@"$SPUR_VM_IP" 'rm -rf ~/.codex && mkdir -p ~/.codex && chmod 700 ~/.codex'
      scp -r "${SSH_OPTS[@]}" "$SPUR_HARNESS_CODEX_DIR"/. admin@"$SPUR_VM_IP":'~/.codex/'
    fi

    # 2. Write the issue snapshot to /tmp inside the VM.
    printf '%s' "$SPUR_ISSUE_JSON" | ssh "${SSH_OPTS[@]}" admin@"$SPUR_VM_IP" 'cat > /tmp/issue.json'

    # 3. Refresh the git remote URL with the current PAT (in case the
    #    token was rotated between runs) and write the credentials.env
    #    file for any agent tooling that reads it. In host_proxy mode
    #    SPUR_LINEAR_TOKEN is intentionally empty; Codex uses the host-side
    #    linear_graphql dynamic tool instead.
    ssh "${SSH_OPTS[@]}" admin@"$SPUR_VM_IP" 'bash -s' <<EOS
    set -euo pipefail
    if [ -d "\$HOME/events/.git" ]; then
      cd "\$HOME/events"
      git remote set-url origin "https://x-access-token:${SPUR_GITHUB_TOKEN}@github.com/sasilver75/events.git"
    fi
    git config --global user.name 'spur-agent'
    git config --global user.email 'spur-agent@local'
    mkdir -p "\$HOME/.config/spur"
    cat > "\$HOME/.config/spur/credentials.env" <<EOF
    GITHUB_TOKEN=$SPUR_GITHUB_TOKEN
    LINEAR_API_KEY=$SPUR_LINEAR_TOKEN
    EOF
    chmod 600 "\$HOME/.config/spur/credentials.env"
    EOS

  after_run: |
    # Runs after each agent attempt (success, failure, timeout, cancel).
    SSH_OPTS=(-i "$SPUR_SSH_KEY" -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR)
    mkdir -p "$SPUR_RUN_LOG_DIR"
    scp -r "${SSH_OPTS[@]}" admin@"$SPUR_VM_IP":'~/.codex/log' "$SPUR_RUN_LOG_DIR/codex-log" 2>/dev/null || true
    scp "${SSH_OPTS[@]}" admin@"$SPUR_VM_IP":'/tmp/issue.json' "$SPUR_RUN_LOG_DIR/issue.json" 2>/dev/null || true

  before_remove: |
    # Runs before the VM is deleted (issue reached a terminal state).
    SSH_OPTS=(-i "$SPUR_SSH_KEY" -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR)
    mkdir -p "$SPUR_RUN_LOG_DIR/final"
    scp -r "${SSH_OPTS[@]}" admin@"$SPUR_VM_IP":'~/events/.build' "$SPUR_RUN_LOG_DIR/final/build-artifacts" 2>/dev/null || true

  timeout_ms: 600000

agent:
  runner: codex
  # Capped at 2 by Apple's macOS-guest license. Raising past 2 will be
  # silently ignored by the host (the 3rd boot will fail).
  max_concurrent_agents: 2
  max_turns: 20
  max_retry_backoff_ms: 300000
  # If successful agent turns keep leaving the issue active, stop dispatching
  # before burning the full max_turns budget. Operator should inspect for a
  # missing closeout comment / In Review transition.
  max_unproductive_successes: 3

credentials:
  # Keep the Linear token on the host. Codex receives a constrained
  # host-side linear_graphql dynamic tool instead of raw tracker credentials.
  linear_access: host_proxy

codex:
  command: codex app-server
  turn_timeout_ms: 3600000
  read_timeout_ms: 5000
  stall_timeout_ms: 600000
---

# Spur agent task: {{ issue.identifier }}

You are running inside a per-issue Tart VM, isolated from the host. You have
`--dangerously-skip-permissions` and full root inside the VM. Do not assume
the host filesystem exists outside this VM.

## Issue

**{{ issue.identifier }} — {{ issue.title }}**

{% if issue.priority %}Priority: {{ issue.priority }}{% endif %}
Labels: {{ issue.labels | join: ", " }}
{% if issue.blocked_by.size > 0 %}Blocked by: {{ issue.blocked_by | map: "identifier" | join: ", " }}{% endif %}

### Description

{{ issue.description }}

{% if attempt %}
> _This is **attempt {{ attempt }}** on this issue. The previous attempt left
> the workspace in some state. Inspect it (`git status`, `git log`), decide
> whether to continue from where it left off or reset to main and start over._
{% endif %}

## Canonical references

- **[CLAUDE.md](./CLAUDE.md)** — working conventions for this repo. Already
  loaded as your system context. Re-read if you need a refresher on style,
  testing posture, or architecture.
- **[CONTEXT.md](./CONTEXT.md)** — domain vocabulary. Use these terms verbatim
  in code, commit messages, PR titles, and the Linear closeout.
- **[PRD-v0.md](./PRD-v0.md)** — product spec.
- **[docs/adr/](./docs/adr/)** — locked architectural decisions. If your work
  contradicts an ADR, flag it; don't silently override.
- **[docs/agents/](./docs/agents/)** — agent-specific guides (issue tracker,
  triage labels, this harness).

## Linear access

If `LINEAR_API_KEY` is present and non-empty in your environment, use it for
Linear API comments and state transitions. If it is absent/empty and a
`linear_graphql` tool is available, use that tool for Linear GraphQL operations
instead; the host keeps the Linear credential and forwards only the GraphQL
request/response.

## Lifecycle for this run

You must follow these steps in order. Each step has explicit success criteria.
Do not skip steps. Do not proceed past a failed verification step.

## Required handoff artifacts before exit

Do not end a successful run until all of these are true:

- The issue has a pickup comment for this attempt.
- The issue state is `In Progress` while you are working.
- Your branch is pushed and a PR against `main` exists.
- The PR description links to {{ issue.identifier }} and includes the required self-assessment line.
- The Linear closeout comment exists and uses the exact structure in step 8.
- The issue state is `In Review`.

If you have opened a PR but any closeout item is missing, your next action is
to finish the missing Linear comment/state transition. Do not continue coding
or start unrelated cleanup. A run with a PR but no closeout comment and no
`In Review` transition is incomplete.

### 1. Acknowledge pickup

Transition the Linear issue from `Ready` (or whatever active state you found
it in) to `In Progress` via the Linear API. Post a brief Linear comment:
"Agent picked up {{ issue.identifier }} (attempt {{ attempt | default: 1 }})."

### 2. Set up the workspace

You are inside a Tart VM. The `after_create` hook has cloned the repo and
left it at `~/events` checked out on `main`. From here:

- **Always:** `cd ~/events && git checkout -b sam-{{ issue.identifier | downcase | replace: "sam-", "" }}-<short-slug>` (the short-slug is a 2-4 word kebab-case summary of the ticket title).
- **Only if the ticket touches `server/`, `supabase/`, `ios/`, or migrations:** run `bash scripts/spur-up.sh` to boot Supabase + the Go server. Pure docs / scripts / orchestrator tickets do NOT need the stack — skip this step entirely. Skipping saves ~10 minutes per run.

If the workspace is in an unexpected state (uncommitted changes from a prior attempt, branch already exists, etc.), inspect with `git status` / `git log` first and decide whether to continue from where you left off (continuation run) or reset to main and start over.

### 3. Decompose the acceptance criteria

Read the issue description carefully. Extract a checklist of acceptance
criteria. Write this checklist to `~/events/.spur-agent/ac-{{ issue.identifier }}.md`
so you can refer back to it as you work. Each AC must be specific enough that
you can give it a `[x]` / `[ ]` / `[~]` mark at handoff time.

### 4. Implement

Follow `/tdd` if non-trivial. For tracer-bullet (vertical) tickets, cut a
thin path through every layer end-to-end before deepening any single layer.

Architecture rules (from CLAUDE.md):
- Business logic in Go, never in PL/pgSQL. DB triggers only for mechanical
  concerns.
- iOS reads through Supabase directly (RLS-protected); iOS writes that carry
  rules go through the Go server.
- Use CONTEXT.md vocabulary verbatim.

### 5. Verify (gates that must pass before opening a PR)

For Go work:
- `cd server && gofmt -l . | (! grep .)`  (formatting clean)
- `cd server && golangci-lint run`  (lints clean)
- `cd server && go test ./...`  (tests pass against the in-VM Postgres)

For migrations:
- `make migrate-up` succeeds, then `make migrate-down` of the new migration
  succeeds (reversible).

For iOS work:
- `xcodebuild -derivedDataPath ./.build/derived-data -scheme Spur build`
  succeeds.
- The iOS app launches on the simulator (use XcodeBuildMCP).
- Smoke-test the changed UI flow: take a screenshot of each non-trivial new
  screen via `screenshot`.
- For bug fixes: **record a `before` video reproducing the bug** (revert your
  fix, record via `record_sim_video`, restore the fix). Then **record an `after`
  video demonstrating the fix.** Save both to `~/events/.spur-agent/videos/`.
- For new features: **record a walkthrough video** showing the feature in use.

If any verification step fails, fix the underlying cause. Do not skip a flaky
test — diagnose it (`/diagnose`).

### 6. Commit

Conventional prose commit messages. Subject is imperative + concise; body
explains *why*, not *what*. Pre-commit hooks must pass; never use `--no-verify`.

### 7. Open the pull request

Title format: `<type>: <summary> ({{ issue.identifier }})`
Example: `feat: post-event feedback flow (SAM-12)`

PR description must include:
- One-line summary of what changed.
- Linear ticket link: `Linear: [{{ issue.identifier }}]({{ issue.url }})`.
- A condensed line: "Agent self-assessment: N/M ACs met — see {{ issue.identifier }} for details."
- For UI work: inline screenshots or links to the recorded videos.

Push the branch with `git push -u origin <branch>` (the `GITHUB_TOKEN`
fine-grained PAT has the necessary scopes). Then `gh pr create`.

### 8. Post the Linear closeout comment

Post a comment on {{ issue.identifier }} with this exact structure:

```
> _Closeout from agent run (attempt {{ attempt | default: 1 }})._

**PR:** <link>

**Acceptance criteria:**
- [x] AC 1 text — evidence (test name, screenshot, commit)
- [ ] AC 2 text — not met, reason
- [~] AC 3 text — partial, scope reduction rationale

**Drift from spec:** (omit if none)
- <one bullet per divergence and why>

**Artifacts:**
- Before video: <attachment or path>
- After video: <attachment or path>
- Screenshots: (inline)
```

### 9. Transition to In Review

Move the Linear issue from `In Progress` to `In Review`. This is the
handoff signal.

Before exiting, re-check the required handoff artifacts list above. If the PR
exists but the closeout comment or `In Review` transition is missing, complete
those missing steps immediately.

## Edge cases

**You realize the issue is HITL.** If, during implementation, you discover
the work requires a human decision (architectural ambiguity, missing
credentials, an external account setup), do not fake it. Transition the
Linear issue to **`Needs Human`** state, post a comment describing exactly
what decision is needed and what you tried, and exit. Do not open a PR.

**A verification step fails and you can't fix it.** Don't open the PR. Post
a Linear comment describing the failure and what you tried. Transition to
`Needs Human` if you've genuinely exhausted options, or let the orchestrator
retry (it will start a new turn on the same thread).

**Your work contradicts an ADR.** Flag it explicitly in the Linear comment —
do not silently override. Quote the ADR you're contradicting and explain
why the override is justified. Wait for human approval before merging.

**A migration slot collides with main.** Renumber **your** migration to the
next free slot; update internal cross-references; do not renumber existing
landed migrations.

## What success looks like

A reviewer opening {{ issue.identifier }} in Linear sees:
- State: `In Review`
- A closeout comment with the AC table, drift list, and artifacts inline
- A PR link, with a green CI run

The reviewer can decide trust-and-skim or zoom-in on the diff, without
needing to reconstruct what you did or why.
