package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/awhitty/bb/internal/bd"
)

func labeledIssues() []bd.Issue {
	return []bd.Issue{
		{ID: "a", Title: "first card", Status: "open", IssueType: "task", Priority: 1, Labels: []string{"backend", "urgent"}},
		{ID: "b", Title: "second card", Status: "open", IssueType: "task", Priority: 2, Labels: []string{"frontend"}},
	}
}

func strippedFrame(t *testing.T, m Model, w, h int) string {
	t.Helper()
	return ansi.Strip(strings.Join(frameLines(t, m, w, h), "\n"))
}

// Label chips render on the FULL-WIDTH list and tree rows (where the flex title
// cell has spare width), and the frame still fits exactly.
func TestLabelChipsOnFullWidthRows(t *testing.T) {
	const w, h = 120, 30

	// List view (b toggles kanban → list).
	m := testModel(t, labeledIssues(), w, h)
	next, _ := m.Update(keyRune('b'))
	m = next.(Model)
	if got := strippedFrame(t, m, w, h); !strings.Contains(got, "backend") || !strings.Contains(got, "frontend") {
		t.Fatalf("list row is missing label chips:\n%s", got)
	}

	// Tree view (5).
	m = testModel(t, labeledIssues(), w, h)
	next, _ = m.Update(keyRune('5'))
	m = next.(Model)
	if got := strippedFrame(t, m, w, h); !strings.Contains(got, "backend") {
		t.Fatalf("tree row is missing label chips:\n%s", got)
	}
}

// The width-critical cards suppress chips: the kanban board (the one card
// renderer that shares formatRow) never shows a label, so a chip can never
// overflow the narrow innerW or annihilate the truncated title.
func TestLabelChipsAbsentOnKanbanCards(t *testing.T) {
	const w, h = 120, 30
	m := testModel(t, labeledIssues(), w, h) // ViewKanban default
	got := strippedFrame(t, m, w, h)
	if strings.Contains(got, "backend") || strings.Contains(got, "frontend") {
		t.Fatalf("kanban card leaked a label chip:\n%s", got)
	}
}

// The DupCount fan-out count surfaces as a compact "+N" marker on a full-width
// row; nothing shows when the count is zero.
func TestLabelChipsDupMarker(t *testing.T) {
	if got := ansi.Strip(labelChips([]string{"backend"}, 2, 40)); !strings.Contains(got, "+2") {
		t.Fatalf("labelChips missing +2 dup marker: %q", got)
	}
	if got := ansi.Strip(labelChips([]string{"backend"}, 0, 40)); strings.Contains(got, "+") {
		t.Fatalf("labelChips should have no dup marker at 0: %q", got)
	}
	if got := labelChips(nil, 0, 40); got != "" {
		t.Fatalf("labelChips(no labels, dup 0) = %q, want empty (byte-parity)", got)
	}
}
