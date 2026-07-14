#!/usr/bin/env bash
# Launches the fixture TUI on an alternate MCP port and, after a delay, drives
# an emphasize() call into it over MCP (the real agent path) so the VHS shot
# shows the emphasis decoration layer. EMPH_JSON + EMPH_DELAY are set per-tape.
here="$(cd "$(dirname "$0")" && pwd)"
export BB_WORKSPACE="$here/fixture-work"
export BB_THEME=dark
export BB_NLQ_URL="http://127.0.0.1:9"
export BB_MCP_PORT=7399
# Isolate the config dir (token copied from the real one) so publishing over MCP
# does NOT pollute the user's real agent-shares store, and clear shares each run.
CFG="$here/fixture-work/cfg"
mkdir -p "$CFG"
[ -f "$CFG/config.toml" ] || cp "$HOME/.config/bb/config.toml" "$CFG/" 2>/dev/null || true
rm -f "$CFG/shares.json"
export BB_CONFIG_DIR="$CFG"
TOKEN="$(sed -n 's/.*token = "\(.*\)".*/\1/p' "$CFG/config.toml")"
(
  sleep "${EMPH_DELAY:-3.5}"
  curl -s -X POST "http://127.0.0.1:7399${EMPH_PATH:-/mcp}" \
    -H "Authorization: Bearer $TOKEN" \
    -H "Content-Type: application/json" \
    -H "Accept: application/json, text/event-stream" \
    -d "$EMPH_JSON" >/dev/null 2>&1
) &
exec "$here/out/bb"
