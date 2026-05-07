#!/usr/bin/env bash
# Per-worktree port assignment for host-level services (Supabase today,
# Go server / Redis / etc. later).
#
# Each worktree is assigned a single SPUR_OFFSET (lowest free multiple of 10).
# Every networked service derives its ports from that offset: Supabase API
# at 54321+offset, DB at 54322+offset, edge-runtime inspector at 8083+offset,
# and so on. Future services slot in by reserving a port within the offset
# window, templating their config, and adding their resolved URL to the pin.
#
# This script:
#   1. Reads sibling worktrees' .spur-services pins to find taken offsets.
#   2. Probes for already-bound ports (catches legacy stacks brought up
#      before this convention existed).
#   3. Picks the lowest free offset.
#   4. Renders supabase/config.toml from supabase/config.toml.template with
#      port placeholders substituted and project_id set to the worktree's
#      directory name (so docker container names are also unique).
#   5. Renders ios/Spur/Local.generated.xcconfig from its template so the
#      iOS build picks up the worktree's Supabase API port.
#   6. Writes .spur-services with the resolved offset, project_id, and
#      per-service URLs (sourceable as env).
#
# Idempotent — re-running is a no-op when .spur-services already exists.
# Pass --force to regenerate (e.g. after manually deleting the pin).
#
# See CLAUDE.md "Multi-session coordination".

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PIN="$ROOT/.spur-services"
TEMPLATE="$ROOT/supabase/config.toml.template"
CONFIG="$ROOT/supabase/config.toml"
IOS_TEMPLATE="$ROOT/ios/Spur/Local.generated.xcconfig.template"
IOS_CONFIG="$ROOT/ios/Spur/Local.generated.xcconfig"

FORCE=0
for arg in "$@"; do
  case "$arg" in
    --force) FORCE=1 ;;
    *) echo "unknown arg: $arg" >&2 ; exit 2 ;;
  esac
done

if [ -f "$PIN" ] && [ "$FORCE" -eq 0 ]; then
  echo "$PIN already exists. Pass --force to regenerate." >&2
  exit 0
fi

if [ ! -f "$TEMPLATE" ]; then
  echo "missing template: $TEMPLATE" >&2
  exit 1
fi

if [ ! -f "$IOS_TEMPLATE" ]; then
  echo "missing template: $IOS_TEMPLATE" >&2
  exit 1
fi

# A worktree may live either at the main repo root (.../events) or under
# .claude/worktrees/<name>. Discover all .spur-services pins under the same
# .claude/worktrees/ directory and the main repo root, and collect taken
# offsets so we don't pick a duplicate.
WORKTREES_DIR=""
case "$ROOT" in
  */.claude/worktrees/*) WORKTREES_DIR="$(cd "$ROOT/.." && pwd)" ;;
esac

declare -a TAKEN_OFFSETS=()
read_offset() {
  local pin="$1"
  if [ -f "$pin" ]; then
    awk -F= '/^SPUR_OFFSET=/ {print $2}' "$pin" | tr -d '"'
  fi
}

if [ -n "$WORKTREES_DIR" ]; then
  shopt -s nullglob
  for sibling in "$WORKTREES_DIR"/*/.spur-services; do
    if [ "$sibling" = "$PIN" ]; then continue; fi
    off="$(read_offset "$sibling")"
    if [ -n "$off" ]; then TAKEN_OFFSETS+=("$off"); fi
  done
  shopt -u nullglob
  # Main repo root also participates if it has a pin.
  MAIN_PIN="$WORKTREES_DIR/../../.spur-services"
  off="$(read_offset "$MAIN_PIN")"
  if [ -n "$off" ]; then TAKEN_OFFSETS+=("$off"); fi
fi

port_in_use() {
  # Return 0 if any process is listening on $1.
  lsof -nP -iTCP:"$1" -sTCP:LISTEN >/dev/null 2>&1
}

# Preserve an existing pin's offset on --force regen so we don't reassign
# offsets to worktrees that already have a stack on disk. Only pick a fresh
# offset if no pin exists yet.
EXISTING_OFFSET="$(read_offset "$PIN")"
if [ -n "$EXISTING_OFFSET" ]; then
  OFFSET="$EXISTING_OFFSET"
else
  # An offset is taken if (a) a sibling pin claims it, or (b) any of its
  # load-bearing ports is already bound — covers legacy stacks brought up
  # before this convention existed (no pin file) and any non-Supabase service
  # that grabbed the port.
  OFFSET=0
  while :; do
    taken=0
    for o in ${TAKEN_OFFSETS[@]+"${TAKEN_OFFSETS[@]}"}; do
      if [ "$o" = "$OFFSET" ]; then taken=1; break; fi
    done
    if [ "$taken" -eq 0 ]; then
      if port_in_use $((54321 + OFFSET)) || port_in_use $((54322 + OFFSET)) || port_in_use $((8080 + OFFSET)); then
        taken=1
      fi
    fi
    if [ "$taken" -eq 0 ]; then break; fi
    OFFSET=$((OFFSET + 10))
    if [ "$OFFSET" -gt 1000 ]; then
      echo "could not find a free offset under +1000" >&2
      exit 1
    fi
  done
fi

PROJECT_ID="$(basename "$ROOT")"

API_PORT=$((54321 + OFFSET))
DB_PORT=$((54322 + OFFSET))
DB_SHADOW_PORT=$((54320 + OFFSET))
DB_POOLER_PORT=$((54329 + OFFSET))
STUDIO_PORT=$((54323 + OFFSET))
INBUCKET_PORT=$((54324 + OFFSET))
ANALYTICS_PORT=$((54327 + OFFSET))
EDGE_INSPECTOR_PORT=$((8083 + OFFSET))
SERVER_PORT=$((8080 + OFFSET))

sed \
  -e "s/__SPUR_PROJECT_ID__/$PROJECT_ID/g" \
  -e "s/__SPUR_API_PORT__/$API_PORT/g" \
  -e "s/__SPUR_DB_PORT__/$DB_PORT/g" \
  -e "s/__SPUR_DB_SHADOW_PORT__/$DB_SHADOW_PORT/g" \
  -e "s/__SPUR_DB_POOLER_PORT__/$DB_POOLER_PORT/g" \
  -e "s/__SPUR_STUDIO_PORT__/$STUDIO_PORT/g" \
  -e "s/__SPUR_INBUCKET_PORT__/$INBUCKET_PORT/g" \
  -e "s/__SPUR_ANALYTICS_PORT__/$ANALYTICS_PORT/g" \
  -e "s/__SPUR_EDGE_INSPECTOR_PORT__/$EDGE_INSPECTOR_PORT/g" \
  "$TEMPLATE" > "$CONFIG"

sed \
  -e "s/__SPUR_API_PORT__/$API_PORT/g" \
  -e "s/__SPUR_SERVER_PORT__/$SERVER_PORT/g" \
  "$IOS_TEMPLATE" > "$IOS_CONFIG"

cat > "$PIN" <<EOF
# Per-worktree services pin. Source this file (or read it programmatically)
# to point shells, tests, and Make targets at the worktree's isolated
# instances. Generated by scripts/spur-services-init.sh — re-run with --force
# to regenerate.
SPUR_OFFSET=$OFFSET
SPUR_PROJECT_ID=$PROJECT_ID

# Supabase (offset window +0..+9 in the 543xx range, plus 8083+offset for
# the edge-runtime inspector).
SPUR_SUPABASE_API_URL=http://127.0.0.1:$API_PORT
SPUR_SUPABASE_DB_URL=postgresql://postgres:postgres@127.0.0.1:$DB_PORT/postgres
SPUR_SUPABASE_STUDIO_URL=http://127.0.0.1:$STUDIO_PORT
SPUR_SUPABASE_INBUCKET_URL=http://127.0.0.1:$INBUCKET_PORT

# Go server (8080+offset).
SPUR_SERVER_PORT=$SERVER_PORT
SPUR_SERVER_URL=http://127.0.0.1:$SERVER_PORT
EOF

echo "Assigned offset $OFFSET to project_id=$PROJECT_ID"
echo "  Supabase API:      http://127.0.0.1:$API_PORT"
echo "  Supabase DB:       postgresql://postgres:postgres@127.0.0.1:$DB_PORT/postgres"
echo "  Supabase Studio:   http://127.0.0.1:$STUDIO_PORT"
echo "  Supabase Inbucket: http://127.0.0.1:$INBUCKET_PORT"
echo "  Go server:         http://127.0.0.1:$SERVER_PORT"
echo "  iOS Local.generated.xcconfig: SUPABASE_URL → http://127.0.0.1:$API_PORT, SERVER_URL → http://127.0.0.1:$SERVER_PORT"
echo
echo "Next: cd $ROOT && supabase start"
