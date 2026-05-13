# Agent docs

This directory documents the agent harness for Spur: how automated and
human-in-the-loop agents bind to this repo's issue tracker, consume its
domain documentation, and apply triage labels. Land here when a skill or
agent prompt mentions an external contract (issue tracker, triage roles,
domain lookups) and you need to know which concrete tool, command, or label
this repo uses.

## Files

| File | Purpose |
| --- | --- |
| [`domain.md`](./domain.md) | How engineering skills consume `CONTEXT.md` and `docs/adr/` when exploring the codebase. |
| [`issue-tracker.md`](./issue-tracker.md) | Binding of issue-tracker operations to GitHub Issues via the `gh` CLI. |
| [`triage-labels.md`](./triage-labels.md) | Canonical role → label mapping (`needs-triage`, `ready-for-agent`, etc.) and the triage workflow. |

## Agent-prompt contract

Per-repo working conventions for agents — style, testing posture, branch and
PR rules, multi-session coordination — live in [`CLAUDE.md`](../../CLAUDE.md)
at the repo root.
