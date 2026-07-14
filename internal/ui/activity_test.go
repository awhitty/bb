package ui

import (
	"strings"
	"testing"

	"github.com/awhitty/bb/internal/dolt"
)

func TestEventVerb(t *testing.T) {
	if v, _, d := eventVerb(dolt.Event{Type: "created"}); v != "created" || d != "" {
		t.Fatalf("created: %q %q", v, d)
	}
	if v, _, d := eventVerb(dolt.Event{Type: "updated",
		Old: map[string]any{"priority": float64(2)}, New: map[string]any{"priority": float64(1)}}); v != "reprioritized" || d != "P2 → P1" {
		t.Fatalf("reprioritized: %q %q", v, d)
	}
	if v, _, d := eventVerb(dolt.Event{Type: "updated",
		Old: map[string]any{"status": "open"}, New: map[string]any{"status": "in_progress"}}); v != "status" || d != "open → wip" {
		t.Fatalf("status: %q %q", v, d)
	}
	if v, _, _ := eventVerb(dolt.Event{Type: "updated", New: map[string]any{"status": "closed"}}); v != "closed" {
		t.Fatalf("closed: %q", v)
	}
	if v, _, _ := eventVerb(dolt.Event{Type: "updated", New: map[string]any{"notes": "x"}}); !strings.HasPrefix(v, "edited") {
		t.Fatalf("edited: %q", v)
	}
}

func TestActivityFeedNavAndJump(t *testing.T) {
	m := testModel(t, deepColumn(3), 100, 24)
	m.activityView = true
	m.activityEvents = []dolt.Event{{IssueID: "g-001", Type: "closed"}, {IssueID: "g-000", Type: "created"}}
	if m.activityFocusID() != "g-001" {
		t.Fatalf("focus = %s", m.activityFocusID())
	}
	m = press(t, m, "j")
	if m.activityFocusID() != "g-000" {
		t.Fatal("j should move the feed cursor")
	}
	m = press(t, m, "enter") // jump to g-000 (on the board), exit the feed
	if m.activityView {
		t.Fatal("enter should exit the activity feed")
	}
	if f := m.focusedIssue(); f == nil || f.ID != "g-000" {
		t.Fatalf("enter should select the bead on the board: %+v", f)
	}
}
