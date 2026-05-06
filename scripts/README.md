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
