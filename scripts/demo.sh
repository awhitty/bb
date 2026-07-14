#!/usr/bin/env bash
# Rebuild the deterministic workspace and render the README GIF with Charm VHS.
set -euo pipefail
cd "$(dirname "$0")/.."

for command in bd go python3 vhs; do
  command -v "$command" >/dev/null || {
    echo "missing required command: $command" >&2
    exit 1
  }
done

mkdir -p docs vhs/out
go build -o vhs/out/bb .

rm -rf vhs/fixture-work
mkdir -p vhs/fixture-work
(
  cd vhs/fixture-work
  git init -q
  bd init --prefix demo >/dev/null 2>&1
  bd import ../fixture/board.jsonl >/dev/null
  bd priority demo-kzr.4 4 >/dev/null 2>&1 || true
  bd close demo-kzr.2 --force >/dev/null 2>&1 || bd close demo-kzr.2 >/dev/null 2>&1 || true
)

config_dir="$PWD/vhs/fixture-work/cfg"
mkdir -p "$config_dir"
chmod 700 "$config_dir"
python3 vhs/seed-shares.py "$config_dir/sessions.json"

PATH="$PWD/vhs/out:$PATH" \
  BB_WORKSPACE="$PWD/vhs/fixture-work" \
  BB_CONFIG_DIR="$config_dir" \
  BB_THEME=dark \
  BB_NLQ_URL=http://127.0.0.1:9 \
  vhs vhs/demo.tape

echo "rendered docs/demo.gif"
