#!/usr/bin/env bash
# Builds the spur-base Tart VM that every per-issue VM clone inherits.
# Idempotent — re-run after editing agent-vm-bootstrap.in-vm.sh to update
# the base image, or pass --rebuild to start fresh from the OCI image.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
VM_NAME="${SPUR_AGENT_VM_NAME:-spur-base}"
BASE_IMAGE="${SPUR_AGENT_BASE_IMAGE:-ghcr.io/cirruslabs/macos-tahoe-xcode:latest}"
IN_VM_SCRIPT="$SCRIPT_DIR/agent-vm-bootstrap.in-vm.sh"

# SSH key dedicated to the spur-agent harness. Public key is baked into
# spur-base; per-issue VM clones inherit it. Private key stays on the host.
HARNESS_KEY="${HOME}/.ssh/spur-agent-vm"
HARNESS_KEY_PUB="${HARNESS_KEY}.pub"

# Cirruslabs default credentials for first connection (used once, then we
# install our key and never touch the password again).
VM_USER="admin"
VM_PASS="admin"

# SSH options for automation (each per-issue clone has the same initial
# host key; we don't want known_hosts churn).
SSH_OPTS=(
  -o StrictHostKeyChecking=no
  -o UserKnownHostsFile=/dev/null
  -o LogLevel=ERROR
  -o ConnectTimeout=5
)

REBUILD=0
for arg in "$@"; do
  case "$arg" in
    --rebuild) REBUILD=1 ;;
    -h|--help)
      cat <<'EOF'
Usage: scripts/agent-vm-bootstrap.sh [--rebuild]

Builds the spur-base Tart VM by cloning the cirruslabs Tahoe+Xcode image,
installing the harness SSH key, then running the Spur toolchain installer
(Go, Supabase CLI, colima, gh, Claude Code) inside the VM.

  --rebuild   Delete the existing spur-base VM and start fresh from the
              OCI image. Use this when the in-VM bootstrap has diverged
              significantly.

Env vars:
  SPUR_AGENT_VM_NAME      VM name (default: spur-base)
  SPUR_AGENT_BASE_IMAGE   OCI source (default: cirruslabs Tahoe+Xcode)
EOF
      exit 0 ;;
  esac
done

command -v tart >/dev/null || { echo "tart not installed (brew install cirruslabs/cli/tart)"; exit 1; }
command -v sshpass >/dev/null || { echo "sshpass not installed (brew install cirruslabs/cli/sshpass)"; exit 1; }

if [ ! -f "$IN_VM_SCRIPT" ]; then
  echo "Missing in-VM bootstrap script: $IN_VM_SCRIPT"
  exit 1
fi

# Generate the harness SSH key if missing.
if [ ! -f "$HARNESS_KEY" ]; then
  echo "==> Generating harness SSH key at $HARNESS_KEY (ed25519, no passphrase)"
  ssh-keygen -t ed25519 -N "" -C "spur-agent harness" -f "$HARNESS_KEY"
fi

if [ "$REBUILD" -eq 1 ] && tart list 2>/dev/null | awk '{print $2}' | grep -qx "$VM_NAME"; then
  echo "==> --rebuild: deleting existing $VM_NAME"
  tart stop "$VM_NAME" 2>/dev/null || true
  tart delete "$VM_NAME"
fi

if ! tart list 2>/dev/null | awk '{print $2}' | grep -qx "$VM_NAME"; then
  echo "==> Cloning $BASE_IMAGE → $VM_NAME"
  tart clone "$BASE_IMAGE" "$VM_NAME"
fi

VM_PID=""
cleanup() {
  if [ -n "$VM_PID" ] && kill -0 "$VM_PID" 2>/dev/null; then
    echo "==> Stopping $VM_NAME"
    tart stop "$VM_NAME" 2>/dev/null || true
    wait "$VM_PID" 2>/dev/null || true
  fi
}
trap cleanup EXIT INT TERM

echo "==> Booting $VM_NAME (headless)"
tart run "$VM_NAME" --no-graphics >/tmp/spur-base-tart.log 2>&1 &
VM_PID=$!

echo "==> Waiting for SSH (up to 6 min)"
VM_IP=""
for i in $(seq 1 180); do
  VM_IP=$(tart ip "$VM_NAME" 2>/dev/null || true)
  if [ -n "$VM_IP" ] && nc -zw2 "$VM_IP" 22 2>/dev/null; then
    echo "==> SSH open at $VM_IP (after ${i}s of polling)"
    break
  fi
  sleep 2
done
if [ -z "$VM_IP" ] || ! nc -zw2 "$VM_IP" 22 2>/dev/null; then
  echo "VM never became SSH-reachable. Tart log:"
  tail -30 /tmp/spur-base-tart.log
  exit 1
fi

# Give sshd a moment to finish accepting after the port opens.
sleep 3

echo "==> Installing harness SSH public key into VM's authorized_keys"
PUB_KEY_CONTENT="$(cat "$HARNESS_KEY_PUB")"
sshpass -p "$VM_PASS" ssh "${SSH_OPTS[@]}" "$VM_USER@$VM_IP" \
  "mkdir -p ~/.ssh && chmod 700 ~/.ssh && \
   grep -qxF '$PUB_KEY_CONTENT' ~/.ssh/authorized_keys 2>/dev/null || \
     echo '$PUB_KEY_CONTENT' >> ~/.ssh/authorized_keys && \
   chmod 600 ~/.ssh/authorized_keys"

echo "==> Verifying key-based SSH"
ssh "${SSH_OPTS[@]}" -i "$HARNESS_KEY" "$VM_USER@$VM_IP" 'echo "key-based SSH OK ($(whoami)@$(hostname))"'

echo "==> Running in-VM bootstrap (this takes 10-20 min)"
ssh "${SSH_OPTS[@]}" -i "$HARNESS_KEY" "$VM_USER@$VM_IP" 'bash -s' < "$IN_VM_SCRIPT"

echo "==> Bootstrap complete. Shutting down $VM_NAME cleanly."
# Trap handles tart stop + wait.
exit 0
