#!/usr/bin/env bash
# Pin the simulator's GPS to (SPUR_SIM_LAT, SPUR_SIM_LON) from .spur-sim.
#
# Why this exists:
#   The iOS app's browse query is GPS-driven (sends near=<lat>,<lon> from
#   CoreLocation). The map's region/title is cosmetic and doesn't constrain
#   the query. iOS sims default GPS to Apple Park (SF Bay) and persist that
#   across reboots — so dev/test fixtures seeded near the canonical LA dev
#   area never appear in browse, and you burn real time on "no events nearby".
#
#   .spur-sim already pins UDID, name, and derived-data path per worktree;
#   this extends the same pin with location so the GPS lines up with the
#   data we seed. Idempotent. Safe to re-run after any sim reboot.
#
# Boots the sim if necessary so the GPS is in place BEFORE the iOS app
# launches. The app caches its first CoreLocation fix at process start,
# so setting GPS after launch (with the app already running) leaves the
# cached SF reading in place and browse queries keep going to SF until
# the app is killed and relaunched. Run this script BEFORE
# `XcodeBuildMCP build_run_sim` (or any iOS launch) to keep the order
# right.
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PIN="$ROOT/.spur-sim"
if [[ ! -r "$PIN" ]]; then
    echo "$PIN missing — create it before pinning the sim location" >&2
    exit 2
fi
# shellcheck disable=SC1090
source "$PIN"
: "${SPUR_SIM_UDID:?SPUR_SIM_UDID missing in .spur-sim}"
: "${SPUR_SIM_LAT:?SPUR_SIM_LAT missing in .spur-sim (LA default: SPUR_SIM_LAT=34.0522 SPUR_SIM_LON=-118.2437)}"
: "${SPUR_SIM_LON:?SPUR_SIM_LON missing in .spur-sim}"

# Boot if needed. simctl boot errors loudly if the device is already
# Booted; treat that as success.
state=$(xcrun simctl list devices | grep "$SPUR_SIM_UDID" | sed -E 's/.*\(([A-Za-z]+)\)[^(]*$/\1/')
if [[ "$state" != "Booted" ]]; then
    echo "==> booting sim $SPUR_SIM_UDID (was: ${state:-unknown})"
    xcrun simctl boot "$SPUR_SIM_UDID" 2>&1 | grep -v "Unable to boot device in current state: Booted" || true
fi

xcrun simctl location "$SPUR_SIM_UDID" set "$SPUR_SIM_LAT,$SPUR_SIM_LON"
echo "==> sim $SPUR_SIM_UDID GPS pinned to $SPUR_SIM_LAT,$SPUR_SIM_LON"
