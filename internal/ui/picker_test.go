package ui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/awhitty/bb/internal/bd"
	"github.com/awhitty/bb/internal/rollup"
)

// keyRune is the shared shorthand for a single-rune key message (used across the
// control tests to drive open/highlight/commit).
func keyRune(r rune) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}} }

// The command palette is gone: p no longer opens anything, so it never opens a
// control, never swallows input, and leaves the current view untouched. This is
// the regression guard that the palette (and its p binding) stayed deleted.
func TestPaletteKeyRetired(t *testing.T) {
	const w, h = 120, 30
	m := testModel(t, deepColumn(6), w, h) // ViewKanban

	before := m.view
	next, _ := m.Update(keyRune('p'))
	m = next.(Model)
	if m.control.open {
		t.Fatal("p opened a control — the palette should be retired")
	}
	if m.view != before {
		t.Fatalf("p changed the view (%v → %v) — it should be a no-op now", before, m.view)
	}
}

// The grouper control drives the board's facet slot, then a legacy mode key clears
// the override — the open/highlight/commit path plus the coherence the old palette
// test guarded. Grouping the board by label (a facet with no legacy Mode) is only
// reachable through the control; it sets boardFacet and re-buckets the columns,
// and a mode key (1 = status) returns to the byte-parity mode-driven board.
func TestGrouperControlGroupsBoardByLabelThenModeKeyClears(t *testing.T) {
	const w, h = 120, 30
	issues := []bd.Issue{
		{ID: "a", Title: "one", Status: "open", IssueType: "task", Labels: []string{"backend"}},
		{ID: "b", Title: "two", Status: "open", IssueType: "task", Labels: []string{"frontend"}},
	}
	m := testModel(t, issues, w, h) // ViewKanban

	// Open the grouper (M), walk to "label", commit (enter).
	m = press(t, m, "M")
	if !m.control.open {
		t.Fatal("M did not open the grouper control")
	}
	m = focusGroup(t, m, "label")
	if m.boardFacet != rollup.FacetLabel {
		t.Fatalf("preview did not set boardFacet: %q, want label", m.boardFacet)
	}
	m = press(t, m, "enter")
	if m.control.open {
		t.Fatal("enter did not close the grouper")
	}
	if m.boardFacet != rollup.FacetLabel {
		t.Fatalf("commit lost: boardFacet = %q, want label", m.boardFacet)
	}
	keys := map[string]bool{}
	for _, c := range m.columns {
		keys[c.Key] = true
	}
	if !keys["backend"] || !keys["frontend"] {
		t.Fatalf("board columns = %v, want label buckets backend+frontend", keys)
	}

	// A legacy mode key (1 = status) clears the facet override and returns to the
	// mode-driven board — the byte-parity path.
	next, _ := m.Update(keyRune('1'))
	m = next.(Model)
	if m.boardFacet != "" {
		t.Fatalf("boardFacet = %q after mode key, want cleared", m.boardFacet)
	}
}
