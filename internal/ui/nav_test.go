package ui

import (
	"testing"

	"github.com/awhitty/bb/internal/bd"
)

// COARSE history: cursor scanning within a page records NOTHING — only a real
// page/view transition checkpoints, capturing the bead you were on when you
// left. `[` restores that page with that bead re-selected.
func TestCursorScanThenTransitionCheckpointsSelection(t *testing.T) {
	m := testModel(t, deepColumn(6), 100, 24) // one open column, board layout default
	// Scan three beads — pure cursor moves.
	m = press(t, m, "j", "j", "j")
	if len(m.navBack) != 0 {
		t.Fatalf("cursor scanning must not record history, got %d entries", len(m.navBack))
	}
	landed := m.focusedIssue().ID

	// A real page transition: toggle the board↔list layout.
	m = press(t, m, "b")
	if len(m.navBack) != 1 {
		t.Fatalf("one transition should leave exactly one checkpoint, got %d", len(m.navBack))
	}

	// [ restores the pre-transition page with the LAST-SCANNED bead selected.
	m = press(t, m, "[")
	if got := m.focusedIssue().ID; got != landed {
		t.Fatalf("[ should restore the last-scanned bead %s, got %s", landed, got)
	}
	// ] redoes the transition (back to the list layout the `b` produced).
	m = press(t, m, "]")
	if m.view != ViewList {
		t.Fatal("] should redo the board→list transition")
	}
}

// Many scans across several pages leave one checkpoint PER transition, never one
// per bead scanned.
func TestOneCheckpointPerTransition(t *testing.T) {
	m := testModel(t, deepColumn(8), 100, 24)
	m = press(t, m, "j", "j", "j", "j") // scan — 0 checkpoints
	m = press(t, m, "b")                // transition 1 (→ list)
	m = press(t, m, "j", "j")           // scan — still 0 new
	m = press(t, m, "b")                // transition 2 (→ board)
	m = press(t, m, "k")                // scan — still 0 new
	m = press(t, m, "5")                // transition 3 (→ tree)
	if len(m.navBack) != 3 {
		t.Fatalf("3 transitions (with scanning between) should leave 3 checkpoints, got %d", len(m.navBack))
	}
}

// Opening a bead's detail checkpoints the pre-detail board position, so esc/[
// returns to the board on the bead you opened from.
func TestDetailOpenCheckpointsAndBacks(t *testing.T) {
	m := testModel(t, deepColumn(6), 100, 24)
	m = press(t, m, "j", "j") // scan — no history
	if len(m.navBack) != 0 {
		t.Fatalf("scan recorded history, got %d", len(m.navBack))
	}
	from := m.focusedIssue().ID

	// Simulate the async detail open (showCmd → detailMsg) for the focused bead.
	next, _ := m.Update(detailMsg{issue: bd.Issue{ID: from, Title: "the opened bead", Status: "open"}})
	m = next.(Model)
	if m.detail == nil {
		t.Fatal("detail did not open")
	}
	if len(m.navBack) != 1 {
		t.Fatalf("opening a detail should checkpoint once, got %d", len(m.navBack))
	}

	// esc is back on the timeline: close the detail, land on the opened-from bead.
	m = press(t, m, "esc")
	if m.detail != nil {
		t.Fatal("esc should close the detail")
	}
	if got := m.focusedIssue().ID; got != from {
		t.Fatalf("esc should return to the bead the detail opened from (%s), got %s", from, got)
	}
	if len(m.navBack) != 0 {
		t.Fatalf("back should have consumed the checkpoint, got %d", len(m.navBack))
	}
}

// esc is browser-BACK on the SAME timeline as [ / ]: a view switch backs out
// with esc and redoes with ].
func TestEscIsBrowserBackOnViews(t *testing.T) {
	m := testModel(t, graphFixture(), 120, 30)
	m = press(t, m, "R") // enter the relationship swimlane (a navigation)
	if m.view != ViewSwim {
		t.Fatal("R should enter the swimlane")
	}
	m = press(t, m, "esc") // esc = back → out of the swimlane
	if m.view == ViewSwim {
		t.Fatal("esc should back out of the swimlane like [")
	}
	m = press(t, m, "]") // forward = redo → back into the swimlane
	if m.view != ViewSwim {
		t.Fatal("] should redo the swimlane entry")
	}
}

// The user's complaint, fixed: escaping a filter returns to the pre-filter
// position; [ never resurrects the cleared filter; ] redoes it.
func TestEscClearsFilterAndDoesNotResurrect(t *testing.T) {
	m := testModel(t, deepColumn(5), 100, 24)
	m = press(t, m, "/")           // open the filter prompt
	m = press(t, m, "status=open") // a query-shaped string → a raw bd filter
	m = press(t, m, "enter")       // apply it (no cursor move)
	if m.activeQuery != "status=open" {
		t.Fatalf("filter not applied, activeQuery=%q", m.activeQuery)
	}
	m = press(t, m, "esc") // esc = back → the filter was the last nav, so it clears
	if m.activeQuery != "" {
		t.Fatalf("esc should back out of the filter, activeQuery=%q", m.activeQuery)
	}
	m = press(t, m, "[") // back again must NOT resurrect the escaped filter
	if m.activeQuery != "" {
		t.Fatalf("[ resurrected a cleared filter, activeQuery=%q", m.activeQuery)
	}
	m = press(t, m, "]") // forward = redo the filter
	if m.activeQuery != "status=open" {
		t.Fatalf("] should redo the filter, activeQuery=%q", m.activeQuery)
	}
}

// Browser-back restores ONLY nav.go's scope. The sort and collapse the user
// changed AFTER a checkpoint survive the back (they are the agent snapshot's
// scope, not nav's); the position and open detail revert. A union snapshot()->
// restore() would revert the sort/collapse too — this locks that out.
func TestBrowserBackKeepsSortCollapseButRevertsPosition(t *testing.T) {
	m := testModel(t, graphFixture(), 120, 30)
	// A view transition checkpoints the board position (default sorts, no
	// collapse, no detail).
	m = press(t, m, "5") // enter the tree — one checkpoint captured (default sort)
	if m.view != ViewTree {
		t.Fatal("5 should enter the tree")
	}
	defaultSort := m.treeSort
	// The user changes the tree sort + collapses a subtree AFTER the checkpoint.
	// S opens the anchored sort control; j highlights the next key (live preview),
	// enter commits it.
	m = press(t, m, "S", "j", "enter")
	sortAfter := m.treeSort
	if sortAfter == defaultSort {
		t.Fatal("S should change the tree sort so it differs from the checkpoint's default")
	}
	m = press(t, m, "z")
	if len(m.collapsed) == 0 {
		t.Fatal("z should collapse the focused subtree")
	}
	collAfter := len(m.collapsed)
	// The user opens a detail so we can prove browser-back reverts it too.
	focused := m.focusedIssue().ID
	next, _ := m.Update(detailMsg{issue: bd.Issue{ID: focused, Title: "d", Status: "open"}})
	m = next.(Model)
	if m.detail == nil {
		t.Fatal("detail did not open")
	}
	// First back: closes the detail (its own checkpoint). Sort/collapse untouched.
	m = press(t, m, "esc")
	if m.detail != nil {
		t.Fatal("browser-back should revert the open detail")
	}
	if m.treeSort != sortAfter || len(m.collapsed) != collAfter {
		t.Fatal("browser-back reverted the user's sort/collapse — union restore regression")
	}
	// Second back: returns to the pre-tree board checkpoint, whose snapshot holds
	// the DEFAULT sort and an empty collapse. A union restore would apply those,
	// reverting the user's changes; the scoped restore must not.
	m = press(t, m, "[")
	if m.view == ViewTree {
		t.Fatal("[ should return to the board (position reverted)")
	}
	if m.treeSort != sortAfter {
		t.Fatalf("[ to a checkpoint with the default sort must not revert the user's sort, got %+v", m.treeSort)
	}
	if len(m.collapsed) != collAfter {
		t.Fatalf("[ must not revert the user's collapse, got %d entries", len(m.collapsed))
	}
}
