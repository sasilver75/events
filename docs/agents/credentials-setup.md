# Agent harness credentials setup

The production harness runs Codex app-server inside per-issue Tart VMs with
`credentials.linear_access: host_proxy`. Linear stays on the host behind the
`linear_graphql` dynamic tool; GitHub enters the VM so the agent can push a
branch and open a PR.

Required credentials:

1. Your **Linear API key** for tracker reads and host-side `linear_graphql`.
2. A **fine-grained GitHub PAT** scoped to `sasilver75/events`.
3. Optional **Codex state snapshot** at `~/.spur/codex-harness/` if the VM does
   not already have usable Codex auth.

## 1. Linear API key

1. Sign in to Linear as yourself.
2. Go to <https://linear.app/samcorp/settings/account/security>.
3. Generate a personal API key and copy the `lin_api_...` value.
4. Store it in macOS Keychain:

   ```sh
   scripts/spur-store-harness-secret linear
   ```

   If Keychain access is awkward, use the chmod-600 env-file fallback:

   ```sh
   scripts/spur-store-harness-secret --env-file linear
   ```

## 2. GitHub PAT

1. Go to <https://github.com/settings/tokens?type=beta> and generate a fine-grained token.
2. Resource owner: `sasilver75`.
3. Repository access: only `sasilver75/events`.
4. Repository permissions:
   - `Contents`: read and write
   - `Pull requests`: read and write
   - `Metadata`: read-only
5. Expiration: 30 days.
6. Store it:

   ```sh
   scripts/spur-store-harness-secret github
   ```

   Env-file fallback:

   ```sh
   scripts/spur-store-harness-secret --env-file github
   ```

## 3. Codex State

If the base VM already has Codex auth, no host snapshot is needed. Otherwise
create a filtered snapshot:

```sh
scripts/spur-snapshot-codex.sh
```

The script copies `~/.codex/` to `~/.spur/codex-harness/` while excluding logs,
sessions/history, SQLite state, shell snapshots, temporary files, and other
bulky local state. Set `SPUR_HARNESS_CODEX_DIR` to that directory before
running the harness, or let `scripts/spur-codex-canary` use it automatically
when it exists.

## 4. Harness SSH Key

Generated automatically by `scripts/agent-vm-bootstrap.sh` the first time it
runs, at `~/.ssh/spur-agent-vm`. To rotate:

```sh
rm ~/.ssh/spur-agent-vm ~/.ssh/spur-agent-vm.pub
scripts/agent-vm-bootstrap.sh --rebuild
```

## First Run

Source a small env file before running the harness:

```sh
# ~/.spur/env
export LINEAR_API_KEY="$(security find-generic-password -a "$USER" -s spur-linear-api-key -w)"
export SPUR_HARNESS_GITHUB_TOKEN="$(security find-generic-password -a "$USER" -s spur-harness-github-token -w)"
# SPUR_HARNESS_SSH_KEY defaults to ~/.ssh/spur-agent-vm.
# SPUR_HARNESS_CODEX_DIR is optional.
```

Then:

```sh
source ~/.spur/env
scripts/spur-agent SAM-49
```

Useful checks:

```sh
cd orchestrator
go run ./cmd/spur-orchestrator --codex-smoke --workflow ../WORKFLOW.md
go run ./cmd/spur-orchestrator --once --preflight --workflow ../WORKFLOW.md
go run ./cmd/spur-orchestrator --once --issue SAM-49 --preflight --workflow ../WORKFLOW.md
```

## Residual Risk

The Linear key is host-held in production and should not be written into the
VM. The VM receives:

- `GITHUB_TOKEN`, scoped to `sasilver75/events`.
- Codex auth/config only if you explicitly provide `SPUR_HARNESS_CODEX_DIR` or
  bake it into the base VM.

The `spur-base` image should contain no long-lived Linear or GitHub secrets.
Credentials are injected per run and the per-issue VM remains isolated by Tart.
