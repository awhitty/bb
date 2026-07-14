#!/usr/bin/env bash
# Seeds the ISOLATED agent-shares store with a few entries — a mentioned batch
# that carries CONTEXT (a conversation name + excerpts with inline bead
# chiclets), a filter view, and an ids view — then launches the fixture TUI, so
# `@` opens the master-detail browser. The mentioned section is headed by the
# conversation name, its body is the excerpt prose with each bead id rendered as
# an inline chiclet, and the preview follows the focused chiclet as you tab.
# A legacy ids-only mentioned batch is included to prove backward-compat.
# BB_CONFIG_DIR isolation keeps this off the user's real shares store.
set -euo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
export BB_WORKSPACE="$here/fixture-work"
export BB_THEME=dark
export BB_NLQ_URL="http://127.0.0.1:9"
CFG="$here/fixture-work/cfg"
mkdir -p "$CFG"
[ -f "$CFG/config.toml" ] || cp "$HOME/.config/bb/config.toml" "$CFG/" 2>/dev/null || true
export BB_CONFIG_DIR="$CFG"

# Build shares.json in python so the excerpt Mention spans are exact byte
# offsets into each excerpt's text (the TUI slices [start,end) to place the
# chiclet). Newest last in the file (the browser lists newest first).
python3 - "$CFG/shares.json" <<'PY'
import json, sys, time
now = int(time.time())
def ts(ago): return time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime(now-ago))

def excerpt(text, *ids):
    # mark each full id where it appears in text, in reading order
    mentions, pos = [], 0
    for i in ids:
        s = text.index(i, pos)
        mentions.append({"id": i, "start": s, "end": s+len(i)})
        pos = s+len(i)
    return {"text": text, "mentions": mentions}

mentioned_rich = {
    "id": "s0", "type": "mentioned",
    "name": "rework the mentioned entries so I can see context",
    "ts": ts(120), "session_id": "804f231e-f820-4c94",
    "spec": {"ids": ["demo-kzr.1", "demo-kzr.2", "demo-xy3", "demo-e5k"]},
    "excerpts": [
        excerpt("I finished demo-kzr.1 and demo-kzr.2 for the calendar sharing "
                "refresh, so the import flow works end to end now.",
                "demo-kzr.1", "demo-kzr.2"),
        excerpt("Next up is demo-xy3 (the Safari calendar flicker) before I get "
                "back to the list-sorting refactor in demo-e5k.",
                "demo-xy3", "demo-e5k"),
    ],
}
legacy_mentioned = {
    "id": "s1", "type": "mentioned", "name": "beads I mentioned last turn",
    "ts": ts(7200), "session_id": "sess-a1b2",
    "spec": {"ids": ["demo-aep", "demo-wjc"]},
}
view_feature = {
    "id": "s2", "type": "view", "name": "features to review",
    "ts": ts(1800), "session_id": "sess-a1b2",
    "spec": {"mode": "type", "query": "type=feature"},
}
view_ids = {
    "id": "s3", "type": "view", "name": "reports to review",
    "ts": ts(300), "session_id": "sess-c3d4",
    "spec": {"ids": ["demo-kzr.2", "demo-4s9", "demo-j1y"]},
}
# file order = oldest first; browser shows newest first.
json.dump([legacy_mentioned, view_feature, view_ids, mentioned_rich],
          open(sys.argv[1], "w"), indent=1)
PY

exec "$here/out/bb"
