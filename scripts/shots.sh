#!/usr/bin/env bash
# Regenerate every TUI screenshot: build the binary, reconstruct the fixture
# workspace from the committed JSONL, run all VHS tapes → PNGs in vhs/out/.
# One command; read the PNGs to verify visual changes by eye.
set -euo pipefail
cd "$(dirname "$0")/.."
mkdir -p vhs/out
echo "building bb…"; go build -o vhs/out/bb .
echo "reconstructing fixture workspace…"
rm -rf vhs/fixture-work && mkdir -p vhs/fixture-work
(
  cd vhs/fixture-work
  git init -q
  bd init --prefix demo >/dev/null 2>&1
  bd import ../fixture/board.jsonl >/dev/null
  # Create real Dolt history so time-travel has something to scrub: a couple of
  # dated mutations, so a bead that's closed/reprioritized NOW differs from the past.
  bd priority demo-kzr.4 4 >/dev/null 2>&1 || true   # P3 → P4
  bd close demo-kzr.2 --force >/dev/null 2>&1 || bd close demo-kzr.2 >/dev/null 2>&1 || true
)
for tape in vhs/*.tape; do echo "vhs $tape"; vhs "$tape"; done
rm -f vhs/out/_throwaway.gif
echo "done — PNGs in vhs/out/"
