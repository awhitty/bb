#!/usr/bin/env python3
"""Write deterministic-looking, time-relative agent-share fixture data."""

import json
import os
import sys
import time


now = int(time.time())


def timestamp(seconds_ago):
    return time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime(now - seconds_ago))


def excerpt(text, *ids):
    mentions = []
    position = 0
    for issue_id in ids:
        start = text.index(issue_id, position)
        mentions.append(
            {"id": issue_id, "start": start, "end": start + len(issue_id)}
        )
        position = start + len(issue_id)
    return {"text": text, "mentions": mentions}


reporting_push = {
    "id": "s1",
    "name": "reports to review",
    "seq": 1,
    "ts": timestamp(300),
    "spec": {"ids": ["demo-rep.1", "demo-rep.2", "demo-rep.3"]},
}

calendar_push = {
    "id": "s2",
    "name": "Safari fix and blocker",
    "seq": 2,
    "ts": timestamp(240),
    "spec": {
        "mode": "relationship",
        "root": "demo-xy3",
        "remarks": {"demo-xy3": "reproduced on iOS; ready for the guard"},
    },
}

calendar_ambient = {
    "id": "s3",
    "name": "calendar polish",
    "seq": 3,
    "ts": timestamp(120),
    "ids": ["demo-kzr.1", "demo-kzr.2", "demo-xy3", "demo-e5k"],
    "excerpts": [
        excerpt(
            "I finished demo-kzr.1 and demo-kzr.2 for the calendar sharing "
            "refresh, so the import flow works end to end now.",
            "demo-kzr.1",
            "demo-kzr.2",
        ),
        excerpt(
            "Next up is demo-xy3 (the Safari calendar flicker) before I get "
            "back to the list-sorting refactor in demo-e5k.",
            "demo-xy3",
            "demo-e5k",
        ),
    ],
}

store = {
    "channels": [
        {
            "session_id": "sess-reports",
            "title": "scheduled exports",
            "freshness": timestamp(300),
            "state": "live",
            "ambient_beads": {},
            "push_ring": [reporting_push],
        },
        {
            "session_id": "sess-calendar",
            "title": "calendar polish",
            "freshness": timestamp(120),
            "state": "live",
            "ambient_beads": calendar_ambient,
            "push_ring": [calendar_push],
        },
    ],
    "seq": 3,
}


with open(sys.argv[1], "w", encoding="utf-8") as output:
    json.dump(store, output, indent=1)
    output.write("\n")
os.chmod(sys.argv[1], 0o600)
