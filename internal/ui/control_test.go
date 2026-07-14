package ui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/awhitty/bb/internal/bd"
	"github.com/awhitty/bb/internal/rollup"
)

// sortFixture: two open cards whose priority order and title order disagree, so
// re-sorting visibly moves which card sits under the board cursor.
//   - by priority (the board default): z (P0) first, then a (P1)
//   - by title (ascending): a ("aaa") first, then z ("zzz")
func sortFixture() []bd.Issue {
	return []bd.Issue{
		{ID: "z", Title: "zzz", Status: "open", Priority: 0, IssueType: "task"},
		{ID: "a", Title: "aaa", Status: "open", Priority: 1, IssueType: "task"},
	}
}

// The sort control is the whole live-preview loop end-to-end: S opens an anchored
// dropdown over the current view's sort keys, arrowing a candidate re-sorts the
// board live (the target field mutates + rebuild), enter commits it. The frame
// stays exactly h lines / ≤ w with the menu open.
func TestSortControlOpensPreviewsAndCommits(t *testing.T) {
	const w, h = 120, 30
	m := testModel(t, sortFixture(), w, h) // ViewKanban, flatSort = priority

	if m.flatSort.Key != rollup.SortPriority {
		t.Fatalf("precondition: flatSort = %q, want priority", m.flatSort.Key)
	}
	if is := m.focusedIssue(); is == nil || is.ID != "z" {
		t.Fatalf("precondition: focused = %v, want z (P0 first)", m.focusedIssue())
	}

	// S opens the control, pre-selected on the active key; the menu-open frame fits.
	m = press(t, m, "S")
	if !m.control.open {
		t.Fatal("S did not open the sort control")
	}
	frameLines(t, m, w, h)
	if it, ok := m.control.list.SelectedItem().(controlItem); !ok || it.label != string(rollup.SortPriority) {
		t.Fatalf("control opened on %+v, want the active key (priority)", m.control.list.SelectedItem())
	}

	// Arrow to "title" (index 3 of FlatSortKeys): the board re-sorts live behind
	// the still-open menu — the field mutates and the focused card flips to "a".
	m = press(t, m, "j", "j", "j")
	if !m.control.open {
		t.Fatal("the control closed while arrowing")
	}
	if m.flatSort.Key != rollup.SortTitle {
		t.Fatalf("preview did not mutate flatSort: %q, want title", m.flatSort.Key)
	}
	if is := m.focusedIssue(); is == nil || is.ID != "a" {
		t.Fatalf("board did not re-sort under the menu: focused = %v, want a", m.focusedIssue())
	}
	frameLines(t, m, w, h)

	// enter commits the previewed sort and closes the menu.
	m = press(t, m, "enter")
	if m.control.open {
		t.Fatal("enter did not close the control")
	}
	if m.flatSort.Key != rollup.SortTitle {
		t.Fatalf("enter did not commit: flatSort = %q, want title", m.flatSort.Key)
	}
}

// esc restores the exact pre-open sort AND cursor: the previewed re-sort (and the
// board card it moved the cursor onto) is undone, back to the pre-open state.
func TestSortControlEscReverts(t *testing.T) {
	const w, h = 120, 30
	m := testModel(t, sortFixture(), w, h)

	beforeSort := m.flatSort
	beforeFocus := m.focusedIssue().ID // "z"

	m = press(t, m, "S", "j", "j", "j") // open, preview → title, focus flips to "a"
	if m.flatSort.Key != rollup.SortTitle || m.focusedIssue().ID != "a" {
		t.Fatalf("preview not live: sort=%q focus=%s", m.flatSort.Key, m.focusedIssue().ID)
	}

	m = press(t, m, "esc")
	if m.control.open {
		t.Fatal("esc did not close the control")
	}
	if m.flatSort != beforeSort {
		t.Fatalf("esc did not revert the sort: %+v, want %+v", m.flatSort, beforeSort)
	}
	if got := m.focusedIssue().ID; got != beforeFocus {
		t.Fatalf("esc did not restore the cursor: focused = %s, want %s", got, beforeFocus)
	}
}

// The open control frame fits every terminal size — a hard-fit text frame like
// every other view (exactly h lines, none wider than w).
func TestSortControlFrameFits(t *testing.T) {
	for _, wh := range [][2]int{{120, 30}, {80, 20}, {60, 16}} {
		w, h := wh[0], wh[1]
		m := testModel(t, deepColumn(6), w, h)
		next, _ := m.Update(keyRune('S'))
		m = next.(Model)
		if !m.control.open {
			t.Fatalf("%dx%d: S did not open the control", w, h)
		}
		frameLines(t, m, w, h)
	}
}

// The hierarchical views offer the tree sort keys (not the flat set): S in the
// tree opens over TreeSortKeys and drives treeSort.
func TestSortControlUsesTreeKeysInTree(t *testing.T) {
	const w, h = 120, 30
	m := testModel(t, graphFixture(), w, h)
	m = press(t, m, "5") // enter the tree (hierarchical view)
	if m.view != ViewTree {
		t.Fatal("5 should enter the tree")
	}

	next, _ := m.Update(keyRune('S'))
	m = next.(Model)
	it, ok := m.control.list.SelectedItem().(controlItem)
	if !ok || it.label != string(rollup.SortSubtreeSize) {
		t.Fatalf("tree control opened on %+v, want the tree default (subtree-size)", m.control.list.SelectedItem())
	}
	// j previews the next tree key, committing drives treeSort (not flatSort).
	next, _ = m.Update(keyRune('j'))
	m = next.(Model)
	if m.treeSort.Key != rollup.SortAggPriority {
		t.Fatalf("tree preview drove %q, want treeSort = aggregate-priority", m.treeSort.Key)
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.control.open {
		t.Fatal("enter did not close the tree sort control")
	}
	if m.treeSort.Key != rollup.SortAggPriority {
		t.Fatalf("commit did not stick: treeSort = %q", m.treeSort.Key)
	}
}
