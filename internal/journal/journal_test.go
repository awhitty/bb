package journal

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func mustParse(t *testing.T, ts string) time.Time {
	t.Helper()
	tm, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		t.Fatalf("parse %q: %v", ts, err)
	}
	return tm
}

// The parser keeps only STATUS field_changes, takes the LATEST per issue, and
// ignores priority/assignee rows (which are not status transitions).
func TestParseStatusOnlyLastWins(t *testing.T) {
	lines := strings.Join([]string{
		`{"created_at":"2026-07-07T18:50:22Z","issue_id":"a","extra":{"field":"status","new_value":"in_progress"}}`,
		`{"created_at":"2026-07-08T09:00:00Z","issue_id":"a","extra":{"field":"priority","new_value":"0"}}`, // ignored
		`{"created_at":"2026-07-09T12:00:00Z","issue_id":"a","extra":{"field":"status","new_value":"needs_review"}}`,
		`{"created_at":"2026-07-08T00:00:00Z","issue_id":"b","extra":{"field":"status","new_value":"closed"}}`,
		`{"created_at":"2026-07-10T00:00:00Z","issue_id":"c","extra":{"field":"assignee","new_value":"x"}}`, // ignored → c absent
	}, "\n")
	got := parse(strings.NewReader(lines))

	if len(got) != 2 {
		t.Fatalf("got %d ids, want 2 (a,b): %v", len(got), got)
	}
	if !got["a"].Equal(mustParse(t, "2026-07-09T12:00:00Z")) {
		t.Errorf("a = %v, want the LATEST status row (2026-07-09), not the priority row", got["a"])
	}
	if !got["b"].Equal(mustParse(t, "2026-07-08T00:00:00Z")) {
		t.Errorf("b = %v", got["b"])
	}
	if _, ok := got["c"]; ok {
		t.Errorf("c has only an assignee change and must be absent")
	}
}

// Out-of-order lines never regress a bead's timestamp: last-wins is by TIME,
// not by file position.
func TestParseOutOfOrderKeepsLatest(t *testing.T) {
	lines := strings.Join([]string{
		`{"created_at":"2026-07-09T12:00:00Z","issue_id":"a","extra":{"field":"status","new_value":"closed"}}`,
		`{"created_at":"2026-07-07T00:00:00Z","issue_id":"a","extra":{"field":"status","new_value":"open"}}`,
	}, "\n")
	got := parse(strings.NewReader(lines))
	if !got["a"].Equal(mustParse(t, "2026-07-09T12:00:00Z")) {
		t.Errorf("a = %v, want the later timestamp regardless of line order", got["a"])
	}
}

// A malformed or blank line is skipped, never fatal to the whole parse.
func TestParseSkipsBadLines(t *testing.T) {
	lines := strings.Join([]string{
		``,
		`not json`,
		`{"created_at":"nope","issue_id":"a","extra":{"field":"status"}}`, // unparseable time
		`{"created_at":"2026-07-09T12:00:00Z","issue_id":"a","extra":{"field":"status","new_value":"open"}}`,
	}, "\n")
	got := parse(strings.NewReader(lines))
	if len(got) != 1 || !got["a"].Equal(mustParse(t, "2026-07-09T12:00:00Z")) {
		t.Errorf("got %v, want only the one valid row for a", got)
	}
}

// A missing file is best-effort: an empty map, never an error.
func TestLastStatusChangeMissingFile(t *testing.T) {
	got := LastStatusChange(filepath.Join(t.TempDir(), "does-not-exist.jsonl"))
	if len(got) != 0 {
		t.Errorf("missing file: got %v, want empty map", got)
	}
}
