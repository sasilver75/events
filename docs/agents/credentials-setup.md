# Agent harness — credentials setup

The orchestrator and per-issue VMs need:

1. Your **Linear API key** (one key, used by both orchestrator and per-VM agent).
2. A **fine-grained GitHub PAT** scoped to `sasilver75/events`.
3. A **snapshot of your Claude Code identity** — your settings, slash commands, plugins, and the OAuth credential pulled from macOS Keychain — sitting at `~/.spur/claude-harness/` for the `before_run` hook to ship into each per-issue VM.

All three are tied to **your personal accounts** — no separate "harness" identities to provision. See [`harness.md` §Credentials](./harness.md) for the threat-model rationale.

## 1. Personal Linear API key

1. Sign in to Linear as yourself.
2. Go to <https://linear.app/samcorp/settings/account/security>.
3. Generate a personal API key. Copy the `lin_api_…` value (last chance to see it).
4. Store it in macOS Keychain:

   ```sh
   security add-generic-password \
     -a "$USER" -s "spur-linear-api-key" \
     -w "lin_api_..."
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
   security add-generic-password \
     -a "$USER" -s "spur-harness-github-token" \
     -w "github_pat_..."
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

## On Codex

If you switch to Codex later, the same shape applies: snapshot `~/.codex/` (or wherever Codex stores its state and credential), inject into each VM via an analogous `before_run` path. Building a Codex agent-runner package alongside `agent/claudecode/` is a small future refactor; the orchestrator's `Worker` interface is already shaped to allow it.

## Residual risk you're accepting

With all three credentials being your personal accounts, a compromised per-issue VM could (in principle):

- Use the Anthropic OAuth credential to call the API as you (burns bill, exposes future conversation history — but not past, since `projects/` and `sessions/` are excluded from the snapshot).
- Use your `LINEAR_API_KEY` to read/write the entire Samcorp workspace.
- Use the fine-grained GitHub PAT to push to `sasilver75/events` only.

For Spur's threat model (single ticket author, ephemeral VMs, `after_run` hook captures activity logs), this is the right tradeoff. If you ever see suspicious activity:

1. `claude login` → rotates the OAuth token; re-run `scripts/spur-snapshot-claude.sh` to propagate.
2. Revoke and rotate the Linear API key.
3. Revoke the GitHub PAT.

The `spur-base` image deliberately contains **no secrets**. Every credential is injected per-run and dies with the per-issue VM.
