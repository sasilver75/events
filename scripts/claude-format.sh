#!/usr/bin/env bash
# Claude Code PostToolUse hook: auto-format edited files by extension.
# Wired in .claude/settings.json on Edit|Write. See README.md "Local development".

set -u

input=$(cat)
file_path=$(printf '%s' "$input" | python3 -c 'import json,sys; d=json.load(sys.stdin); print(d.get("tool_input",{}).get("file_path",""))')

if [[ -z "$file_path" || ! -f "$file_path" ]]; then
  exit 0
fi

log() { printf '[claude-format] %s\n' "$*" >&2; }

require() {
  if ! command -v "$1" >/dev/null 2>&1; then
    log "ERROR: '$1' not found on PATH. Install: $2"
    exit 1
  fi
}

case "$file_path" in
  *.go)
    require gofmt "ships with Go (https://go.dev/dl/)"
    require goimports "go install golang.org/x/tools/cmd/goimports@latest (and add \$(go env GOPATH)/bin to PATH)"
    gofmt -w "$file_path" || { log "gofmt failed on $file_path"; exit 1; }
    goimports -w "$file_path" || { log "goimports failed on $file_path"; exit 1; }
    ;;
  *.swift)
    require swift-format "brew install swift-format"
    swift-format format -i "$file_path" || { log "swift-format failed on $file_path"; exit 1; }
    ;;
  *)
    ;;
esac
