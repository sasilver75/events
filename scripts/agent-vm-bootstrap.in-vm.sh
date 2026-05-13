#!/usr/bin/env bash
# Runs INSIDE the spur-base VM (via `tart exec` from agent-vm-bootstrap.sh).
# Installs the toolchain that every per-issue VM clone inherits.
# Credentials are NOT installed here — they are injected per-run by the harness.

set -euo pipefail

echo "==> spur-base in-VM bootstrap starting"
echo "    user: $(whoami)"
echo "    arch: $(uname -m)"
echo "    macOS: $(sw_vers -productVersion)"

# Homebrew should already be present in cirruslabs xcode images. Make sure
# the shellenv is loaded for non-interactive use.
if [ -x /opt/homebrew/bin/brew ]; then
  eval "$(/opt/homebrew/bin/brew shellenv)"
else
  echo "Homebrew not at /opt/homebrew — aborting."
  exit 1
fi

echo "==> Updating Homebrew"
brew update --quiet

echo "==> Installing toolchain"
# Idempotent: brew install is a no-op if already installed. Don't pass
# --quiet — it suppresses real failure signal on tap-based packages and
# masks misses (we lost supabase in the first run that way).
brew install \
  go \
  gh \
  node \
  colima \
  docker \
  docker-compose \
  jq

# Supabase CLI lives in its own tap; install separately so a tap fetch
# failure doesn't take down the rest of the install batch.
brew install supabase/tap/supabase

echo "==> Installing Claude Code via npm"
# Anthropic publishes Claude Code as @anthropic-ai/claude-code on npm.
# It pulls a self-contained binary on first launch.
npm install -g @anthropic-ai/claude-code

echo "==> Trusting github.com host key"
mkdir -p ~/.ssh
chmod 700 ~/.ssh
ssh-keyscan -t ed25519,rsa github.com 2>/dev/null >> ~/.ssh/known_hosts
sort -u ~/.ssh/known_hosts -o ~/.ssh/known_hosts
chmod 600 ~/.ssh/known_hosts

echo "==> Seeding Claude Code settings (bypassPermissions inside VM only)"
# Use printf, not a heredoc. The bootstrap script pipes this whole file
# into `bash -s` over SSH, and nested heredocs trip the parser there.
mkdir -p "$HOME/.claude"
printf '%s\n' '{"permissions":{"defaultMode":"bypassPermissions"}}' > "$HOME/.claude/settings.json"

echo "==> Versions installed"
printf "  go:       %s\n" "$(go version 2>/dev/null | awk '{print $3}')"
printf "  gh:       %s\n" "$(gh --version 2>/dev/null | head -1)"
printf "  node:     %s\n" "$(node --version 2>/dev/null)"
printf "  npm:      %s\n" "$(npm --version 2>/dev/null)"
printf "  colima:   %s\n" "$(colima version 2>/dev/null | head -1)"
printf "  docker:   %s\n" "$(docker --version 2>/dev/null)"
printf "  supabase: %s\n" "$(supabase --version 2>/dev/null)"
printf "  claude:   %s\n" "$(claude --version 2>/dev/null || echo 'unknown')"
printf "  xcode:    %s\n" "$(xcodebuild -version 2>/dev/null | head -1)"

echo "==> spur-base in-VM bootstrap complete"
