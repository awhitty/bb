#!/usr/bin/env bash
# Launches bb against the deterministic VHS fixture. Used by the .tape
# scripts; the binary + fixture workspace are (re)built by scripts/shots.sh.
here="$(cd "$(dirname "$0")" && pwd)"
export BB_WORKSPACE="$here/fixture-work"
export BB_THEME="${BB_THEME:-dark}"
export BB_NLQ_URL="http://127.0.0.1:9" # unreachable → no model autostart in the harness
exec "$here/out/bb" "$@"
