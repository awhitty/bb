// Package journal reads the beads interactions journal
// (.beads/interactions.jsonl) — the append-only event stream bd writes for
// every field change. Its one job here is time-in-status: the timestamp of a
// bead's LAST status transition, which (against now) is how long the bead has
// sat cold in its current status.
package journal

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"time"
)

// row is one journal line. Only status field_changes matter for status-age;
// priority/assignee changes are ignored (they are not status transitions, so
// counting them would reset a bead's status-age on an unrelated edit).
type row struct {
	CreatedAt string `json:"created_at"`
	IssueID   string `json:"issue_id"`
	Extra     struct {
		Field string `json:"field"`
	} `json:"extra"`
}

// LastStatusChange maps issue id → the time of its most recent status
// transition, read from the journal at path. Last-wins by timestamp, so a
// re-appended or out-of-order line never regresses a bead's age. It is
// best-effort: a missing or unreadable file yields an empty map (a bead with
// no transition row simply falls back to created_at at the call site), and a
// malformed line is skipped rather than failing the whole parse.
func LastStatusChange(path string) map[string]time.Time {
	f, err := os.Open(path)
	if err != nil {
		return map[string]time.Time{}
	}
	defer f.Close()
	return parse(f)
}

func parse(r io.Reader) map[string]time.Time {
	out := map[string]time.Time{}
	sc := bufio.NewScanner(r)
	// A reason field can make a line long; give the scanner generous headroom
	// so an oversized line is skipped-if-anything, never a hard error.
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var rw row
		if json.Unmarshal(line, &rw) != nil {
			continue
		}
		if rw.Extra.Field != "status" || rw.IssueID == "" {
			continue
		}
		t, err := time.Parse(time.RFC3339, rw.CreatedAt)
		if err != nil {
			continue
		}
		if prev, ok := out[rw.IssueID]; !ok || t.After(prev) {
			out[rw.IssueID] = t
		}
	}
	return out
}
