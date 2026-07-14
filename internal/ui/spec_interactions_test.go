package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/awhitty/bb/internal/rollup"
)

// spec_interactions_test.go drives the facet-controls spec end-to-end through real
// key sequences and asserts on rendered frames. It fills the interactions the
// existing suites leave implicit: the LIST surface's section-facet + within-section
// sort (the board tests exercise the kanban), the live-preview visible IN the frame
// behind the still-open menu, esc reverting to the byte-exact pre-open frame, and
// the glanceable strip carrying mode · group · lanes · sort together.

// headerLine returns the ANSI-stripped header strip (the line carrying "bb")
// from a rendered frame, so the strip's segments can be asserted as plain text.
func headerLine(t *testing.T, view string) string {
	t.Helper()
	for _, l := range strings.Split(ansi.Strip(view), "\n") {
		if strings.Contains(l, "bb") {
			return l
		}
	}
	t.Fatalf("no header strip in frame:\n%s", ansi.Strip(view))
	return ""
}

// boardColKeys is the set of column/section keys the current board/list built.
func boardColKeys(m Model) map[string]bool {
	keys := map[string]bool{}
	for _, c := range m.columns {
		keys[c.Key] = true
	}
	return keys
}

// TestListSectionFacetCycles drives the LIST's section-facet control: b enters the
// list, M opens the grouper, and highlighting a facet re-sections the list live —
// the same neighborhood re-bucketed by a new axis. Committed with enter.
func TestListSectionFacetCycles(t *testing.T) {
	const w, h = 120, 30
	m := testModel(t, crossTabIssues(), w, h) // ViewKanban, grouped by status

	m = press(t, m, "b") // kanban → list
	if m.view != ViewList {
		t.Fatalf("b should enter the list, view=%v", m.view)
	}
	// The list sections by status to start.
	if k := boardColKeys(m); !k["open"] || !k["in_progress"] {
		t.Fatalf("list sections = %v, want the status axis (open + in_progress)", k)
	}

	m = press(t, m, "M")
	if !m.control.open {
		t.Fatal("M did not open the section-facet control in the list")
	}
	frameLines(t, m, w, h) // the anchored menu fits

	m = focusGroup(t, m, "type") // cycle the section-facet to type
	if m.boardFacet != rollup.FacetType {
		t.Fatalf("section-facet preview did not set boardFacet: %q, want type", m.boardFacet)
	}
	if k := boardColKeys(m); !k["task"] || !k["bug"] {
		t.Fatalf("list did not re-section by type behind the menu: %v", k)
	}

	m = press(t, m, "enter")
	if m.control.open {
		t.Fatal("enter did not close the section-facet control")
	}
	if m.boardFacet != rollup.FacetType {
		t.Fatalf("commit lost: boardFacet=%q, want type", m.boardFacet)
	}
	if m.view != ViewList {
		t.Fatalf("the section-facet control must leave the list in place, view=%v", m.view)
	}
}

// TestListWithinSectionSortCycles drives the LIST's within-section sort control: S
// opens the sort keys, and highlighting a key re-orders the cards inside their
// section live — the card under the cursor flips. Committed with enter.
func TestListWithinSectionSortCycles(t *testing.T) {
	const w, h = 120, 30
	m := testModel(t, sortFixture(), w, h) // ViewKanban, flatSort = priority

	m = press(t, m, "b") // kanban → list
	if m.view != ViewList {
		t.Fatalf("b should enter the list, view=%v", m.view)
	}
	// The whole fixture is one "open" section; by priority z (P0) leads.
	if is := m.focusedIssue(); is == nil || is.ID != "z" {
		t.Fatalf("precondition: focused=%v, want z (P0 leads by priority)", m.focusedIssue())
	}

	m = press(t, m, "S")
	if !m.control.open {
		t.Fatal("S did not open the within-section sort control in the list")
	}
	if it, ok := m.control.list.SelectedItem().(controlItem); !ok || it.label != string(rollup.SortPriority) {
		t.Fatalf("sort control opened on %+v, want the active key (priority)", m.control.list.SelectedItem())
	}
	frameLines(t, m, w, h)

	m = focusGroup(t, m, string(rollup.SortTitle)) // cycle within-section sort → title
	if m.flatSort.Key != rollup.SortTitle {
		t.Fatalf("sort preview did not set flatSort: %q, want title", m.flatSort.Key)
	}
	if is := m.focusedIssue(); is == nil || is.ID != "a" {
		t.Fatalf("section did not re-sort under the menu: focused=%v, want a (title order)", m.focusedIssue())
	}

	m = press(t, m, "enter")
	if m.control.open {
		t.Fatal("enter did not close the within-section sort control")
	}
	if m.flatSort.Key != rollup.SortTitle {
		t.Fatalf("commit lost: flatSort=%q, want title", m.flatSort.Key)
	}
}

// TestGrouperLivePreviewRendersBehindMenu proves the live preview is visible IN the
// rendered frame: with the grouper open, highlighting "type" re-groups the board
// BEHIND the still-open menu — the frame gains the type column headers while the
// cards stay on screen (the small menu never occludes the view).
func TestGrouperLivePreviewRendersBehindMenu(t *testing.T) {
	const w, h = 120, 30
	m := testModel(t, crossTabIssues(), w, h) // ViewKanban, grouped by status

	// Pre-open: the board is status-grouped, so no type-bucket header renders.
	f0 := ansi.Strip(m.View())
	if strings.Contains(f0, "bug") {
		t.Fatalf("precondition: pre-open frame already shows a type bucket:\n%s", f0)
	}

	m = press(t, m, "M")
	if !m.control.open {
		t.Fatal("M did not open the grouper")
	}
	// Menu open, nothing highlighted away from status yet: board behind unchanged.
	fOpen := ansi.Strip(m.View())
	if strings.Contains(fOpen, "bug") {
		t.Fatalf("board changed before any option was highlighted:\n%s", fOpen)
	}

	m = focusGroup(t, m, "type") // highlight type → live preview applies
	if !m.control.open {
		t.Fatal("highlighting an option closed the menu; preview must stay live")
	}
	fType := ansi.Strip(m.View())
	// The board re-rendered behind the menu — the type-bucket headers appear.
	for _, want := range []string{"task", "bug"} {
		if !strings.Contains(fType, want) {
			t.Fatalf("board did not re-render behind the open menu (missing %q):\n%s", want, fType)
		}
	}
	// The small menu is anchored over the board's top-left, so it is NOT a
	// full-screen modal: the board shows around it. Canonical type order puts
	// "task" left (behind the menu) and "bug" right; the right-hand "bug" column
	// (its header and both cards, clear of the anchored menu) stays on screen.
	for _, want := range []string{"bug (2)", "two", "four"} {
		if !strings.Contains(fType, want) {
			t.Fatalf("the anchored menu occluded the board (missing %q — a full-screen modal would hide it):\n%s", want, fType)
		}
	}

	m = press(t, m, "enter") // commit keeps the previewed grouping
	if m.control.open {
		t.Fatal("enter did not close the grouper")
	}
	if fc := ansi.Strip(m.View()); !strings.Contains(fc, "bug") {
		t.Fatalf("commit lost the previewed grouping in the frame:\n%s", fc)
	}
}

// TestControlEscRestoresExactPreOpenFrame proves esc reverts to the pre-open FRAME,
// byte-for-byte: it captures the frame before the control opens, previews a live
// change (the board re-renders behind the menu), then esc — and the rendered frame
// is identical to the pre-open capture, cursor and all.
func TestControlEscRestoresExactPreOpenFrame(t *testing.T) {
	const w, h = 120, 30
	m := testModel(t, crossTabIssues(), w, h)

	preOpen := m.View() // the exact pre-open frame (ANSI included)

	m = press(t, m, "M")
	m = focusGroup(t, m, "type") // live preview changes the board behind the menu
	if !strings.Contains(ansi.Strip(m.View()), "bug") {
		t.Fatal("preview did not change the board — nothing to revert")
	}

	m = press(t, m, "esc")
	if m.control.open {
		t.Fatal("esc did not close the control")
	}
	if m.boardFacet != "" {
		t.Fatalf("esc did not revert the grouping facet: %q", m.boardFacet)
	}
	if got := m.View(); got != preOpen {
		t.Fatalf("esc did not restore the exact pre-open frame.\n--- pre-open ---\n%s\n--- after esc ---\n%s",
			ansi.Strip(preOpen), ansi.Strip(got))
	}
}

// TestStripShowsModeGroupLanesSort asserts the glanceable header strip carries all
// four facets in reading order — mode · group · lanes · sort — once the kanban has a
// break-out lane set, so a glance names every axis the controls drive.
func TestStripShowsModeGroupLanesSort(t *testing.T) {
	const w, h = 140, 30
	m := testModel(t, crossTabIssues(), w, h) // ViewKanban, grouped by status, sort priority

	// Break out lanes by type so the lanes segment is present.
	m = press(t, m, "L")
	m = focusGroup(t, m, "type")
	m = press(t, m, "enter")
	if m.boardLane != rollup.FacetType {
		t.Fatalf("setup: boardLane=%q, want type", m.boardLane)
	}

	strip := headerLine(t, m.View())
	iMode := strings.Index(strip, "board")       // mode segment
	iGroup := strings.Index(strip, "status")     // grouper segment (default status)
	iLanes := strings.Index(strip, "lanes:type") // break-out lanes segment
	iSort := strings.Index(strip, "priority")    // sort segment (default key)

	for name, idx := range map[string]int{"mode(board)": iMode, "group(status)": iGroup, "lanes:type": iLanes, "sort(priority)": iSort} {
		if idx < 0 {
			t.Fatalf("strip missing %s segment; strip = %q", name, strip)
		}
	}
	if !(iMode < iGroup && iGroup < iLanes && iLanes < iSort) {
		t.Fatalf("strip segments out of order (want mode<group<lanes<sort): mode=%d group=%d lanes=%d sort=%d\n%q",
			iMode, iGroup, iLanes, iSort, strip)
	}
}
