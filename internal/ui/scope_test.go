package ui

import (
	"sort"
	"strings"
	"testing"

	"github.com/awhitty/bb/internal/bd"
	"github.com/awhitty/bb/internal/rollup"
)

// idSet reduces a list of issues to the sorted set of ids it contains — the
// scoped neighborhood in a comparable form (ignoring order and grouping).
func idSet(issues []bd.Issue) []string {
	out := make([]string, 0, len(issues))
	for _, is := range issues {
		out = append(out, is.ID)
	}
	sort.Strings(out)
	return out
}

// boardIDs is every id shown across the built columns (the union of what the
// board/list renders), sorted — so a group/lane split doesn't change the set.
func boardIDs(m Model) []string {
	var out []string
	for _, c := range m.columns {
		for _, is := range c.Issues {
			out = append(out, is.ID)
		}
	}
	sort.Strings(out)
	return out
}

func eqIDs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestScopeIsModeIndependent proves relationship focus is a mode-independent
// scope: entering it (R) narrows the whole board to the bead's neighborhood;
// cycling to another mode (v) and applying a grouper (M) keep that same
// neighborhood; and esc restores the exact pre-scope view, mode, and cursor.
func TestScopeIsModeIndependent(t *testing.T) {
	const w, h = 140, 40
	m := testModel(t, swimFixture(), w, h) // ViewKanban, grouped by status, all 8 beads

	// g-R's neighborhood: itself + sub-issues (g-c1/g-c2) + blocker (g-B1) +
	// dependent (g-D1) + siblings (g-S1/g-S2). The epic parent g-E is NOT in it.
	neighborhood := []string{"g-B1", "g-D1", "g-R", "g-S1", "g-S2", "g-c1", "g-c2"}

	// Precondition: the whole board (no scope).
	if m.scopeRoot != "" {
		t.Fatalf("precondition: scopeRoot=%q, want unscoped", m.scopeRoot)
	}
	if got := idSet(m.visibleIssues()); len(got) != 8 {
		t.Fatalf("precondition: %d visible, want the whole board (8): %v", len(got), got)
	}

	// Focus g-R, then R enters its relationship focus.
	m.jumpTo("g-R")
	m = press(t, m, "R")
	if m.scopeRoot != "g-R" {
		t.Fatalf("R should scope to g-R, scopeRoot=%q", m.scopeRoot)
	}
	if m.view != ViewSwim {
		t.Fatalf("R should present the scoped root as the swimlane, view=%v", m.view)
	}
	if got := idSet(m.visibleIssues()); !eqIDs(got, neighborhood) {
		t.Fatalf("scoped set = %v, want the neighborhood %v", got, neighborhood)
	}

	// The strip names the scope.
	if view := m.View(); !strings.Contains(view, "scope:") {
		t.Fatalf("header should carry a scope segment:\n%s", view)
	}

	// Cycle to another mode: v steps swim → list. The neighborhood pivots with it —
	// the list is built from the SAME scoped set, not the whole board.
	m = press(t, m, "v")
	if m.view != ViewList {
		t.Fatalf("v should cycle to the list, view=%v", m.view)
	}
	if m.scopeRoot != "g-R" {
		t.Fatalf("cycling a mode must not drop the scope, scopeRoot=%q", m.scopeRoot)
	}
	if got := boardIDs(m); !eqIDs(got, neighborhood) {
		t.Fatalf("list inside the scope = %v, want the neighborhood %v", got, neighborhood)
	}

	// Apply a grouper inside the scope (M opens the control; commit type). The
	// grouper re-buckets the SAME neighborhood — pivoting, not widening.
	m = press(t, m, "M")
	if !m.control.open {
		t.Fatal("M should open the grouper control inside the scope")
	}
	m = focusGroup(t, m, "type")
	m = press(t, m, "enter")
	if m.boardFacet != rollup.FacetType {
		t.Fatalf("grouper should have set boardFacet=type, got %q", m.boardFacet)
	}
	if got := boardIDs(m); !eqIDs(got, neighborhood) {
		t.Fatalf("grouped-by-type inside the scope = %v, want the neighborhood %v", got, neighborhood)
	}

	// esc restores the EXACT pre-scope arrangement in one press: back to the
	// kanban, status grouping (the grouper set inside the scope does NOT leak),
	// the whole board, cursor back on g-R.
	m = press(t, m, "esc")
	if m.scopeRoot != "" {
		t.Fatalf("esc should leave the scope, scopeRoot=%q", m.scopeRoot)
	}
	if m.view != ViewKanban {
		t.Fatalf("esc should restore the pre-scope kanban, view=%v", m.view)
	}
	if m.boardFacet != "" {
		t.Fatalf("esc should restore the pre-scope grouping, boardFacet=%q leaked out", m.boardFacet)
	}
	if got := boardIDs(m); len(got) != 8 {
		t.Fatalf("esc should restore the whole board, got %d ids: %v", len(got), got)
	}
	if is := m.focusedIssue(); is == nil || is.ID != "g-R" {
		t.Fatalf("esc should re-select the pre-scope cursor (g-R), got %v", is)
	}
}
