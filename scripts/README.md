# scripts/

Repo-level dev scripts — anything that's useful across the iOS, server, and
db subprojects (bootstrap, lint runners, local environment helpers).

Per-language tooling lives inside the relevant subproject (`ios/`,
`server/`, `db/`). Only put a script here if it spans more than one of them.

## Git hooks

`git-hooks/pre-push` runs `swift-format lint` + `xcodebuild test` against the
worktree's pinned simulator before each push. iOS CI doesn't run on GitHub
Actions (macOS runners are 10× the Linux billing rate on private repos), so
this hook is the only thing catching iOS regressions before merge.

Enable per clone:

    git config core.hooksPath scripts/git-hooks

Bypass with `git push --no-verify` when you know what you're doing.

## Agent harness

`spur-agent` dispatches one Linear issue through the default production runner.

`spur-publish-preflight` guards the final publish path. Run it before
committing, pushing, or opening a PR for a Linear issue:

```sh
scripts/spur-publish-preflight SAM-12
```

It stops when the current branch appears to belong to another `SAM-N`, because
that usually means the work would stack one Linear issue on another issue's
branch. Create a clean branch from `origin/main` and carry over only the
intended diff before publishing.

`spur-dashboard` serves a local read-only dashboard for harness status
snapshots. With no arguments it aggregates `/tmp/spur-orchestrator/*.json`;
pass a file to follow one daemon snapshot:

```sh
scripts/spur-dashboard
scripts/spur-dashboard /tmp/spur-orchestrator/status.json
```

`spur-store-harness-secret` stores Linear/GitHub harness tokens through a
silent prompt, avoiding shell history. It defaults to macOS Keychain and also
supports a chmod-600 `~/.spur/env` fallback with `--env-file`.

`spur-codex-canary` is a backwards-compatible Codex readiness/run wrapper:

```sh
scripts/spur-snapshot-codex.sh
scripts/spur-codex-canary doctor
scripts/spur-codex-canary smoke
scripts/spur-codex-canary discover
scripts/spur-codex-canary preflight SAM-12
scripts/spur-codex-canary run SAM-12
scripts/spur-codex-canary verify SAM-12
scripts/spur-codex-canary checklist SAM-12
```

The status file defaults to
`/tmp/spur-orchestrator/<SAM-id>-codex.json`. If
`~/.spur/codex-harness` exists, `spur-codex-canary` exports it as
`SPUR_HARNESS_CODEX_DIR` for discovery, preflight, and run commands. The
wrapper sources `~/.spur/env` and also reads `spur-linear-api-key` and
`spur-harness-github-token` from macOS Keychain when the matching env vars are
unset.
