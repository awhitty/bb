package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/awhitty/bb/internal/bd"
	"github.com/awhitty/bb/internal/rollup"
)

// focusGroup walks the open grouper control's cursor to the named option, so the
// live preview has run its apply func by the time it returns. White-box: it reads
// the live list's SelectedItem, robust to option order.
func focusGroup(t *testing.T, m Model, label string) Model {
	t.Helper()
	if !m.control.open {
		t.Fatalf("grouper control not open while seeking %q", label)
	}
	for i := 0; i < 20; i++ {
		if it, ok := m.control.list.SelectedItem().(controlItem); ok && it.label == label {
			return m
		}
		m = press(t, m, "j")
	}
	t.Fatalf("could not focus grouper option %q (selected %+v)", label, m.control.list.SelectedItem())
	return m
}

// focusControlUp walks the open control's cursor UP (k) to the named option — the
// list does not wrap, so reaching an option above the pre-selected one needs k,
// not focusGroup's j.
func focusControlUp(t *testing.T, m Model, label string) Model {
	t.Helper()
	if !m.control.open {
		t.Fatalf("control not open while seeking %q", label)
	}
	for i := 0; i < 20; i++ {
		if it, ok := m.control.list.SelectedItem().(controlItem); ok && it.label == label {
			return m
		}
		m = press(t, m, "k")
	}
	t.Fatalf("could not focus control option %q (selected %+v)", label, m.control.list.SelectedItem())
	return m
}

// crossTabIssues is a clean 2×2 fixture: two statuses (the default grouper axis)
// crossed with two types (the break-out lane axis), so a lane split produces a
// visible columns×lanes cross-tab with more than one band per column.
func crossTabIssues() []bd.Issue {
	return []bd.Issue{
		{ID: "a", Title: "one", Status: "open", IssueType: "task"},
		{ID: "b", Title: "two", Status: "open", IssueType: "bug"},
		{ID: "c", Title: "three", Status: "in_progress", IssueType: "task"},
		{ID: "d", Title: "four", Status: "in_progress", IssueType: "bug"},
	}
}

// L opens the break-out lane control on the kanban: arrowing a facet splits each
// column into lane bands live (boardLane + boardBands mutate behind the still-open
// menu), the frame fits, enter commits, and the rendered board shows a 2D cross-tab.
func TestLaneControlBuilds2DBoard(t *testing.T) {
	const w, h = 120, 30
	m := testModel(t, crossTabIssues(), w, h) // ViewKanban, grouped by status

	// Precondition: the flat 1D board — no lanes, no bands.
	if m.boardLane != "" || m.boardBands != nil {
		t.Fatalf("precondition: boardLane=%q boardBands=%v, want flat board", m.boardLane, m.boardBands)
	}

	m = press(t, m, "L")
	if !m.control.open {
		t.Fatal("L did not open the lane control on the kanban")
	}
	frameLines(t, m, w, h) // the anchored menu fits the terminal

	m = focusGroup(t, m, "type") // break out lanes by type
	if m.boardLane != rollup.FacetType {
		t.Fatalf("lane preview did not set boardLane: %q, want type", m.boardLane)
	}
	if len(m.boardBands) != len(m.columns) {
		t.Fatalf("boardBands (%d) not parallel to columns (%d)", len(m.boardBands), len(m.columns))
	}
	// 2D: every status column is split into more than one type band.
	for i, bands := range m.boardBands {
		if len(bands) < 2 {
			t.Fatalf("column %d (%q) has %d band(s), want a multi-lane split", i, m.columns[i].Key, len(bands))
		}
	}

	m = press(t, m, "enter")
	if m.control.open {
		t.Fatal("enter did not close the lane control")
	}
	if m.boardLane != rollup.FacetType {
		t.Fatalf("commit lost: boardLane = %q, want type", m.boardLane)
	}

	// The rendered board carries the lane bands (dim "─ <type>" headers) beneath
	// the status column titles — the visible 2D cross-tab.
	frame := ansi.Strip(m.View())
	for _, want := range []string{"─ task", "─ bug"} {
		if !strings.Contains(frame, want) {
			t.Fatalf("rendered board missing lane band header %q", want)
		}
	}
	// The header strip reads mode · grouper · lanes · sort.
	if !strings.Contains(frame, "lanes:type") {
		t.Fatalf("header strip missing the lanes segment; got:\n%s", strings.SplitN(frame, "\n", 2)[0])
	}

	// The 2D board renders to exactly the terminal frame, and stays fitted while
	// the cursor walks the lane-major column (the fixed per-band row budget).
	frameLines(t, m, w, h)
	for i := 0; i < 4; i++ {
		m = press(t, m, "j")
		frameLines(t, m, w, h)
	}
}

// laneAlignFixture forces the misalignment the swimlane bug produced: status
// columns crossed with priority lanes that DON'T line up column-to-column unless
// the board bands GLOBALLY. The open column holds three P0 cards and no P1; the
// in_progress column holds two P1 cards and no P0; both hold one P2 card. Per-
// column subdivision would stack ─ P0 at a different height in each column; global
// bands render ─ P0 once, spanning both columns, with in_progress's P0 slot blank.
func laneAlignFixture() []bd.Issue {
	mk := func(id, status string, pri int) bd.Issue {
		return bd.Issue{ID: id, Title: id, Status: status, Priority: pri, IssueType: "task"}
	}
	return []bd.Issue{
		mk("OA1", "open", 0), mk("OA2", "open", 0), mk("OA3", "open", 0),
		mk("IB1", "in_progress", 1), mk("IB2", "in_progress", 1),
		mk("OC1", "open", 2), mk("IC1", "in_progress", 2),
	}
}

// The break-out lanes render as GLOBAL horizontal bands: one lane order for the
// whole board, each lane a single band aligned across every column, so reading
// across a band shows how that priority plays out column by column.
func TestLaneBandsAlignGloballyAcrossColumns(t *testing.T) {
	const w, h = 140, 40
	m := testModel(t, laneAlignFixture(), w, h) // ViewKanban, grouped by status
	m = press(t, m, "L")
	m = focusGroup(t, m, "priority") // break out lanes by priority
	m = press(t, m, "enter")
	if m.boardLane != rollup.FacetPriority {
		t.Fatalf("boardLane = %q, want priority", m.boardLane)
	}

	frameLines(t, m, w, h) // the 2D board fits the terminal
	lines := strings.Split(ansi.Strip(m.View()), "\n")

	// lineOf finds the ONE body line carrying sub, failing if it renders on two
	// lines — a band header duplicated per column is exactly the bug being fixed.
	// Line 0 (the header strip, which echoes the focused card id) is skipped.
	lineOf := func(sub string) int {
		idx := -1
		for i := 1; i < len(lines); i++ {
			l := lines[i]
			if strings.Contains(l, sub) {
				if idx >= 0 {
					t.Fatalf("%q renders on both line %d and %d — a band header must appear once for the whole board, not once per column", sub, idx, i)
				}
				idx = i
			}
		}
		if idx < 0 {
			t.Fatalf("%q not found in frame:\n%s", sub, strings.Join(lines, "\n"))
		}
		return idx
	}

	// (a) Each lane band header appears exactly once, in the global count-desc order
	// P0(3) · P1(2) · P2(2) — not once per column.
	p0, p1, p2 := lineOf("─ P0"), lineOf("─ P1"), lineOf("─ P2")
	if !(p0 < p1 && p1 < p2) {
		t.Fatalf("band order lines P0=%d P1=%d P2=%d, want P0 < P1 < P2 (global order)", p0, p1, p2)
	}

	// (b) Cards from different columns belonging to the same lane appear between
	// that band's header and the next band's header: the open column's three P0
	// cards under ─ P0, the in_progress column's two P1 cards under ─ P1.
	for _, id := range []string{"OA1", "OA2", "OA3"} {
		if r := lineOf(id); !(r > p0 && r < p1) {
			t.Fatalf("%s on line %d, want between ─ P0 (%d) and ─ P1 (%d)", id, r, p0, p1)
		}
	}
	for _, id := range []string{"IB1", "IB2"} {
		if r := lineOf(id); !(r > p1 && r < p2) {
			t.Fatalf("%s on line %d, want between ─ P1 (%d) and ─ P2 (%d)", id, r, p1, p2)
		}
	}

	// (c) A column empty in a lane still occupies aligned space. In the P1 band the
	// open column has no card, yet in_progress's first P1 card sits on the band's
	// first row (right below ─ P1), with the open column blank to its left — the
	// empty column does not pull the next column's cards up.
	ib1 := lineOf("IB1")
	if ib1 != p1+1 {
		t.Fatalf("IB1 on line %d, want p1+1 (%d) — an empty open column must not collapse the band", ib1, p1+1)
	}
	// The two in_progress P1 cards stack on the band's first two rows, and the open
	// column bleeds no card onto them — aligned empty space, not a collapsed band.
	if r := lineOf("IB2"); r != p1+2 {
		t.Fatalf("IB2 on line %d, want p1+2 (%d) — the P1 cards must stack under their header", r, p1+2)
	}
	for _, r := range []int{p1 + 1, p1 + 2} {
		for _, id := range []string{"OA1", "OA2", "OA3", "OC1"} {
			if strings.Contains(lines[r], id) {
				t.Fatalf("open card %s bled into the P1 band row %d (%q) — the open column must be blank there", id, r, lines[r])
			}
		}
	}
	// And a lane populated in BOTH columns reads across one shared row: the P2 band
	// puts open's OC1 and in_progress's IC1 on the SAME line, open then in_progress.
	oc, ic := strings.Index(lines[p2+1], "OC1"), strings.Index(lines[p2+1], "IC1")
	if oc < 0 || ic < 0 || oc >= ic {
		t.Fatalf("P2 band row %q: OC1@%d IC1@%d — both columns' P2 cards must read across the same row, open then in_progress", lines[p2+1], oc, ic)
	}
}

// "none" collapses the 2D board back to the flat 1D board, and esc reverts a live
// lane preview to the pre-open state.
func TestLaneControlNoneClearsAndEscReverts(t *testing.T) {
	const w, h = 120, 30
	m := testModel(t, crossTabIssues(), w, h)

	// Break out by type, committed.
	m = press(t, m, "L")
	m = focusGroup(t, m, "type")
	m = press(t, m, "enter")
	if m.boardLane != rollup.FacetType || m.boardBands == nil {
		t.Fatalf("setup: boardLane=%q bands=%v, want a 2D board", m.boardLane, m.boardBands)
	}

	// none clears it back to the flat board.
	m = press(t, m, "L")
	m = focusControlUp(t, m, "none")
	if m.boardLane != "" {
		t.Fatalf("none did not clear the lane: %q", m.boardLane)
	}
	m = press(t, m, "enter")
	if m.boardBands != nil {
		t.Fatalf("none-cleared board still carries bands: %v", m.boardBands)
	}

	// esc reverts a live preview: break out by type, then esc back to flat.
	m = press(t, m, "L")
	m = focusGroup(t, m, "type")
	if m.boardLane != rollup.FacetType {
		t.Fatalf("preview did not set the lane: %q", m.boardLane)
	}
	m = press(t, m, "esc")
	if m.control.open {
		t.Fatal("esc did not close the lane control")
	}
	if m.boardLane != "" || m.boardBands != nil {
		t.Fatalf("esc did not revert to the flat board: boardLane=%q bands=%v", m.boardLane, m.boardBands)
	}
}

// v steps the view directly through list → board → tree → list with no menu,
// each keypress a single transition of m.view.
func TestViewCycleStepsListBoardTree(t *testing.T) {
	const w, h = 120, 30
	m := testModel(t, deepColumn(6), w, h) // ViewKanban

	// Start from the list so the full documented order is exercised.
	m = press(t, m, "b") // kanban → list
	if m.view != ViewList {
		t.Fatalf("precondition: view = %v, want list", m.view)
	}

	m = press(t, m, "v")
	if m.view != ViewKanban {
		t.Fatalf("v from list → %v, want board", m.view)
	}
	m = press(t, m, "v")
	if m.view != ViewTree {
		t.Fatalf("v from board → %v, want tree", m.view)
	}
	m = press(t, m, "v")
	if m.view != ViewList {
		t.Fatalf("v from tree → %v, want list (wrap)", m.view)
	}
}

// The cycle re-syncs m.mode from any active facet override (facetToMode), so the
// header and the digit/m grouping keys stay coherent across a view switch.
func TestViewCycleKeepsModeCoherent(t *testing.T) {
	const w, h = 120, 30
	m := testModel(t, deepColumn(6), w, h) // ViewKanban, mode status

	m.applyGroup(rollup.FacetType) // boardFacet = type, mode = type
	if m.boardFacet != rollup.FacetType || m.mode != rollup.ModeType {
		t.Fatalf("precondition: boardFacet=%q mode=%q, want type/type", m.boardFacet, m.mode)
	}
	m.mode = rollup.ModeStatus // desync to prove the cycle re-syncs it

	m = press(t, m, "v") // board → tree
	if m.view != ViewTree {
		t.Fatalf("v → %v, want tree", m.view)
	}
	if m.mode != rollup.ModeType {
		t.Fatalf("cycle did not re-sync mode from boardFacet: %q, want type", m.mode)
	}
}

// M opens the grouper over the board's facet slot: arrowing a candidate re-groups
// the board live (boardFacet mutates behind the still-open menu), enter commits,
// and the menu-open frame fits.
func TestGrouperControlBoard(t *testing.T) {
	const w, h = 120, 30
	issues := []bd.Issue{
		{ID: "a", Title: "one", Status: "open", IssueType: "task", Labels: []string{"backend"}},
		{ID: "b", Title: "two", Status: "open", IssueType: "task", Labels: []string{"frontend"}},
	}
	m := testModel(t, issues, w, h) // ViewKanban

	m = press(t, m, "M")
	if !m.control.open {
		t.Fatal("M did not open the grouper control")
	}
	frameLines(t, m, w, h)

	m = focusGroup(t, m, "label") // a facet with no legacy Mode
	if m.boardFacet != rollup.FacetLabel {
		t.Fatalf("board grouper preview did not set boardFacet: %q, want label", m.boardFacet)
	}
	keys := map[string]bool{}
	for _, c := range m.columns {
		keys[c.Key] = true
	}
	if !keys["backend"] || !keys["frontend"] {
		t.Fatalf("board did not re-segment by label under the menu: %v", keys)
	}

	m = press(t, m, "enter")
	if m.control.open {
		t.Fatal("enter did not close the grouper")
	}
	if m.boardFacet != rollup.FacetLabel {
		t.Fatalf("commit lost: boardFacet = %q, want label", m.boardFacet)
	}
}

// In the tree the grouper drives treeFacet (segmentation), not boardFacet.
func TestGrouperControlTree(t *testing.T) {
	const w, h = 120, 30
	m := testModel(t, graphFixture(), w, h)
	m = press(t, m, "5") // enter the tree
	if m.view != ViewTree {
		t.Fatalf("5 should enter the tree, got %v", m.view)
	}

	m = press(t, m, "M")
	if !m.control.open {
		t.Fatal("M did not open the grouper in the tree")
	}
	frameLines(t, m, w, h)

	m = focusGroup(t, m, "label")
	if m.treeFacet != rollup.FacetLabel {
		t.Fatalf("tree grouper preview did not set treeFacet: %q, want label", m.treeFacet)
	}

	// esc reverts to the unsegmented tree.
	m = press(t, m, "esc")
	if m.control.open {
		t.Fatal("esc did not close the tree grouper")
	}
	if m.treeFacet != "" {
		t.Fatalf("esc did not revert treeFacet: %q, want none", m.treeFacet)
	}
}

// In the relationship board the grouper drives the column-axis facet (swimFacet).
func TestGrouperControlSwim(t *testing.T) {
	const w, h = 120, 30
	m := testModel(t, graphFixture(), w, h)
	m = press(t, m, "R") // relationship board rooted at the focused bead
	if m.view != ViewSwim {
		t.Fatalf("R should enter the relationship board, got %v", m.view)
	}

	m = press(t, m, "M")
	if !m.control.open {
		t.Fatal("M did not open the grouper in the relationship board")
	}
	frameLines(t, m, w, h)

	m = focusGroup(t, m, "type")
	if m.swimFacet != rollup.FacetType {
		t.Fatalf("swim grouper preview did not set swimFacet: %q, want type", m.swimFacet)
	}
}
