package ui

import (
	"strings"
	"testing"

	"github.com/awhitty/bb/internal/bd"
	"github.com/awhitty/bb/internal/rollup"
)

// facetTreeFixture: an epic whose two children span two statuses, and whose
// in_progress child has two grandchildren that also span two statuses — so
// facet segmentation is exercised at depth 1 AND depth 2.
func facetTreeFixture() []bd.Issue {
	return []bd.Issue{
		{ID: "g-1", Title: "Epic", Status: bd.StatusInProgress, Priority: 1, IssueType: "epic"},
		{ID: "g-1.1", Title: "child open", Status: bd.StatusOpen, Priority: 1, IssueType: "task", Parent: "g-1"},
		{ID: "g-1.2", Title: "child inprog", Status: bd.StatusInProgress, Priority: 1, IssueType: "task", Parent: "g-1"},
		{ID: "g-1.2.1", Title: "gc open", Status: bd.StatusOpen, Priority: 1, IssueType: "task", Parent: "g-1.2"},
		{ID: "g-1.2.2", Title: "gc inprog", Status: bd.StatusInProgress, Priority: 1, IssueType: "task", Parent: "g-1.2"},
	}
}

// With no facet the tree is byte-unchanged (no row leads a section); with the
// status facet the children of every parent render segmented in statusOrder,
// with the box-drawing connectors still valid and the two-level sort applied
// recursively (depth ≥ 2 segments too).
func TestTreeFacetSegmentsChildren(t *testing.T) {
	m := testModel(t, facetTreeFixture(), 120, 30)
	m = press(t, m, "5") // tree view
	mp := &m

	// facet none (default): nothing is segmented.
	if mp.treeFacet != "" {
		t.Fatalf("default treeFacet = %q, want empty", mp.treeFacet)
	}
	for _, r := range mp.treeRows {
		if r.section != "" {
			t.Fatalf("row %s carries a section %q with no facet active", r.issue.ID, r.section)
		}
	}
	frameLines(t, m, 120, 30)

	// Turn on the status facet.
	mp.treeFacet = rollup.FacetStatus
	mp.rebuild()

	idx := map[string]int{}
	sect := map[string]string{}
	pref := map[string]string{}
	for i, r := range mp.treeRows {
		idx[r.issue.ID] = i
		sect[r.issue.ID] = r.section
		pref[r.issue.ID] = r.prefix
	}
	if len(mp.treeRows) != 5 {
		t.Fatalf("segmented tree has %d rows, want 5", len(mp.treeRows))
	}

	// Depth 1: the open child sorts ahead of the in_progress child (statusOrder),
	// and the in_progress child LEADS its section.
	if idx["g-1.1"] > idx["g-1.2"] {
		t.Fatalf("open child g-1.1 (idx %d) should precede in_progress g-1.2 (idx %d)", idx["g-1.1"], idx["g-1.2"])
	}
	if sect["g-1.1"] != "" {
		t.Fatalf("first section's lead g-1.1 should carry no separator, got %q", sect["g-1.1"])
	}
	if sect["g-1.2"] != bd.StatusInProgress {
		t.Fatalf("g-1.2 should lead the %q section, got %q", bd.StatusInProgress, sect["g-1.2"])
	}

	// Depth 2: the grandchildren segment the same way, proving the two-level sort
	// is applied recursively rather than only at the flat root.
	if idx["g-1.2.1"] > idx["g-1.2.2"] {
		t.Fatalf("open gc g-1.2.1 should precede in_progress gc g-1.2.2")
	}
	if sect["g-1.2.1"] != "" {
		t.Fatalf("g-1.2.1 should not lead a section, got %q", sect["g-1.2.1"])
	}
	if sect["g-1.2.2"] != bd.StatusInProgress {
		t.Fatalf("depth-2 lead g-1.2.2 should carry the %q separator, got %q", bd.StatusInProgress, sect["g-1.2.2"])
	}

	// The connectors stay valid: the last sibling of each group renders └─, the
	// earlier ones ├─, regardless of the section boundaries between them.
	if !strings.HasSuffix(pref["g-1.1"], "├─ ") {
		t.Fatalf("g-1.1 connector = %q, want ├─ suffix", pref["g-1.1"])
	}
	if !strings.HasSuffix(pref["g-1.2"], "└─ ") {
		t.Fatalf("g-1.2 (last child) connector = %q, want └─ suffix", pref["g-1.2"])
	}
	if !strings.HasSuffix(pref["g-1.2.2"], "└─ ") {
		t.Fatalf("g-1.2.2 (last gc) connector = %q, want └─ suffix", pref["g-1.2.2"])
	}

	// The whole segmented frame still fits exactly (separators are extra lines
	// that the page window absorbs; no overflow, no line-count drift).
	frameLines(t, m, 120, 30)
}

// A separator line appears in the rendered tree between the two sibling groups,
// and the navigation cursor still lands on issue rows (separators aren't
// focusable — m.treeIdx indexes only issue rows).
func TestTreeFacetSeparatorRendersAndCursorSkipsIt(t *testing.T) {
	m := testModel(t, facetTreeFixture(), 120, 30)
	m = press(t, m, "5")
	mp := &m
	mp.treeFacet = rollup.FacetStatus
	mp.rebuild()
	mp.jumpTo("g-1.2") // the in_progress section lead

	view := strings.Join(frameLines(t, m, 120, 30), "\n")
	if !strings.Contains(view, bd.StatusInProgress) {
		t.Fatalf("expected an in_progress section separator in the rendered tree:\n%s", view)
	}
	// The cursor is on an issue row, never a separator.
	if mp.focusedIssue() == nil || mp.focusedIssue().ID != "g-1.2" {
		t.Fatalf("cursor should rest on issue g-1.2, got %v", mp.focusedIssue())
	}
}

// treeFixture: an epic with a child that itself has a grandchild, plus a loner.
func treeFixture() []bd.Issue {
	return []bd.Issue{
		{ID: "g-1", Title: "Epic", Status: "open", Priority: 1, IssueType: "epic"},
		{ID: "g-1.1", Title: "Child", Status: "open", Priority: 0, IssueType: "task", Parent: "g-1"},
		{ID: "g-1.1.1", Title: "Grandchild", Status: "open", Priority: 2, IssueType: "task", Parent: "g-1.1"},
		{ID: "g-2", Title: "Loner", Status: "open", Priority: 3, IssueType: "chore"},
	}
}

// In the tree, left folds an expanded parent (and right unfolds it); with
// nothing to fold, left walks up to the parent and right steps into the child.
func TestTreeLeftRightCollapseExpand(t *testing.T) {
	m := testModel(t, treeFixture(), 120, 30)
	m = press(t, m, "5") // tree view
	// Focus the epic (row 0).
	m.treeIdx = 0
	mp := &m
	rowID := func() string { return mp.treeRows[mp.treeIdx].issue.ID }

	// Right on the epic steps into its first child.
	mp.treeExpandFocused()
	if rowID() != "g-1.1" {
		t.Fatalf("right on epic → %q, want g-1.1", rowID())
	}

	// Left on the child (an expanded parent) folds it; focus stays on it, and
	// the grandchild is now hidden.
	mp.treeCollapseFocused()
	if rowID() != "g-1.1" {
		t.Fatalf("collapse moved focus to %q", rowID())
	}
	if !mp.collapsed["g-1.1"] {
		t.Fatal("child was not collapsed")
	}
	for _, r := range mp.treeRows {
		if r.issue.ID == "g-1.1.1" {
			t.Fatal("grandchild still visible after collapsing its parent")
		}
	}

	// Right unfolds the child; the grandchild is back.
	mp.treeExpandFocused()
	if mp.collapsed["g-1.1"] {
		t.Fatal("child still collapsed after expand")
	}
	found := false
	for _, r := range mp.treeRows {
		if r.issue.ID == "g-1.1.1" {
			found = true
		}
	}
	if !found {
		t.Fatal("grandchild not restored after expand")
	}

	// Left on a leaf walks up to the parent.
	mp.jumpTo("g-1.1.1")
	mp.treeCollapseFocused()
	if rowID() != "g-1.1" {
		t.Fatalf("left on a leaf → %q, want parent g-1.1", rowID())
	}
}

// Holding ← from a deep node folds the whole branch to its root (collapse an
// expanded parent, else walk up, else move up a row — never stalls).
func TestTreeHoldLeftCollapsesToRoot(t *testing.T) {
	m := testModel(t, treeFixture(), 120, 30)
	m = press(t, m, "5")
	mp := &m
	mp.jumpTo("g-1.1.1") // start deep

	for i := 0; i < 8; i++ { // "hold" ←
		m = press(t, m, "h")
		mp = &m
	}
	if !mp.collapsed["g-1"] {
		t.Fatal("holding ← should fold the branch to its root g-1")
	}
	for _, r := range mp.treeRows {
		if r.issue.ID == "g-1.1" || r.issue.ID == "g-1.1.1" {
			t.Fatalf("%s still visible under the collapsed root", r.issue.ID)
		}
	}
	if mp.focusedIssue().ID != "g-1" {
		t.Fatalf("focus should rest on the root, got %s", mp.focusedIssue().ID)
	}
}

// Holding → from a collapsed root unfurls the branch and descends into it
// (unfold, else step into the first child, else move down a row).
func TestTreeHoldRightUnfurls(t *testing.T) {
	m := testModel(t, treeFixture(), 120, 30)
	m = press(t, m, "5")
	mp := &m
	mp.collapsed["g-1"] = true
	mp.rebuild()
	mp.jumpTo("g-1") // start on the collapsed root

	for i := 0; i < 4; i++ { // "hold" →
		m = press(t, m, "l")
		mp = &m
	}
	if mp.collapsed["g-1"] {
		t.Fatal("holding → should have unfolded g-1")
	}
	seen := map[string]bool{}
	for _, r := range mp.treeRows {
		seen[r.issue.ID] = true
	}
	if !seen["g-1.1"] || !seen["g-1.1.1"] {
		t.Fatal("holding → should have unfurled the whole branch")
	}
	if mp.focusedIssue().ID == "g-1" {
		t.Fatal("holding → should have descended past the root")
	}
}

func TestTreeZCollapsesEveryBranch(t *testing.T) {
	m := testModel(t, treeFixture(), 120, 30)
	m = press(t, m, "5", "Z")
	mp := &m
	if !mp.collapsed["g-1"] || !mp.collapsed["g-1.1"] {
		t.Fatalf("Z should fold root and nested branch, got collapsed=%v", mp.collapsed)
	}

	mp.jumpTo("g-1")
	mp.treeExpandFocused()
	if mp.collapsed["g-1"] {
		t.Fatal("expanding the root should unfold only the focused root")
	}
	if !mp.collapsed["g-1.1"] {
		t.Fatal("nested branch should remain folded after expanding the root")
	}
	for _, r := range mp.treeRows {
		if r.issue.ID == "g-1.1.1" {
			t.Fatal("grandchild should stay hidden under the still-folded nested branch")
		}
	}

	m = press(t, *mp, "Z")
	if len(m.collapsed) != 0 {
		t.Fatalf("second Z should expand all, got collapsed=%v", m.collapsed)
	}
}

// loadSplit builds a model whose VISIBLE set (m.issues) differs from the whole
// board (m.graph) — the closed-toggle state where a closed parent stays on the
// board but is excluded from the rendered set.
func loadSplit(t *testing.T, visible, graph []bd.Issue, w, h int) Model {
	t.Helper()
	m := testModel(t, graph, w, h)
	next, _ := m.Update(issuesMsg{seq: 0, issues: visible, graph: graph})
	return next.(Model)
}

func rowByID(rows []treeRow) map[string]treeRow {
	m := map[string]treeRow{}
	for _, r := range rows {
		m[r.issue.ID] = r
	}
	return m
}

// (a) An open child of a CLOSED parent renders indented under a dimmed (closed)
// stub of that parent, not at the root. (c) Toggling closed-visible replaces the
// stub with the real row.
func TestTreeHiddenClosedParentRendersAsStub(t *testing.T) {
	graph := []bd.Issue{
		{ID: "g-1", Title: "The secrets story", Status: bd.StatusClosed, Priority: 1, IssueType: "epic"},
		{ID: "g-1.1", Title: "child one", Status: bd.StatusOpen, Priority: 1, IssueType: "task", Parent: "g-1"},
		{ID: "g-1.2", Title: "child two", Status: bd.StatusOpen, Priority: 0, IssueType: "task", Parent: "g-1"},
	}
	visible := []bd.Issue{graph[1], graph[2]}
	m := loadSplit(t, visible, graph, 120, 30)
	m = press(t, m, "5")
	mp := &m

	rows := rowByID(mp.treeRows)
	st, ok := rows["g-1"]
	if !ok || !st.stub {
		t.Fatalf("g-1 (closed parent) should render as a stub row, rows=%v", mp.treeRows)
	}
	if st.depth != 0 {
		t.Fatalf("stub g-1 should be at root depth, got %d", st.depth)
	}
	if rows["g-1.1"].depth != 1 || rows["g-1.2"].depth != 1 {
		t.Fatalf("children should be indented under the stub, got depths %d/%d", rows["g-1.1"].depth, rows["g-1.2"].depth)
	}
	if rows["g-1.1"].stub || rows["g-1.2"].stub {
		t.Fatal("real children must not be marked stub")
	}
	view := strings.Join(frameLines(t, m, 120, 30), "\n")
	if !strings.Contains(view, "The secrets story (closed)") {
		t.Fatalf("expected the dimmed closed-stub label in the tree:\n%s", view)
	}

	// (c) closed included in the visible set → g-1 is a real row, not a stub.
	wc := loadSplit(t, graph, graph, 120, 30)
	wc = press(t, wc, "5")
	for _, r := range wc.treeRows {
		if r.issue.ID == "g-1" && r.stub {
			t.Fatal("with closed visible, g-1 should be a real row, not a stub")
		}
	}
}

// (b) A two-level hidden chain renders both stubs, in order, above the leaf.
func TestTreeTwoLevelHiddenChainRendersBothStubs(t *testing.T) {
	graph := []bd.Issue{
		{ID: "g-1", Title: "grandparent", Status: bd.StatusClosed, Priority: 1, IssueType: "epic"},
		{ID: "g-1.1", Title: "parent", Status: bd.StatusClosed, Priority: 1, IssueType: "feature", Parent: "g-1"},
		{ID: "g-1.1.1", Title: "leaf", Status: bd.StatusOpen, Priority: 1, IssueType: "task", Parent: "g-1.1"},
	}
	m := loadSplit(t, []bd.Issue{graph[2]}, graph, 120, 30)
	m = press(t, m, "5")
	mp := &m
	if len(mp.treeRows) != 3 {
		t.Fatalf("want 3 rows (2 stubs + leaf), got %d: %v", len(mp.treeRows), mp.treeRows)
	}
	want := []struct {
		id    string
		stub  bool
		depth int
	}{{"g-1", true, 0}, {"g-1.1", true, 1}, {"g-1.1.1", false, 2}}
	for i, w := range want {
		r := mp.treeRows[i]
		if r.issue.ID != w.id || r.stub != w.stub || r.depth != w.depth {
			t.Fatalf("row %d = {%s stub=%v depth=%d}, want {%s stub=%v depth=%d}",
				i, r.issue.ID, r.stub, r.depth, w.id, w.stub, w.depth)
		}
	}
}

// (d) The deps forest gets the same anti-orphan behavior: a waiter whose blocker
// is closed nests under a dimmed stub of that blocker.
func TestTreeDepsHiddenBlockerRendersAsStub(t *testing.T) {
	graph := []bd.Issue{
		{ID: "g-1", Title: "closed blocker", Status: bd.StatusClosed, Priority: 1, IssueType: "task"},
		{ID: "g-2", Title: "waiter", Status: bd.StatusOpen, Priority: 1, IssueType: "task",
			Dependencies: []bd.Dependency{{IssueID: "g-2", DependsOnID: "g-1", Type: "blocks"}}},
	}
	m := loadSplit(t, []bd.Issue{graph[1]}, graph, 120, 30)
	m = press(t, m, "5")
	mp := &m
	mp.depsTree = true
	mp.rebuild()

	rows := rowByID(mp.treeRows)
	st, ok := rows["g-1"]
	if !ok || !st.stub || st.depth != 0 {
		t.Fatalf("deps: the closed blocker should be a root stub, rows=%v", mp.treeRows)
	}
	if rows["g-2"].depth != 1 {
		t.Fatalf("deps: the waiter should nest under the stub blocker, got depth %d", rows["g-2"].depth)
	}
}

// (e) Stubs are excluded from subtree counts: the folded +N and the min-subtree
// filter both reflect only the real descendants, and the filter drops the whole
// subtree rather than promoting children past a hidden ancestor.
func TestTreeStubExcludedFromCounts(t *testing.T) {
	graph := []bd.Issue{
		{ID: "g-1", Title: "closed epic", Status: bd.StatusClosed, Priority: 1, IssueType: "epic"},
		{ID: "g-1.1", Title: "c1", Status: bd.StatusOpen, Priority: 1, IssueType: "task", Parent: "g-1"},
		{ID: "g-1.2", Title: "c2", Status: bd.StatusOpen, Priority: 0, IssueType: "task", Parent: "g-1"},
	}
	visible := []bd.Issue{graph[1], graph[2]}
	m := loadSplit(t, visible, graph, 120, 30)
	m = press(t, m, "5")
	mp := &m

	// Fold the stub root: the +N count is the 2 REAL children, never 3.
	mp.collapsed["g-1"] = true
	mp.rebuild()
	rows := rowByID(mp.treeRows)
	st := rows["g-1"]
	if !st.collapsed {
		t.Fatal("g-1 stub should be collapsed")
	}
	if st.hidden != 2 {
		t.Fatalf("folded stub +N = %d, want 2 (real descendants only)", st.hidden)
	}

	// min-subtree counts real descendants (2), keeping the subtree at threshold 2
	// and dropping the WHOLE subtree at 3 — never promoting the children.
	mp.collapsed = map[string]bool{}
	mp.minSubtree = 2
	mp.rebuild()
	if len(mp.treeRows) == 0 {
		t.Fatal("min-subtree 2 should keep the 2-child subtree")
	}
	mp.minSubtree = 3
	mp.rebuild()
	for _, r := range mp.treeRows {
		if r.issue.ID == "g-1.1" || r.issue.ID == "g-1.2" {
			t.Fatal("min-subtree 3 should drop the whole subtree, not promote children")
		}
	}
}
