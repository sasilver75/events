# Agent harness — credentials setup

The orchestrator and per-issue VMs need these credentials for the current
production path (`agent.runner: claudecode`, `credentials.linear_access:
vm_env`):

1. Your **Linear API key** (one key, used by both orchestrator and per-VM agent in `vm_env` mode).
2. A **fine-grained GitHub PAT** scoped to `sasilver75/events`.
3. A **snapshot of your Claude Code identity** — your settings, slash commands, plugins, and the OAuth credential pulled from macOS Keychain — sitting at `~/.spur/claude-harness/` for the `before_run` hook to ship into each per-issue VM.

For Codex canary runs (`--codex-canary`), the same host Linear key is required
for tracker reads, but it stays on the host and is exposed to Codex only through
the `linear_graphql` dynamic tool. GitHub credentials still enter the VM because
the agent must push branches and open PRs.

All three are tied to **your personal accounts** — no separate "harness" identities to provision. See [`harness.md` §Credentials](./harness.md) for the threat-model rationale.

## 1. Personal Linear API key

1. Sign in to Linear as yourself.
2. Go to <https://linear.app/samcorp/settings/account/security>.
3. Generate a personal API key. Copy the `lin_api_…` value (last chance to see it).
4. Store it in macOS Keychain:

   ```sh
   scripts/spur-store-harness-secret linear
   ```

   If Keychain access is awkward in the current terminal/session, use the
   chmod-600 env-file fallback instead:

   ```sh
   scripts/spur-store-harness-secret --env-file linear
   ```

## 2. Fine-grained GitHub PAT

1. Go to <https://github.com/settings/tokens?type=beta> → "Generate new token".
2. **Resource owner:** `sasilver75`.
3. **Repository access:** Only select repositories → `sasilver75/events`.
4. **Repository permissions:**
   - `Contents` → Read and write
   - `Pull requests` → Read and write
   - `Metadata` → Read-only (auto-included)
5. **Expiration:** 30 days. Calendar a rotation.
6. Copy the `github_pat_…` token. Store in Keychain:

   ```sh
   scripts/spur-store-harness-secret github
   ```

   Env-file fallback:

   ```sh
   scripts/spur-store-harness-secret --env-file github
   ```

## 3. Claude Code identity snapshot

This is the subtle one. Two facts make it worth understanding:

- **On macOS, Claude Code stores OAuth tokens in Keychain** (entry `Claude Code-credentials`), not in `~/.claude/`. A naive `rsync -a ~/.claude/ …` doesn't transport authentication.
- **Your `~/.claude/projects/`, `sessions/`, etc. accumulate every conversation you've ever had.** Copying those into every VM is wasteful and leaks your local activity history.

The snapshot script `scripts/spur-snapshot-claude.sh` handles both:

```sh
scripts/spur-snapshot-claude.sh
```

It does:

- A **filtered rsync** of `~/.claude/` → `~/.spur/claude-harness/`, **excluding** `projects/`, `sessions/`, `shell-snapshots/`, `tasks/`, `cache/`, `history.jsonl`, telemetry, etc. So you ship in: settings, skills (slash commands), plugins (MCP servers like Linear MCP), keybindings, hooks.
- An **extract** of the OAuth credential from Keychain (`security find-generic-password -s 'Claude Code-credentials' -w`) into `~/.spur/claude-harness/.credentials.json`.

Re-run this whenever you want fresh state to propagate to future VM runs:

- After `claude login` (token rotation).
- After installing a new MCP plugin or user-level slash command.
- After changing global `~/.claude/settings.json`.

The `before_run` hook ships `~/.spur/claude-harness/` into each per-issue VM's `~/.claude/`. Inside the VM the hook **also** injects the credential into the VM's Keychain (because Claude Code on macOS prefers Keychain over the `.credentials.json` file when both are available).

## 4. Harness SSH key

Generated automatically by `scripts/agent-vm-bootstrap.sh` the first time it runs, at `~/.ssh/spur-agent-vm`. No manual step. To rotate:

```sh
rm ~/.ssh/spur-agent-vm ~/.ssh/spur-agent-vm.pub
scripts/agent-vm-bootstrap.sh --rebuild
```

## Putting it together — first run

Source a small `.env`-style file before running the agent:

```sh
# ~/.spur/env  (gitignored anywhere it lives; never check in)
export LINEAR_API_KEY="$(security find-generic-password -a "$USER" -s spur-linear-api-key -w)"
export SPUR_HARNESS_GITHUB_TOKEN="$(security find-generic-password -a "$USER" -s spur-harness-github-token -w)"
# SPUR_HARNESS_LINEAR_BOT_TOKEN can equal LINEAR_API_KEY; the orchestrator
# falls back automatically if the bot var isn't set.
# SPUR_HARNESS_SSH_KEY defaults to ~/.ssh/spur-agent-vm.
# SPUR_HARNESS_CLAUDE_DIR defaults to ~/.spur/claude-harness/.
```

Then:

```sh
scripts/spur-snapshot-claude.sh    # once, plus whenever ~/.claude/ changes
source ~/.spur/env
scripts/spur-agent SAM-49          # or whatever ticket
```

`scripts/spur-codex-canary` also sources `~/.spur/env` and reads these two
Keychain entries directly when the matching env vars are not already set, so
the Codex proof path can be run without placing tokens in shell history. Both
helper scripts use `~/Library/Keychains/login.keychain-db` by default; set
`SPUR_HARNESS_KEYCHAIN` to override that path.

## 5. Codex canary credentials

The Codex runner now exists behind `agent.runner: codex`, but production
defaults remain Claude Code until a real issue canary proves the handoff.

Use this readiness sequence:

```sh
source ~/.spur/env
scripts/spur-snapshot-codex.sh   # optional if the VM does not already have Codex auth
scripts/spur-codex-canary doctor
scripts/spur-codex-canary smoke
scripts/spur-codex-canary discover
scripts/spur-codex-canary preflight <SAM-N>
scripts/spur-codex-canary run <SAM-N>
scripts/spur-codex-canary verify <SAM-N>
scripts/spur-codex-canary checklist <SAM-N>
```

In this mode:

- `LINEAR_API_KEY` is still required on the host for tracker reads and the
  host-side `linear_graphql` dynamic tool. Discovery can run with only this
  Linear key; issue preflight and real runs also require GitHub.
- `SPUR_HARNESS_GITHUB_TOKEN` is still injected into the VM.
- `SPUR_LINEAR_TOKEN` is intentionally empty for hooks and `LINEAR_API_KEY` is
  omitted from the agent environment.
- `SPUR_HARNESS_CODEX_DIR` is optional. If set, `before_run` ships that
  filtered Codex snapshot into the VM as `~/.codex/`; if unset, the VM must
  already have whatever Codex auth the app-server needs. The
  `scripts/spur-codex-canary` wrapper automatically uses
  `~/.spur/codex-harness` when that directory exists.
- `scripts/spur-snapshot-codex.sh` intentionally excludes bulky local state
  such as logs, sessions, SQLite logs, temporary files, shell snapshots, and
  computer-use artifacts. The snapshot should contain auth/config/plugin-like
  state, not conversation history.

## Residual risk you're accepting

With the default `vm_env` production path, all three credentials are your
personal accounts and a compromised per-issue VM could (in principle):

- Use the Anthropic OAuth credential to call the API as you (burns bill, exposes future conversation history — but not past, since `projects/` and `sessions/` are excluded from the snapshot).
- Use your `LINEAR_API_KEY` to read/write the entire Samcorp workspace.
- Use the fine-grained GitHub PAT to push to `sasilver75/events` only.

In the Codex `host_proxy` path, the Linear key should not enter the VM; the
remaining VM credential exposure is GitHub plus the selected agent runtime's
identity. Verify that with the canary checklist before changing production
defaults.

For Spur's threat model (single ticket author, ephemeral VMs, `after_run` hook captures activity logs), this is the right tradeoff. If you ever see suspicious activity:

1. `claude login` → rotates the OAuth token; re-run `scripts/spur-snapshot-claude.sh` to propagate.
2. Revoke and rotate the Linear API key.
3. Revoke the GitHub PAT.

The `spur-base` image deliberately contains **no secrets**. Credentials are
injected per-run according to the selected credential mode and die with the
per-issue VM.
