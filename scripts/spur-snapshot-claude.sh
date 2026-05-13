#!/usr/bin/env bash
# Snapshot your host-level Claude Code identity into ~/.spur/claude-harness/
# so the agent harness can ship it into each per-issue Tart VM.
#
# What goes in:
#   - ~/.claude/.credentials.json   (extracted from macOS Keychain — this
#                                    is what authenticates the agent as you)
#   - ~/.claude/settings.json        (global Claude Code settings)
#   - ~/.claude/skills/              (your user-level slash commands)
#   - ~/.claude/plugins/             (MCP plugins like Linear MCP)
#   - ~/.claude/hooks/, plans/, ide/, keybindings.json, etc.
#
# What's deliberately excluded:
#   - projects/, sessions/, shell-snapshots/, tasks/, cache/, backups/,
#     file-history/, telemetry/, image-cache/, paste-cache/, downloads/,
#     history.jsonl, usage-data, stats-cache.json
#     (conversation history + telemetry — large, irrelevant to the VM,
#     and would leak your local activity into every VM clone)
#
# Re-run this whenever you want to propagate fresh state to future VM
# runs (after `claude login` rotation, after installing a new MCP plugin
# or slash command, etc.).

set -euo pipefail

DEST="${SPUR_HARNESS_CLAUDE_DIR:-$HOME/.spur/claude-harness}"
SRC="$HOME/.claude"

if [ ! -d "$SRC" ]; then
  echo "Source not found: $SRC (have you run 'claude login' on this host?)" >&2
  exit 1
fi

if ! command -v rsync >/dev/null; then
  echo "rsync not installed" >&2
  exit 1
fi

if ! command -v security >/dev/null; then
  echo "security command not found (this script targets macOS)" >&2
  exit 1
fi

echo "==> Snapshotting $SRC → $DEST (filtered)"
mkdir -p "$DEST"
chmod 700 "$DEST"

rsync -a --delete \
  --exclude='projects/' \
  --exclude='sessions/' \
  --exclude='shell-snapshots/' \
  --exclude='tasks/' \
  --exclude='cache/' \
  --exclude='backups/' \
  --exclude='file-history/' \
  --exclude='telemetry/' \
  --exclude='image-cache/' \
  --exclude='paste-cache/' \
  --exclude='downloads/' \
  --exclude='history.jsonl' \
  --exclude='usage-data' \
  --exclude='stats-cache.json' \
  --exclude='.credentials.json' \
  "$SRC/" "$DEST/"

echo "==> Extracting OAuth credential from macOS Keychain"
if ! security find-generic-password -s "Claude Code-credentials" -w > "$DEST/.credentials.json.tmp" 2>/dev/null; then
  echo "WARNING: No 'Claude Code-credentials' entry in Keychain." >&2
  echo "You may not be logged in yet — run 'claude login' and re-run this script." >&2
  rm -f "$DEST/.credentials.json.tmp"
  exit 1
fi
mv "$DEST/.credentials.json.tmp" "$DEST/.credentials.json"
chmod 600 "$DEST/.credentials.json"

echo "==> Snapshot ready:"
du -sh "$DEST" | awk '{printf "  size:       %s\n", $1}'
printf "  credential: %s bytes\n" "$(wc -c < "$DEST/.credentials.json" | tr -d ' ')"
if [ -d "$DEST/skills" ]; then
  printf "  skills:     %s installed\n" "$(ls -1 "$DEST/skills" 2>/dev/null | wc -l | tr -d ' ')"
fi
if [ -d "$DEST/plugins" ]; then
  printf "  plugins:    %s installed\n" "$(ls -1 "$DEST/plugins" 2>/dev/null | wc -l | tr -d ' ')"
fi

echo
echo "The before_run hook will ship $DEST/ into each per-issue VM's ~/.claude/."
