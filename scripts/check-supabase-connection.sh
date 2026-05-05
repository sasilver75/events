#!/usr/bin/env bash
# Verifies the local Supabase stack is reachable.
#
# Checks (in order):
#   1. supabase CLI is installed
#   2. Postgres is accepting connections on 127.0.0.1:54322
#   3. Studio is responding on http://127.0.0.1:54323
#   4. API gateway is responding on http://127.0.0.1:54321
#
# Exit 0 on success, 1 on any failure. Prints a one-line status per check.

set -u

PG_HOST="127.0.0.1"
PG_PORT="54322"
STUDIO_URL="http://127.0.0.1:54323"
API_URL="http://127.0.0.1:54321"

ok()   { printf "  ok   %s\n" "$1"; }
fail() { printf "  FAIL %s — %s\n" "$1" "$2"; }

failures=0

echo "Supabase local stack check"

if ! command -v supabase >/dev/null 2>&1; then
  fail "supabase CLI" "not installed (brew install supabase/tap/supabase)"
  failures=$((failures + 1))
else
  ok "supabase CLI ($(supabase --version 2>/dev/null | head -1))"
fi

if (echo > "/dev/tcp/${PG_HOST}/${PG_PORT}") >/dev/null 2>&1; then
  ok "Postgres ${PG_HOST}:${PG_PORT}"
else
  fail "Postgres ${PG_HOST}:${PG_PORT}" "connection refused (run: supabase start)"
  failures=$((failures + 1))
fi

if curl --silent --fail --max-time 2 "${STUDIO_URL}" >/dev/null 2>&1; then
  ok "Studio ${STUDIO_URL}"
else
  fail "Studio ${STUDIO_URL}" "no response"
  failures=$((failures + 1))
fi

if curl --silent --fail --max-time 2 "${API_URL}/rest/v1/" \
     -H "apikey: ${SUPABASE_ANON_KEY:-}" >/dev/null 2>&1 \
   || curl --silent --max-time 2 "${API_URL}/rest/v1/" >/dev/null 2>&1; then
  ok "API ${API_URL}"
else
  fail "API ${API_URL}" "no response"
  failures=$((failures + 1))
fi

if [ "${failures}" -eq 0 ]; then
  echo "All checks passed."
  exit 0
fi

echo "${failures} check(s) failed."
exit 1
