#!/usr/bin/env bash
# Boot the worktree's local dev stack end-to-end, in the right order:
#
#   1. Warn about orphan Supabase stacks left behind by deleted worktrees
#      and offer to clean them up. Orphans silently starve Postgres in the
#      active worktree (each runs ~13 containers; supabase_analytics in
#      particular pegs a CPU core even when idle).
#   2. Run scripts/spur-services-init.sh if the worktree has no .spur-services
#      pin yet — assigns SPUR_OFFSET, renders supabase/config.toml, renders
#      ios/Spur/Local.generated.xcconfig.
#   3. supabase start (idempotent — skipped if the stack is already up).
#   4. Apply migrations + seed curated events.
#   5. Start the Go server in the background, writing pid → .build/server.pid
#      and stdout/stderr → .build/server.log.
#
# Idempotent. Re-run any time. Pass --restart-server to bounce the Go
# server only (e.g. after editing server/...).

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
RESTART_SERVER=0
for arg in "$@"; do
  case "$arg" in
    --restart-server) RESTART_SERVER=1 ;;
    *) echo "unknown arg: $arg" >&2 ; exit 2 ;;
  esac
done

# ---------- 1. orphan Supabase stack detection ----------

echo "==> checking for orphan Supabase stacks"

# Live worktree directory names from the git porcelain.
live_worktrees=$(git -C "$ROOT" worktree list --porcelain \
  | awk '/^worktree / {print $2}' \
  | xargs -n1 basename)

# Project ids that docker is currently running a Supabase DB container for.
# `grep` exits 1 when nothing matches; under `set -o pipefail` that kills
# the whole script before the orphan-check finishes. The empty-input case
# is the common one (no Supabase stacks running yet), so absorb the
# non-match exit explicitly.
docker_projects=$(docker ps --format '{{.Names}}' \
  | { grep -oE '^supabase_db_.+$' || true; } \
  | sed 's/^supabase_db_//' \
  | sort -u)

orphans=()
for proj in $docker_projects; do
  if ! grep -qx "$proj" <<<"$live_worktrees"; then
    orphans+=("$proj")
  fi
done

if [ "${#orphans[@]}" -gt 0 ]; then
  echo "  ${#orphans[@]} orphan stack(s) found: ${orphans[*]}"
  echo "  these eat CPU and starve Postgres in the active worktree."
  read -r -p "  remove orphan containers now? [y/N] " yn
  if [[ "$yn" =~ ^[Yy]$ ]]; then
    for proj in "${orphans[@]}"; do
      names=$(docker ps -a --format '{{.Names}}' | grep -E "_${proj}\$" || true)
      for name in $names; do
        docker rm -f "$name" >/dev/null
      done
      echo "  removed $proj"
    done
  fi
else
  echo "  none."
fi

# ---------- 2. services init ----------

if [ ! -f "$ROOT/.spur-services" ]; then
  echo "==> running spur-services-init.sh (first-time worktree setup)"
  "$ROOT/scripts/spur-services-init.sh"
fi
# shellcheck source=/dev/null
source "$ROOT/.spur-services"

# ---------- 3. supabase start ----------

if docker ps --format '{{.Names}}' | grep -qx "supabase_db_${SPUR_PROJECT_ID}"; then
  echo "==> Supabase stack already up for $SPUR_PROJECT_ID"
else
  echo "==> starting Supabase stack ($SPUR_PROJECT_ID)"
  (cd "$ROOT" && supabase start)
fi

# ---------- 4. migrate + seed ----------

DB_URL="${SPUR_SUPABASE_DB_URL}?sslmode=disable"

echo "==> migrate-up"
DATABASE_URL="$DB_URL" make -C "$ROOT/server" migrate-up >/dev/null

# `supabase status -o env` exposes ANON_KEY / SERVICE_ROLE_KEY; the .spur-services
# pin intentionally omits them (they're shared across local stacks and trivially
# refetched).
eval "$(supabase status -o env --workdir "$ROOT" 2>/dev/null)"

echo "==> seed"
DATABASE_URL="$DB_URL" \
  SUPABASE_URL="$SPUR_SUPABASE_API_URL" \
  SUPABASE_SERVICE_ROLE_KEY="$SERVICE_ROLE_KEY" \
  make -C "$ROOT/server" seed >/dev/null

# ---------- 5. Go server ----------

mkdir -p "$ROOT/.build"
PIDFILE="$ROOT/.build/server.pid"
LOGFILE="$ROOT/.build/server.log"

server_running() {
  [ -f "$PIDFILE" ] && kill -0 "$(cat "$PIDFILE" 2>/dev/null)" 2>/dev/null
}

if server_running && [ "$RESTART_SERVER" -eq 0 ]; then
  echo "==> Go server already running (pid $(cat "$PIDFILE"), logs: $LOGFILE)"
else
  if server_running; then
    echo "==> stopping previous Go server (pid $(cat "$PIDFILE"))"
    kill "$(cat "$PIDFILE")" 2>/dev/null || true
    rm -f "$PIDFILE"
    sleep 1
  fi
  # If something else is on the worktree's pinned port (forgotten `go run`,
  # sibling worktree without pin discipline, etc.), bail loudly rather than
  # launch a server that will silently crash on bind.
  if foreign_pid=$(lsof -t -i :"$SPUR_SERVER_PORT" 2>/dev/null); then
    echo "  port $SPUR_SERVER_PORT is held by pid $foreign_pid — kill it and retry:" >&2
    echo "    kill $foreign_pid" >&2
    exit 1
  fi
  echo "==> starting Go server (logs: $LOGFILE)"
  cd "$ROOT/server"
  DATABASE_URL="$DB_URL" \
    SUPABASE_URL="$SPUR_SUPABASE_API_URL" \
    PORT="$SPUR_SERVER_PORT" \
    nohup go run ./cmd/server >"$LOGFILE" 2>&1 &
  echo $! > "$PIDFILE"
  cd - >/dev/null

  for i in $(seq 1 30); do
    sleep 1
    if curl -sf "$SPUR_SERVER_URL/healthz" >/dev/null 2>&1; then
      break
    fi
    if [ "$i" -eq 30 ]; then
      echo "  server didn't come up in 30s — see $LOGFILE" >&2
      exit 1
    fi
  done
fi

cat <<EOF

==> ready

  Supabase API:     $SPUR_SUPABASE_API_URL
  Supabase Studio:  $SPUR_SUPABASE_STUDIO_URL
  Go server:        $SPUR_SERVER_URL
                    pid: $(cat "$PIDFILE")
                    logs: $LOGFILE

  Build & run iOS:  XcodeBuildMCP build_run_sim, or
                    xcodebuild -derivedDataPath ./.build/derived-data ...

  Tear down:        scripts/spur-down.sh
EOF
