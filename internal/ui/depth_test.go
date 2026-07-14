package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/awhitty/bb/internal/bd"
	"github.com/awhitty/bb/internal/rollup"
)

// The depth facet groups the board by computed dependency depth, so the columns
// read as a tech tree: the left column is what can happen NOW, each column to its
// right is what the previous columns unlock. These are frame-level proofs that
// drive the board the way a user would.

// depthChain is a←b←c: b is blocked by a, c by b. Titles avoid the words "ready"
// and "depth" so a column-header scan can't match on card text.
func depthChain() []bd.Issue {
	return []bd.Issue{
		{ID: "a", Title: "alpha", Status: "open", Priority: 1, IssueType: "task"},
		{ID: "b", Title: "beta", Status: "open", Priority: 1, IssueType: "task",
			Dependencies: []bd.Dependency{{IssueID: "b", DependsOnID: "a", Type: "blocks"}}},
		{ID: "c", Title: "gamma", Status: "open", Priority: 1, IssueType: "task",
			Dependencies: []bd.Dependency{{IssueID: "c", DependsOnID: "b", Type: "blocks"}}},
	}
}

// colKeyOf reports which board column currently holds id, "" if none.
func colKeyOf(m Model, id string) string {
	for _, c := range m.columns {
		for _, is := range c.Issues {
			if is.ID == id {
				return c.Key
			}
		}
	}
	return ""
}

// (a) A chain a←b←c grouped by depth renders three columns left to right:
// ready · depth 1 · depth 2, each holding exactly its rank's card.
func TestBoardColumnsDepthTechTreeOrder(t *testing.T) {
	const w, h = 160, 40
	m := testModel(t, depthChain(), w, h) // ViewKanban
	m.applyGroup(rollup.FacetDepth)

	want := []struct{ key, id string }{{"ready", "a"}, {"depth 1", "b"}, {"depth 2", "c"}}
	if len(m.columns) != len(want) {
		t.Fatalf("depth board has %d columns, want %d: %+v", len(m.columns), len(want), m.columns)
	}
	for i, wc := range want {
		col := m.columns[i]
		if col.Key != wc.key {
			t.Fatalf("column %d key = %q, want %q", i, col.Key, wc.key)
		}
		if len(col.Issues) != 1 || col.Issues[0].ID != wc.id {
			t.Fatalf("column %q = %v, want [%s]", col.Key, ids(col.Issues), wc.id)
		}
	}

	// Frame: the shared column-header row reads ready → depth 1 → depth 2 left to
	// right (the tech-tree reading), not by card count.
	lines := strings.Split(ansi.Strip(m.View()), "\n")
	var hdr string
	for _, l := range lines {
		if strings.Contains(l, "ready") && strings.Contains(l, "depth 2") {
			hdr = l
			break
		}
	}
	if hdr == "" {
		t.Fatalf("no column-header row carrying ready and depth 2:\n%s", strings.Join(lines, "\n"))
	}
	prev := -1
	for _, s := range []string{"ready", "depth 1", "depth 2"} {
		i := strings.Index(hdr, s)
		if i < 0 {
			t.Fatalf("depth column %q missing from header row %q", s, hdr)
		}
		if i <= prev {
			t.Fatalf("depth columns out of tech-tree order in %q: %q at x=%d, previous at x=%d", hdr, s, i, prev)
		}
		prev = i
	}
}

// (b) Closing a blocker moves its dependent to the ready front — a closed blocker
// does not gate. Start with the open chain (b at depth 1), close a, and b lands
// in ready.
func TestBoardColumnsDepthClosedBlockerMovesToReady(t *testing.T) {
	const w, h = 160, 40
	m := testModel(t, depthChain(), w, h)
	m.applyGroup(rollup.FacetDepth)
	if key := colKeyOf(m, "b"); key != "depth 1" {
		t.Fatalf("precondition: b in %q, want depth 1", key)
	}

	closed := depthChain()
	closed[0].Status = "closed" // a is done
	next, _ := m.Update(issuesMsg{seq: m.seq, issues: closed})
	m = next.(Model)

	if key := colKeyOf(m, "b"); key != "ready" {
		t.Fatalf("after closing a, b in %q, want ready (closed blocker must not gate)", key)
	}
	if key := colKeyOf(m, "c"); key != "depth 1" {
		t.Fatalf("after closing a, c in %q, want depth 1 (still blocked by open b)", key)
	}
}

// epicWithChildren is an epic E over two open children: C0 is a workable leaf
// (depth 0); C1 is blocked by an unrelated task X, so C1 is depth 1. Because bd
// never calls a parent with open children "ready", E is gated by its subtree and
// lands at depth 2. Titles avoid "ready"/"depth" so a header scan can't false-match.
func epicWithChildren() []bd.Issue {
	return []bd.Issue{
		{ID: "E", Title: "the epic", Status: "open", Priority: 1, IssueType: "epic"},
		{ID: "C0", Title: "leaf child", Status: "open", Priority: 1, IssueType: "task", Parent: "E"},
		{ID: "C1", Title: "blocked child", Status: "open", Priority: 1, IssueType: "task", Parent: "E",
			Dependencies: []bd.Dependency{{IssueID: "C1", DependsOnID: "X", Type: "blocks"}}},
		{ID: "X", Title: "sibling task", Status: "open", Priority: 1, IssueType: "task"},
	}
}

// (a) An epic with open children ranks BELOW the ready front. Grouped by depth,
// E lands in depth 2 (one past its deepest open child), never in ready — the
// ready column is genuinely workable leaves only.
func TestBoardColumnsDepthEpicWithOpenChildrenNotReady(t *testing.T) {
	const w, h = 160, 40
	m := testModel(t, epicWithChildren(), w, h)
	m.applyGroup(rollup.FacetDepth)

	if key := colKeyOf(m, "E"); key != "depth 2" {
		t.Fatalf("epic E in %q, want depth 2 (gated by its open children, not ready)", key)
	}
	// The ready front is workable leaves only: C0 and X, and never the epic.
	var ready []string
	for _, c := range m.columns {
		if c.Key == "ready" {
			ready = ids(c.Issues)
		}
	}
	if contains(ready, "E") {
		t.Fatalf("epic E must not sit in the ready front: %v", ready)
	}
	if !contains(ready, "C0") || !contains(ready, "X") {
		t.Fatalf("ready front = %v, want the workable leaves C0 and X", ready)
	}
}

// (b) Closing every child of the epic moves it to the ready front — closed
// children do not gate, the mirror of a closed blocker.
func TestBoardColumnsDepthClosingChildrenMovesEpicToReady(t *testing.T) {
	const w, h = 160, 40
	m := testModel(t, epicWithChildren(), w, h)
	m.applyGroup(rollup.FacetDepth)
	if key := colKeyOf(m, "E"); key != "depth 2" {
		t.Fatalf("precondition: E in %q, want depth 2", key)
	}

	closed := epicWithChildren()
	for i := range closed {
		if closed[i].ID == "C0" || closed[i].ID == "C1" {
			closed[i].Status = "closed"
		}
	}
	next, _ := m.Update(issuesMsg{seq: m.seq, issues: closed})
	m = next.(Model)

	if key := colKeyOf(m, "E"); key != "ready" {
		t.Fatalf("after closing its children, E in %q, want ready (closed children don't gate)", key)
	}
}

// (c) Gates compose across the hierarchy and dependency edges. The epic's only
// child sits atop a two-deep blocker chain (A←B←child), so child is depth 2 and
// the epic is one deeper — the apex lands at max-subtree-depth + 1.
func TestBoardColumnsDepthChildDependencyChain(t *testing.T) {
	const w, h = 160, 40
	chain := []bd.Issue{
		{ID: "A", Title: "a", Status: "open", Priority: 1, IssueType: "task"},
		{ID: "B", Title: "b", Status: "open", Priority: 1, IssueType: "task",
			Dependencies: []bd.Dependency{{IssueID: "B", DependsOnID: "A", Type: "blocks"}}},
		{ID: "child", Title: "child", Status: "open", Priority: 1, IssueType: "task", Parent: "E",
			Dependencies: []bd.Dependency{{IssueID: "child", DependsOnID: "B", Type: "blocks"}}},
		{ID: "E", Title: "the epic", Status: "open", Priority: 1, IssueType: "epic"},
	}
	m := testModel(t, chain, w, h)
	m.applyGroup(rollup.FacetDepth)

	if key := colKeyOf(m, "child"); key != "depth 2" {
		t.Fatalf("child in %q, want depth 2 (atop the A←B chain)", key)
	}
	if key := colKeyOf(m, "E"); key != "depth 3" {
		t.Fatalf("epic E in %q, want depth 3 (deepest descendant + 1)", key)
	}
}

// contains reports whether ids holds id.
func contains(ids []string, id string) bool {
	for _, s := range ids {
		if s == id {
			return true
		}
	}
	return false
}

// depthLaneFixture crosses depth columns with priority lanes such that a lane is
// populated in one depth column and empty in the other, so only GLOBAL bands line
// up. Ready column: three P0 cards (RA1..3) and one P2 (RC1), all unblocked.
// Depth-1 column: two P1 cards (DB1, DB2) and one P2 (DC1), each blocked by RA1.
func depthLaneFixture() []bd.Issue {
	mk := func(id string, pri int, blockedBy string) bd.Issue {
		is := bd.Issue{ID: id, Title: id, Status: "open", Priority: pri, IssueType: "task"}
		if blockedBy != "" {
			is.Dependencies = []bd.Dependency{{IssueID: id, DependsOnID: blockedBy, Type: "blocks"}}
		}
		return is
	}
	return []bd.Issue{
		mk("RA1", 0, ""), mk("RA2", 0, ""), mk("RA3", 0, ""), mk("RC1", 2, ""),
		mk("DB1", 1, "RA1"), mk("DB2", 1, "RA1"), mk("DC1", 2, "RA1"),
	}
}

// (c) columns=depth composes with lanes=priority: the priority bands render as
// single GLOBAL horizontal bands aligned across the depth columns (the alignment
// from the depth calculation), not once per column.
func TestBoardColumnsDepthWithPriorityLanesGlobalBands(t *testing.T) {
	const w, h = 140, 40
	m := testModel(t, depthLaneFixture(), w, h)
	m.applyGroup(rollup.FacetDepth) // columns = depth
	m = press(t, m, "L")
	m = focusGroup(t, m, "priority") // lanes = priority
	m = press(t, m, "enter")
	if m.boardFacet != rollup.FacetDepth || m.boardLane != rollup.FacetPriority {
		t.Fatalf("board axes = columns %q / lanes %q, want depth / priority", m.boardFacet, m.boardLane)
	}
	// The depth columns are ready then depth 1.
	if len(m.columns) != 2 || m.columns[0].Key != "ready" || m.columns[1].Key != "depth 1" {
		t.Fatalf("depth columns = %+v, want [ready, depth 1]", m.columns)
	}

	frameLines(t, m, w, h) // the 2D board fits the terminal
	lines := strings.Split(ansi.Strip(m.View()), "\n")

	// lineOf finds the ONE body line carrying sub — a band header duplicated per
	// column (the bug global bands fix) would make it appear twice.
	lineOf := func(sub string) int {
		idx := -1
		for i := 1; i < len(lines); i++ {
			if strings.Contains(lines[i], sub) {
				if idx >= 0 {
					t.Fatalf("%q renders on both line %d and %d — a band header must appear once for the whole board, not once per depth column", sub, idx, i)
				}
				idx = i
			}
		}
		if idx < 0 {
			t.Fatalf("%q not found in frame:\n%s", sub, strings.Join(lines, "\n"))
		}
		return idx
	}

	// Each priority band header appears once, in canonical order P0 · P1 · P2.
	p0, p1, p2 := lineOf("─ P0"), lineOf("─ P1"), lineOf("─ P2")
	if !(p0 < p1 && p1 < p2) {
		t.Fatalf("band order lines P0=%d P1=%d P2=%d, want P0 < P1 < P2", p0, p1, p2)
	}

	// The ready column's three P0 cards sit under ─ P0; the depth-1 column's two P1
	// cards under ─ P1 — cards from different columns read across the same band.
	for _, id := range []string{"RA1", "RA2", "RA3"} {
		if r := lineOf(id); !(r > p0 && r < p1) {
			t.Fatalf("%s on line %d, want in the P0 band (%d..%d)", id, r, p0, p1)
		}
	}
	for _, id := range []string{"DB1", "DB2"} {
		if r := lineOf(id); !(r > p1 && r < p2) {
			t.Fatalf("%s on line %d, want in the P1 band (%d..%d)", id, r, p1, p2)
		}
	}
	// The P1 band is empty in the ready column, yet the depth-1 cards start right
	// under the shared header — the empty column does not collapse the band.
	if r := lineOf("DB1"); r != p1+1 {
		t.Fatalf("DB1 on line %d, want p1+1 (%d) — an empty ready column must not collapse the band", r, p1+1)
	}
	// A lane populated in BOTH columns reads across one shared row: the P2 band puts
	// ready's RC1 and depth-1's DC1 on the same line, ready (left) then depth 1.
	rc, dc := strings.Index(lines[p2+1], "RC1"), strings.Index(lines[p2+1], "DC1")
	if rc < 0 || dc < 0 || rc >= dc {
		t.Fatalf("P2 band row %q: RC1@%d DC1@%d — both columns' P2 cards must read across the same row, ready then depth 1", lines[p2+1], rc, dc)
	}
}

// (d) Inside a relationship scope (scopeRoot — what R sets), depth ranks are
// computed within the neighborhood, not the whole board. Over the full chain
// a←b←c←d←e, b is depth 1 (blocked by a). Scoped to c, the neighborhood is
// {b, c, d} — b's blocker a is out of scope — so b is recomputed to the ready
// front.
func TestBoardColumnsDepthWithinScope(t *testing.T) {
	const w, h = 160, 40
	chain := []bd.Issue{
		{ID: "a", Title: "a", Status: "open", IssueType: "task"},
		{ID: "b", Title: "b", Status: "open", IssueType: "task",
			Dependencies: []bd.Dependency{{IssueID: "b", DependsOnID: "a", Type: "blocks"}}},
		{ID: "c", Title: "c", Status: "open", IssueType: "task",
			Dependencies: []bd.Dependency{{IssueID: "c", DependsOnID: "b", Type: "blocks"}}},
		{ID: "d", Title: "d", Status: "open", IssueType: "task",
			Dependencies: []bd.Dependency{{IssueID: "d", DependsOnID: "c", Type: "blocks"}}},
		{ID: "e", Title: "e", Status: "open", IssueType: "task",
			Dependencies: []bd.Dependency{{IssueID: "e", DependsOnID: "d", Type: "blocks"}}},
	}
	m := testModel(t, chain, w, h)
	m.applyGroup(rollup.FacetDepth)

	// Unscoped: b is one deep, blocked by a.
	if key := colKeyOf(m, "b"); key != "depth 1" {
		t.Fatalf("unscoped: b in %q, want depth 1", key)
	}

	// Scope to c (the mode-independent scope R produces) and recompute.
	m.scopeRoot = "c"
	m.rebuild()

	// The neighborhood is {b, c, d}; b's blocker a is out of scope, so b is ready.
	if key := colKeyOf(m, "b"); key != "ready" {
		t.Fatalf("scoped to c: b in %q, want ready (a is out of the neighborhood)", key)
	}
	if key := colKeyOf(m, "c"); key != "depth 1" {
		t.Fatalf("scoped to c: c in %q, want depth 1 (blocked by in-scope b)", key)
	}
	if key := colKeyOf(m, "d"); key != "depth 2" {
		t.Fatalf("scoped to c: d in %q, want depth 2", key)
	}
	// e is outside the neighborhood entirely.
	if key := colKeyOf(m, "e"); key != "" {
		t.Fatalf("scoped to c: e should be out of the neighborhood, found in %q", key)
	}
}
