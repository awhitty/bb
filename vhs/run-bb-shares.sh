#!/usr/bin/env bash
# Seeds the isolated session-channel store with ambient mentions and published
# views, then launches the fixture TUI so `@` opens the live-sessions browser.
# BB_CONFIG_DIR isolation keeps this off the user's real shares store.
set -euo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
export BB_WORKSPACE="$here/fixture-work"
export BB_THEME=dark
export BB_NLQ_URL="http://127.0.0.1:9"
CFG="$here/fixture-work/cfg"
mkdir -p "$CFG"
export BB_CONFIG_DIR="$CFG"

python3 "$here/seed-shares.py" "$CFG/sessions.json"

exec "$here/out/bb"
