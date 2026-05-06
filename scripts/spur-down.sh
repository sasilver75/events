#!/usr/bin/env bash
# Tear down everything spur-up.sh started, in reverse order:
#   1. Kill the Go server (pid in .build/server.pid).
#   2. supabase stop --workdir <ROOT> (worktree-pinned stack).
#
# Leaves .spur-services and supabase/config.toml on disk so the next
# spur-up.sh re-runs cleanly without re-picking an offset.

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PIDFILE="$ROOT/.build/server.pid"

if [ -f "$PIDFILE" ]; then
  pid=$(cat "$PIDFILE")
  if kill -0 "$pid" 2>/dev/null; then
    echo "==> stopping Go server (pid $pid)"
    kill "$pid" 2>/dev/null || true
  fi
  rm -f "$PIDFILE"
fi

echo "==> stopping Supabase stack"
(cd "$ROOT" && supabase stop)
