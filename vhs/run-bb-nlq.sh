#!/usr/bin/env bash
# Like run-bb.sh but WITHOUT the unreachable NLQ url, so zero-config discovery
# finds the local model server (omlx) and the `/` NL-compile flow works. Used
# only by the nlq screenshot tape.
here="$(cd "$(dirname "$0")" && pwd)"
export BB_WORKSPACE="$here/fixture-work"
export BB_THEME=dark
exec "$here/out/bb" "$@"
