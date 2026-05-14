#!/usr/bin/env bash
# Snapshot host-level Codex state into ~/.spur/codex-harness/ for optional
# shipment into per-issue Tart VMs during Codex canaries.
#
# Deliberately excluded: logs, sessions/history, SQLite log/state DBs,
# temporary files, shell snapshots, and computer-use artifacts.

set -euo pipefail

DEST="${SPUR_HARNESS_CODEX_DIR:-$HOME/.spur/codex-harness}"
SRC="${CODEX_HOME:-$HOME/.codex}"

if [ ! -d "$SRC" ]; then
  echo "Source not found: $SRC (have you run Codex on this host?)" >&2
  exit 1
fi

if ! command -v rsync >/dev/null; then
  echo "rsync not installed" >&2
  exit 1
fi

echo "==> Snapshotting $SRC -> $DEST (filtered)"
mkdir -p "$DEST"
chmod 700 "$DEST"

rsync -a --delete --delete-excluded \
  --exclude='log/' \
  --exclude='logs/' \
  --exclude='logs_*.sqlite*' \
  --exclude='sessions/' \
  --exclude='archived_sessions/' \
  --exclude='history.jsonl' \
  --exclude='session_index.jsonl' \
  --exclude='transcription-history.jsonl' \
  --exclude='cache/' \
  --exclude='shell_snapshots/' \
  --exclude='computer-use/' \
  --exclude='.tmp/' \
  --exclude='tmp/' \
  --exclude='state_*.sqlite*' \
  "$SRC/" "$DEST/"

chmod -R go-rwx "$DEST"

echo "==> Snapshot ready:"
du -sh "$DEST" | awk '{printf "  size: %s\n", $1}'
if [ -f "$DEST/auth.json" ]; then
  printf "  auth: present\n"
else
  printf "  auth: auth.json not found\n"
fi

echo
echo "Set SPUR_HARNESS_CODEX_DIR=$DEST before a Codex canary to ship it into the VM."
