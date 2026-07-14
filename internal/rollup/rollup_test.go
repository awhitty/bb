package rollup

import (
	"reflect"
	"testing"

	"github.com/awhitty/bb/internal/bd"
)

// Fixture board:
//
//	g-1   epic  open  P1           (parent of g-1.1 via parent link + parent-child dep)
//	g-1.1 task  in_progress P0     parent g-1
//	g-2   bug   open  P2           blocks-dep on g-3 (open) → blocked
//	g-3   task  open  P1           dependent_count 1 → blocking others
//	g-4   chore closed P3          → done
//	g-5   epic  open  P0
//	g-5.2 task  open  P1           NO parent link; dotted id → root g-5
//	g-6   task  open  P2           blocks-dep on g-4 (closed) → NOT blocked → free
func fixture() []bd.Issue {
	return []bd.Issue{
		{ID: "g-2", Title: "Blocked bug", Status: "open", Priority: 2, IssueType: "bug",
			Dependencies: []bd.Dependency{{IssueID: "g-2", DependsOnID: "g-3", Type: "blocks"}}},
		{ID: "g-1", Title: "Big epic", Status: "open", Priority: 1, IssueType: "epic", DependentCount: 0},
		{ID: "g-1.1", Title: "Epic child", Status: "in_progress", Priority: 0, IssueType: "task", Parent: "g-1",
			Dependencies: []bd.Dependency{{IssueID: "g-1.1", DependsOnID: "g-1", Type: "parent-child"}}},
		{ID: "g-3", Title: "Load-bearing task", Status: "open", Priority: 1, IssueType: "task", DependentCount: 1},
		{ID: "g-4", Title: "Done chore", Status: "closed", Priority: 3, IssueType: "chore"},
		{ID: "g-5", Title: "Dotted epic", Status: "open", Priority: 0, IssueType: "epic"},
		{ID: "g-5.2", Title: "Dotted orphan", Status: "open", Priority: 1, IssueType: "task"},
		{ID: "g-6", Title: "Dep on closed", Status: "open", Priority: 2, IssueType: "task",
			Dependencies: []bd.Dependency{{IssueID: "g-6", DependsOnID: "g-4", Type: "blocks"}}},
	}
}

func ids(issues []bd.Issue) []string {
	out := make([]string, len(issues))
	for i, is := range issues {
		out[i] = is.ID
	}
	return out
}

func colByKey(t *testing.T, cols []Column, key string) Column {
	t.Helper()
	for _, c := range cols {
		if c.Key == key {
			return c
		}
	}
	t.Fatalf("no column %q in %v", key, cols)
	return Column{}
}

func hasKey(cols []Column, key string) bool {
	for _, c := range cols {
		if c.Key == key {
			return true
		}
	}
	return false
}

func TestStatusMode(t *testing.T) {
	cols := Rollup(fixture(), ModeStatus)
	var keys []string
	for _, c := range cols {
		keys = append(keys, c.Key)
	}
	if !reflect.DeepEqual(keys, []string{"open", "in_progress", "closed"}) {
		t.Fatalf("status column order = %v", keys)
	}
	// Within a column: priority then id.
	open := colByKey(t, cols, "open")
	want := []string{"g-5", "g-1", "g-3", "g-5.2", "g-2", "g-6"}
	if !reflect.DeepEqual(ids(open.Issues), want) {
		t.Fatalf("open column = %v, want %v", ids(open.Issues), want)
	}
}

func TestTypeMode(t *testing.T) {
	cols := Rollup(fixture(), ModeType)
	var keys []string
	for _, c := range cols {
		keys = append(keys, c.Key)
	}
	// Canonical type order (containers first), never count: epic, task, bug, chore.
	if !reflect.DeepEqual(keys, []string{"epic", "task", "bug", "chore"}) {
		t.Fatalf("type column order = %v", keys)
	}
	// Untyped falls back to "untyped".
	cols = Rollup([]bd.Issue{{ID: "x", Status: "open"}}, ModeType)
	if cols[0].Key != "untyped" {
		t.Fatalf("untyped key = %q", cols[0].Key)
	}
}

func TestRootMode(t *testing.T) {
	cols := Rollup(fixture(), ModeRoot)
	g1 := colByKey(t, cols, "g-1")
	if g1.Title != "g-1 Big epic" {
		t.Fatalf("root column title = %q", g1.Title)
	}
	if !reflect.DeepEqual(ids(g1.Issues), []string{"g-1.1", "g-1"}) {
		t.Fatalf("g-1 column = %v (want child via parent link, sorted P0 first)", ids(g1.Issues))
	}
	// Dotted-id fallback: g-5.2 has no parent link but lands under g-5.
	g5 := colByKey(t, cols, "g-5")
	if !reflect.DeepEqual(ids(g5.Issues), []string{"g-5", "g-5.2"}) {
		t.Fatalf("g-5 column = %v (want dotted-id fallback)", ids(g5.Issues))
	}
	// Parentless single-member roots (g-2, g-3, g-4, g-6) no longer get their
	// own one-item section — they roll into ONE "standalone" bucket at the end.
	for _, key := range []string{"g-2", "g-3", "g-4", "g-6"} {
		if hasKey(cols, key) {
			t.Fatalf("%s should have rolled into the standalone bucket, not its own section", key)
		}
	}
	sa := colByKey(t, cols, StandaloneKey)
	if len(sa.Issues) != 4 {
		t.Fatalf("standalone bucket = %v (want the 4 singletons)", ids(sa.Issues))
	}
	if sa.Title != "standalone (4)" {
		t.Fatalf("standalone title = %q", sa.Title)
	}
	if cols[len(cols)-1].Key != StandaloneKey {
		t.Fatal("standalone bucket must be last")
	}
}

func TestRootModeParentCycleTerminates(t *testing.T) {
	issues := []bd.Issue{
		{ID: "a", Parent: "b", Status: "open"},
		{ID: "b", Parent: "a", Status: "open"},
	}
	Rollup(issues, ModeRoot) // must not hang
}

func TestBlockersMode(t *testing.T) {
	cols := Rollup(fixture(), ModeBlockers)
	var keys []string
	for _, c := range cols {
		keys = append(keys, c.Key)
	}
	if !reflect.DeepEqual(keys, []string{"blocked", "blocking others", "free", "done"}) {
		t.Fatalf("blockers column order = %v", keys)
	}
	if got := ids(colByKey(t, cols, "blocked").Issues); !reflect.DeepEqual(got, []string{"g-2"}) {
		t.Fatalf("blocked = %v (parent-child deps and deps-on-closed must not count)", got)
	}
	if got := ids(colByKey(t, cols, "blocking others").Issues); !reflect.DeepEqual(got, []string{"g-3"}) {
		t.Fatalf("blocking others = %v", got)
	}
	if got := ids(colByKey(t, cols, "done").Issues); !reflect.DeepEqual(got, []string{"g-4"}) {
		t.Fatalf("done = %v", got)
	}
	// g-1.1's only dep is parent-child; g-6's blocker is closed → both free.
	free := ids(colByKey(t, cols, "free").Issues)
	if !reflect.DeepEqual(free, []string{"g-1.1", "g-5", "g-1", "g-5.2", "g-6"}) {
		t.Fatalf("free = %v", free)
	}
}

func TestBuildTree(t *testing.T) {
	roots := BuildTree(fixture())
	byID := map[string]*TreeNode{}
	var walk func(ns []*TreeNode)
	walk = func(ns []*TreeNode) {
		for _, n := range ns {
			byID[n.Issue.ID] = n
			walk(n.Children)
		}
	}
	walk(roots)

	if len(roots) != 7 { // everything except g-1.1 (only real parent link)
		t.Fatalf("roots = %d", len(roots))
	}
	g1 := byID["g-1"]
	if len(g1.Children) != 1 || g1.Children[0].Issue.ID != "g-1.1" {
		t.Fatalf("g-1 children wrong: %+v", g1.Children)
	}
	// Roots sorted by priority then id: g-5 (P0) first.
	if roots[0].Issue.ID != "g-5" {
		t.Fatalf("first root = %s", roots[0].Issue.ID)
	}
}

func TestRootOfDeepChain(t *testing.T) {
	issues := []bd.Issue{
		{ID: "top", Status: "open"},
		{ID: "mid", Parent: "top", Status: "open"},
		{ID: "leaf", Parent: "mid", Status: "open"},
	}
	byID := map[string]bd.Issue{}
	for _, i := range issues {
		byID[i.ID] = i
	}
	if got := RootOf(byID["leaf"], byID); got.ID != "top" {
		t.Fatalf("RootOf(leaf) = %s", got.ID)
	}
}

func TestBuildDepsForest(t *testing.T) {
	issues := []bd.Issue{
		{ID: "a", Title: "root blocker", Status: "open", Priority: 1},
		{ID: "b", Title: "blocked by a", Status: "open", Priority: 0,
			Dependencies: []bd.Dependency{{IssueID: "b", DependsOnID: "a", Type: "blocks"}}},
		{ID: "c", Title: "blocked by a and b", Status: "open", Priority: 2,
			Dependencies: []bd.Dependency{
				{IssueID: "c", DependsOnID: "a", Type: "blocks"},
				{IssueID: "c", DependsOnID: "b", Type: "blocks"}}},
		{ID: "d", Title: "only parent-child dep", Status: "open", Priority: 1, Parent: "a",
			Dependencies: []bd.Dependency{{IssueID: "d", DependsOnID: "a", Type: "parent-child"}}},
		{ID: "e", Title: "dep on missing issue", Status: "open", Priority: 3,
			Dependencies: []bd.Dependency{{IssueID: "e", DependsOnID: "zzz", Type: "blocks"}}},
	}
	roots, extra := BuildDepsForest(issues)
	// Roots: a (no blockers), d (parent-child edges don't nest), e (blocker
	// off-board) — sorted by priority then id: a(P1), d(P1), e(P3).
	var rootIDs []string
	for _, r := range roots {
		rootIDs = append(rootIDs, r.Issue.ID)
	}
	if !reflect.DeepEqual(rootIDs, []string{"a", "d", "e"}) {
		t.Fatalf("roots = %v", rootIDs)
	}
	a := roots[0]
	if len(a.Children) != 2 || a.Children[0].Issue.ID != "b" || a.Children[1].Issue.ID != "c" {
		t.Fatalf("a's children wrong: %+v", a.Children)
	}
	// c hangs under its FIRST blocker (a) and reports one extra dep.
	if extra["c"] != 1 || extra["b"] != 0 {
		t.Fatalf("extra = %v", extra)
	}
}

func TestBuildDepsForestCycleSafe(t *testing.T) {
	issues := []bd.Issue{
		{ID: "x", Status: "open", Dependencies: []bd.Dependency{{IssueID: "x", DependsOnID: "y", Type: "blocks"}}},
		{ID: "y", Status: "open", Dependencies: []bd.Dependency{{IssueID: "y", DependsOnID: "z", Type: "blocks"}}},
		{ID: "z", Status: "open", Dependencies: []bd.Dependency{{IssueID: "z", DependsOnID: "x", Type: "blocks"}}},
		{ID: "self", Status: "open", Dependencies: []bd.Dependency{{IssueID: "self", DependsOnID: "self", Type: "blocks"}}},
	}
	roots, _ := BuildDepsForest(issues) // must not hang or drop nodes
	count := 0
	var walk func(ns []*TreeNode)
	walk = func(ns []*TreeNode) {
		for _, n := range ns {
			count++
			walk(n.Children)
		}
	}
	walk(roots)
	if count != 4 {
		t.Fatalf("cycle broke node count: %d of 4", count)
	}
	if len(roots) == 0 {
		t.Fatal("a cycle must yield at least one root")
	}
}

func TestBuildDepsForestReverse(t *testing.T) {
	// Same edges as TestBuildDepsForest, read the other way: nest each issue
	// under what it BLOCKS. a blocks b and c; b blocks c; c blocks nothing.
	issues := []bd.Issue{
		{ID: "a", Title: "blocks b and c", Status: "open", Priority: 1},
		{ID: "b", Title: "blocks c, blocked by a", Status: "open", Priority: 0,
			Dependencies: []bd.Dependency{{IssueID: "b", DependsOnID: "a", Type: "blocks"}}},
		{ID: "c", Title: "blocked by a and b", Status: "open", Priority: 2,
			Dependencies: []bd.Dependency{
				{IssueID: "c", DependsOnID: "a", Type: "blocks"},
				{IssueID: "c", DependsOnID: "b", Type: "blocks"}}},
		{ID: "d", Title: "only parent-child dep", Status: "open", Priority: 1, Parent: "a",
			Dependencies: []bd.Dependency{{IssueID: "d", DependsOnID: "a", Type: "parent-child"}}},
		{ID: "e", Title: "blocked by off-board zzz, blocks nothing", Status: "open", Priority: 3,
			Dependencies: []bd.Dependency{{IssueID: "e", DependsOnID: "zzz", Type: "blocks"}}},
	}
	roots, extra := BuildDepsForestReverse(issues)
	// Roots (block nothing on-board): c, d, e — sorted by priority then id:
	// d(P1), c(P2), e(P3).
	var rootIDs []string
	for _, r := range roots {
		rootIDs = append(rootIDs, r.Issue.ID)
	}
	if !reflect.DeepEqual(rootIDs, []string{"d", "c", "e"}) {
		t.Fatalf("roots = %v", rootIDs)
	}
	// c > b > a: a nests under b (its first dependent), b under c.
	c := roots[1]
	if len(c.Children) != 1 || c.Children[0].Issue.ID != "b" {
		t.Fatalf("c's children wrong: %+v", c.Children)
	}
	b := c.Children[0]
	if len(b.Children) != 1 || b.Children[0].Issue.ID != "a" {
		t.Fatalf("b's children wrong: %+v", b.Children)
	}
	// a blocks TWO (b, c) → hangs under the first, one extra reported.
	if extra["a"] != 1 || extra["b"] != 0 {
		t.Fatalf("extra = %v", extra)
	}
}

func TestBuildTreeReverse(t *testing.T) {
	// Hierarchy R1 > (R1.a > R1.a.x, R1.b). Inverted, leaves become roots and
	// the ancestors sink beneath their first descendant.
	issues := []bd.Issue{
		{ID: "R1", Status: "open", Priority: 0},
		{ID: "R1.a", Status: "open", Priority: 0, Parent: "R1"},
		{ID: "R1.b", Status: "open", Priority: 0, Parent: "R1"},
		{ID: "R1.a.x", Status: "open", Priority: 0, Parent: "R1.a"},
	}
	roots, extra := BuildTreeReverse(issues)
	// Roots (no children in the forward tree): R1.a.x, R1.b — all P0, so id order.
	var rootIDs []string
	for _, r := range roots {
		rootIDs = append(rootIDs, r.Issue.ID)
	}
	if !reflect.DeepEqual(rootIDs, []string{"R1.a.x", "R1.b"}) {
		t.Fatalf("roots = %v", rootIDs)
	}
	// R1.a.x > R1.a > R1: each ancestor nests under a descendant.
	x := roots[0]
	if len(x.Children) != 1 || x.Children[0].Issue.ID != "R1.a" {
		t.Fatalf("R1.a.x children wrong: %+v", x.Children)
	}
	a := x.Children[0]
	if len(a.Children) != 1 || a.Children[0].Issue.ID != "R1" {
		t.Fatalf("R1.a children wrong: %+v", a.Children)
	}
	// R1 has two children (R1.a, R1.b) → nests under the first, one extra reported.
	if extra["R1"] != 1 {
		t.Fatalf("extra = %v", extra)
	}
}

func TestReversedForestsCycleSafe(t *testing.T) {
	// A blocker cycle x→y→z→x and a self-loop must not hang or drop nodes in
	// either reversed builder (they share assembleForest's chain-walk cut).
	issues := []bd.Issue{
		{ID: "x", Status: "open", Dependencies: []bd.Dependency{{IssueID: "x", DependsOnID: "y", Type: "blocks"}}},
		{ID: "y", Status: "open", Dependencies: []bd.Dependency{{IssueID: "y", DependsOnID: "z", Type: "blocks"}}},
		{ID: "z", Status: "open", Dependencies: []bd.Dependency{{IssueID: "z", DependsOnID: "x", Type: "blocks"}}},
		{ID: "self", Status: "open", Parent: "self"},
	}
	count := func(roots []*TreeNode) int {
		n := 0
		var walk func(ns []*TreeNode)
		walk = func(ns []*TreeNode) {
			for _, node := range ns {
				n++
				walk(node.Children)
			}
		}
		walk(roots)
		return n
	}
	dr, _ := BuildDepsForestReverse(issues)
	if got := count(dr); got != 4 {
		t.Fatalf("deps-reverse cycle broke node count: %d of 4", got)
	}
	hr, _ := BuildTreeReverse(issues)
	if got := count(hr); got != 4 {
		t.Fatalf("hierarchy-reverse cycle broke node count: %d of 4", got)
	}
}

func TestFlatSortWithinColumn(t *testing.T) {
	issues := []bd.Issue{
		{ID: "a", Status: "open", Priority: 2, Title: "zeta", UpdatedAt: "2026-01-01"},
		{ID: "b", Status: "open", Priority: 0, Title: "alpha", UpdatedAt: "2026-03-01"},
		{ID: "c", Status: "open", Priority: 1, Title: "mid", UpdatedAt: "2026-02-01"},
	}
	cols := Rollup(issues, ModeStatus)
	// Priority (default): P0 first.
	SortColumns(cols, ModeStatus, Sort{Key: SortPriority, Desc: false}, Sort{})
	if got := ids(cols[0].Issues); !reflect.DeepEqual(got, []string{"b", "c", "a"}) {
		t.Fatalf("priority sort = %v", got)
	}
	// Title ascending.
	SortColumns(cols, ModeStatus, Sort{Key: SortTitle, Desc: false}, Sort{})
	if got := ids(cols[0].Issues); !reflect.DeepEqual(got, []string{"b", "c", "a"}) { // alpha, mid, zeta
		t.Fatalf("title sort = %v", got)
	}
	// Updated descending: newest first (b, c, a).
	SortColumns(cols, ModeStatus, Sort{Key: SortUpdated, Desc: true}, Sort{})
	if got := ids(cols[0].Issues); !reflect.DeepEqual(got, []string{"b", "c", "a"}) {
		t.Fatalf("updated-desc sort = %v", got)
	}
}

// colShape is the (column-key, ordered-id-list) skeleton of a set of columns —
// the byte-identity the board actually renders, independent of DupCount stamps
// and title decoration.
type colShape struct {
	Key string
	IDs []string
}

func shapeOf(cols []Column) []colShape {
	out := make([]colShape, len(cols))
	for i, c := range cols {
		out[i] = colShape{Key: c.Key, IDs: ids(c.Issues)}
	}
	return out
}

// TestGroupMatchesRollup pins byte-parity: for each legacy facet, Group +
// SortGrouped produce the exact same ordered column keys and per-column ordered
// ids as the old Rollup + SortColumns pipeline.
func TestGroupMatchesRollup(t *testing.T) {
	flat := Sort{Key: SortPriority, Desc: false}
	tree := Sort{Key: SortSubtreeSize, Desc: true}
	modes := []struct {
		mode  Mode
		facet Facet
	}{
		{ModeStatus, FacetStatus},
		{ModeType, FacetType},
		{ModeRoot, FacetRoot},
		{ModeBlockers, FacetBlockers},
	}
	for _, tc := range modes {
		old := Rollup(fixture(), tc.mode)
		SortColumns(old, tc.mode, flat, tree)

		got := Group(fixture(), tc.facet)
		got = SortGrouped(got, SectionSort{Facet: tc.facet, Tree: tree}, ItemSort{Flat: flat})

		if !reflect.DeepEqual(shapeOf(old), shapeOf(got)) {
			t.Fatalf("facet %s: shape mismatch\n old=%v\n new=%v", tc.facet, shapeOf(old), shapeOf(got))
		}
		// Legacy facets are single-valued: no issue is a duplicate.
		for _, c := range got {
			for _, is := range c.Issues {
				if is.DupCount != 0 {
					t.Fatalf("facet %s: %s DupCount=%d, want 0 (single-valued)", tc.facet, is.ID, is.DupCount)
				}
			}
		}
	}
}

// TestLabelFacetFanOut: a two-label issue lands in BOTH label columns and each
// copy reports DupCount==1; a single-label issue reports 0.
func TestLabelFacetFanOut(t *testing.T) {
	issues := []bd.Issue{
		{ID: "a", Status: "open", Priority: 1, Labels: []string{"bug", "ui"}},
		{ID: "b", Status: "open", Priority: 0, Labels: []string{"ui"}},
		{ID: "c", Status: "open", Priority: 2}, // no labels
	}
	cols := Group(issues, FacetLabel)
	cols = SortGrouped(cols, SectionSort{Facet: FacetLabel}, ItemSort{})

	ui := colByKey(t, cols, "ui")
	if !reflect.DeepEqual(ids(ui.Issues), []string{"b", "a"}) { // P0 then P1 within the section
		t.Fatalf("ui column = %v", ids(ui.Issues))
	}
	bug := colByKey(t, cols, "bug")
	if !reflect.DeepEqual(ids(bug.Issues), []string{"a"}) {
		t.Fatalf("bug column = %v", ids(bug.Issues))
	}
	// a appears in two columns → DupCount 1 in each; b in one → 0.
	for _, c := range cols {
		for _, is := range c.Issues {
			switch is.ID {
			case "a":
				if is.DupCount != 1 {
					t.Fatalf("a DupCount=%d in %q, want 1", is.DupCount, c.Key)
				}
			case "b":
				if is.DupCount != 0 {
					t.Fatalf("b DupCount=%d, want 0", is.DupCount)
				}
			}
		}
	}
	// The unlabeled issue still shows up under its own synthetic bucket.
	if got := ids(colByKey(t, cols, NoLabelKey).Issues); !reflect.DeepEqual(got, []string{"c"}) {
		t.Fatalf("unlabeled bucket = %v", got)
	}
}

// TestPriorityFacet: the priority facet is single-valued (one Pn key each), and
// sections sort by canonical rank (P0 first), never by card count.
func TestPriorityFacet(t *testing.T) {
	issues := []bd.Issue{
		{ID: "a", Status: "open", Priority: 1},
		{ID: "b", Status: "open", Priority: 1},
		{ID: "c", Status: "open", Priority: 0},
	}
	cols := Group(issues, FacetPriority)
	cols = SortGrouped(cols, SectionSort{Facet: FacetPriority}, ItemSort{})

	var keys []string
	for _, c := range cols {
		keys = append(keys, c.Key)
	}
	// P0 before P1 by canonical rank, even though P1 has the most cards.
	if !reflect.DeepEqual(keys, []string{"P0", "P1"}) {
		t.Fatalf("priority column order = %v", keys)
	}
	if got := ids(colByKey(t, cols, "P1").Issues); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("P1 column = %v", got)
	}
	for _, c := range cols {
		for _, is := range c.Issues {
			if is.DupCount != 0 {
				t.Fatalf("%s DupCount=%d, want 0 (single-valued)", is.ID, is.DupCount)
			}
		}
	}
}

// blocks is a one-edge "id is blocked by on" dependency, for the depth fixtures.
func blocks(id, on string) []bd.Dependency {
	return []bd.Dependency{{IssueID: id, DependsOnID: on, Type: "blocks"}}
}

func depthKeys(cols []Column) []string {
	var keys []string
	for _, c := range cols {
		keys = append(keys, c.Key)
	}
	return keys
}

// TestDepthFacetChain: a chain a←b←c (b blocked by a, c by b) groups into three
// depth columns ready → depth 1 → depth 2, each holding exactly its rank's issue.
func TestDepthFacetChain(t *testing.T) {
	issues := []bd.Issue{
		{ID: "a", Status: "open", Priority: 1},
		{ID: "b", Status: "open", Priority: 1, Dependencies: blocks("b", "a")},
		{ID: "c", Status: "open", Priority: 1, Dependencies: blocks("c", "b")},
	}
	cols := SortGrouped(Group(issues, FacetDepth), SectionSort{Facet: FacetDepth}, ItemSort{})
	if got := depthKeys(cols); !reflect.DeepEqual(got, []string{"ready", "depth 1", "depth 2"}) {
		t.Fatalf("depth column order = %v", got)
	}
	if got := ids(colByKey(t, cols, "ready").Issues); !reflect.DeepEqual(got, []string{"a"}) {
		t.Fatalf("ready column = %v, want [a]", got)
	}
	if got := ids(colByKey(t, cols, "depth 1").Issues); !reflect.DeepEqual(got, []string{"b"}) {
		t.Fatalf("depth 1 column = %v, want [b]", got)
	}
	if got := ids(colByKey(t, cols, "depth 2").Issues); !reflect.DeepEqual(got, []string{"c"}) {
		t.Fatalf("depth 2 column = %v, want [c]", got)
	}
	// Single-valued: no card is a duplicate.
	for _, c := range cols {
		for _, is := range c.Issues {
			if is.DupCount != 0 {
				t.Fatalf("%s DupCount=%d, want 0 (single-valued)", is.ID, is.DupCount)
			}
		}
	}
}

// TestDepthFacetClosedBlockerReady: a closed blocker does not gate, so its
// dependent lands in the ready front rather than opening a depth-1 column.
func TestDepthFacetClosedBlockerReady(t *testing.T) {
	issues := []bd.Issue{
		{ID: "a", Status: "closed", Priority: 1},
		{ID: "b", Status: "open", Priority: 1, Dependencies: blocks("b", "a")},
	}
	cols := SortGrouped(Group(issues, FacetDepth), SectionSort{Facet: FacetDepth}, ItemSort{})
	if hasKey(cols, "depth 1") {
		t.Fatalf("a closed blocker must not open a depth-1 column: %v", depthKeys(cols))
	}
	if got := ids(colByKey(t, cols, "ready").Issues); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("ready column = %v, want [a b] (closed blocker doesn't gate)", got)
	}
}

// TestDepthFacetNeighborhood: depth ranks are relative to the VISIBLE set. Over
// the whole chain a←b←c←d, d is depth 3; over the neighborhood {c, d} alone —
// c's blocker b is out of the set — c is ready and d is depth 1. An out-of-set
// blocker doesn't gate, exactly like a closed one, which is what lets a scoped
// board read depth within its neighborhood.
func TestDepthFacetNeighborhood(t *testing.T) {
	full := []bd.Issue{
		{ID: "a", Status: "open"},
		{ID: "b", Status: "open", Dependencies: blocks("b", "a")},
		{ID: "c", Status: "open", Dependencies: blocks("c", "b")},
		{ID: "d", Status: "open", Dependencies: blocks("d", "c")},
	}
	whole := SortGrouped(Group(full, FacetDepth), SectionSort{Facet: FacetDepth}, ItemSort{})
	if got := ids(colByKey(t, whole, "depth 3").Issues); !reflect.DeepEqual(got, []string{"d"}) {
		t.Fatalf("whole-board d = %v, want depth 3", got)
	}

	neighborhood := full[2:] // c, d only
	cols := SortGrouped(Group(neighborhood, FacetDepth), SectionSort{Facet: FacetDepth}, ItemSort{})
	if got := depthKeys(cols); !reflect.DeepEqual(got, []string{"ready", "depth 1"}) {
		t.Fatalf("neighborhood depth columns = %v, want [ready, depth 1]", got)
	}
	if got := ids(colByKey(t, cols, "ready").Issues); !reflect.DeepEqual(got, []string{"c"}) {
		t.Fatalf("neighborhood ready = %v, want [c] (out-of-set blocker doesn't gate)", got)
	}
}

// TestDepthFacetCycleSafe: a dependency cycle x→y→z→x must not hang, and every
// member still lands in a column with a finite rank.
func TestDepthFacetCycleSafe(t *testing.T) {
	issues := []bd.Issue{
		{ID: "x", Status: "open", Dependencies: blocks("x", "y")},
		{ID: "y", Status: "open", Dependencies: blocks("y", "z")},
		{ID: "z", Status: "open", Dependencies: blocks("z", "x")},
	}
	cols := SortGrouped(Group(issues, FacetDepth), SectionSort{Facet: FacetDepth}, ItemSort{}) // must not hang
	total := 0
	for _, c := range cols {
		total += len(c.Issues)
	}
	if total != 3 {
		t.Fatalf("cycle dropped nodes: %d of 3 placed across %v", total, depthKeys(cols))
	}
}

// TestDepthFacetOpenChildrenGateParent: an epic with two open children (one a
// leaf at depth 0, one blocked at depth 1) ranks at depth 2 and stays out of the
// ready front — bd never lists a parent with open children as ready, so open
// children gate a parent exactly like open blockers.
func TestDepthFacetOpenChildrenGateParent(t *testing.T) {
	issues := []bd.Issue{
		{ID: "E", Status: "open", Priority: 1, IssueType: "epic"},
		{ID: "C0", Status: "open", Priority: 1, Parent: "E"},                                  // leaf child → depth 0
		{ID: "C1", Status: "open", Priority: 1, Parent: "E", Dependencies: blocks("C1", "X")}, // → depth 1
		{ID: "X", Status: "open", Priority: 1},                                                // depth 0
	}
	cols := SortGrouped(Group(issues, FacetDepth), SectionSort{Facet: FacetDepth}, ItemSort{})
	// The epic is one past its deepest open child (max(0,1)=1 → depth 2).
	if got := ids(colByKey(t, cols, "depth 2").Issues); !reflect.DeepEqual(got, []string{"E"}) {
		t.Fatalf("depth 2 column = %v, want [E] (epic gated by its open children)", got)
	}
	// The ready front is genuinely workable leaves only — E is not among them.
	ready := ids(colByKey(t, cols, "ready").Issues)
	if !reflect.DeepEqual(ready, []string{"C0", "X"}) {
		t.Fatalf("ready column = %v, want [C0 X] (workable leaves only, no parent)", ready)
	}
}

// TestDepthFacetClosingChildrenMovesParentToReady: closing every child of the
// epic ungates it — with no open children and no blockers it drops to the ready
// front. A closed child does not gate, the mirror of a closed blocker.
func TestDepthFacetClosingChildrenMovesParentToReady(t *testing.T) {
	issues := []bd.Issue{
		{ID: "E", Status: "open", Priority: 1, IssueType: "epic"},
		{ID: "C0", Status: "closed", Priority: 1, Parent: "E"},
		{ID: "C1", Status: "closed", Priority: 1, Parent: "E"},
	}
	cols := SortGrouped(Group(issues, FacetDepth), SectionSort{Facet: FacetDepth}, ItemSort{})
	if hasKey(cols, "depth 1") {
		t.Fatalf("closed children must not gate the parent: %v", depthKeys(cols))
	}
	ready := ids(colByKey(t, cols, "ready").Issues)
	if !reflect.DeepEqual(ready, []string{"C0", "C1", "E"}) {
		t.Fatalf("ready column = %v, want [C0 C1 E] (closed children don't gate; within-section sort is priority-then-id)", ready)
	}
}

// TestDepthFacetChildDependencyChain: gates compose. An epic's only child sits
// atop a two-deep blocker chain (A←B←child), so the child is depth 2 and the
// epic is one deeper — the apex lands at max-subtree-depth + 1.
func TestDepthFacetChildDependencyChain(t *testing.T) {
	issues := []bd.Issue{
		{ID: "A", Status: "open", Priority: 1},
		{ID: "B", Status: "open", Priority: 1, Dependencies: blocks("B", "A")},
		{ID: "child", Status: "open", Priority: 1, Parent: "E", Dependencies: blocks("child", "B")},
		{ID: "E", Status: "open", Priority: 1, IssueType: "epic"},
	}
	cols := SortGrouped(Group(issues, FacetDepth), SectionSort{Facet: FacetDepth}, ItemSort{})
	// A=0, B=1, child=2, E=1+deepest(child=2)=3.
	if got := ids(colByKey(t, cols, "depth 2").Issues); !reflect.DeepEqual(got, []string{"child"}) {
		t.Fatalf("depth 2 column = %v, want [child]", got)
	}
	if got := ids(colByKey(t, cols, "depth 3").Issues); !reflect.DeepEqual(got, []string{"E"}) {
		t.Fatalf("depth 3 column = %v, want [E] (apex = deepest descendant + 1)", got)
	}
}

func TestTreeSortBySubtreeSize(t *testing.T) {
	// root R1 has 2 children; R2 has 0. Subtree-size desc puts R1 first.
	issues := []bd.Issue{
		{ID: "R2", Status: "open"},
		{ID: "R1", Status: "open"},
		{ID: "R1.a", Parent: "R1", Status: "open"},
		{ID: "R1.b", Parent: "R1", Status: "open"},
	}
	roots := BuildTree(issues)
	SortForestBy(roots, Sort{Key: SortSubtreeSize, Desc: true})
	if roots[0].Issue.ID != "R1" {
		t.Fatalf("subtree-size desc: first root = %q, want R1", roots[0].Issue.ID)
	}
}

// flattenTree renders a forest as {id, depth, stub} in pre-order for structural
// assertions.
func flattenTree(roots []*TreeNode) []struct {
	id    string
	depth int
	stub  bool
} {
	var out []struct {
		id    string
		depth int
		stub  bool
	}
	var walk func(n *TreeNode, d int)
	walk = func(n *TreeNode, d int) {
		out = append(out, struct {
			id    string
			depth int
			stub  bool
		}{n.Issue.ID, d, n.Stub})
		for _, c := range n.Children {
			walk(c, d+1)
		}
	}
	for _, r := range roots {
		walk(r, 0)
	}
	return out
}

// A visible child of a hidden (closed) parent stays rooted under a dimmed stub
// of that parent rather than surfacing as a fake root; the stub carries no
// subtree weight of its own (size/open reflect the real children, and it never
// wins the priority min).
func TestBuildTreeStubbedRootsHiddenParent(t *testing.T) {
	board := map[string]bd.Issue{
		"p":   {ID: "p", Title: "Closed parent", Status: bd.StatusClosed, Priority: 1},
		"p.1": {ID: "p.1", Title: "child a", Status: bd.StatusOpen, Priority: 1, Parent: "p"},
		"p.2": {ID: "p.2", Title: "child b", Status: bd.StatusOpen, Priority: 0, Parent: "p"},
	}
	visible := []bd.Issue{board["p.1"], board["p.2"]}
	roots := BuildTreeStubbed(visible, board)
	if len(roots) != 1 {
		t.Fatalf("want 1 root (the stub), got %d", len(roots))
	}
	st := roots[0]
	if !st.Stub || st.Issue.ID != "p" {
		t.Fatalf("root should be stub p, got stub=%v id=%s", st.Stub, st.Issue.ID)
	}
	if len(st.Children) != 2 {
		t.Fatalf("stub should hold 2 real children, got %d", len(st.Children))
	}
	for _, c := range st.Children {
		if c.Stub {
			t.Fatalf("child %s wrongly marked stub", c.Issue.ID)
		}
	}
	mets := computeMetrics(roots)
	if got := mets["p"]; got.size != 2 || got.open != 2 || got.aggPri != 0 {
		t.Fatalf("stub metric = %+v, want size 2, open 2, aggPri 0 (stub adds nothing)", got)
	}
}

// A two-level hidden chain materializes BOTH ancestors as stubs, in order, with
// the visible leaf nested two deep under its true lineage.
func TestBuildTreeStubbedTwoLevelChain(t *testing.T) {
	board := map[string]bd.Issue{
		"a":     {ID: "a", Title: "gp", Status: bd.StatusClosed, Priority: 1},
		"a.b":   {ID: "a.b", Title: "parent", Status: bd.StatusClosed, Priority: 1, Parent: "a"},
		"a.b.c": {ID: "a.b.c", Title: "leaf", Status: bd.StatusOpen, Priority: 1, Parent: "a.b"},
	}
	roots := BuildTreeStubbed([]bd.Issue{board["a.b.c"]}, board)
	got := flattenTree(roots)
	want := []struct {
		id    string
		depth int
		stub  bool
	}{{"a", 0, true}, {"a.b", 1, true}, {"a.b.c", 2, false}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("two-level chain = %+v, want %+v", got, want)
	}
}

// The deps forest gets the same treatment: an issue whose nesting blocker is
// hidden (closed) hangs under a stub of that blocker instead of rooting.
func TestBuildDepsForestStubbedHiddenBlocker(t *testing.T) {
	board := map[string]bd.Issue{
		"blk": {ID: "blk", Title: "closed blocker", Status: bd.StatusClosed, Priority: 1},
		"x": {ID: "x", Title: "waiter", Status: bd.StatusOpen, Priority: 1,
			Dependencies: []bd.Dependency{{IssueID: "x", DependsOnID: "blk", Type: "blocks"}}},
	}
	roots, _ := BuildDepsForestStubbed([]bd.Issue{board["x"]}, board)
	if len(roots) != 1 || !roots[0].Stub || roots[0].Issue.ID != "blk" {
		t.Fatalf("waiter should nest under a stub of its closed blocker, got %+v", flattenTree(roots))
	}
	if len(roots[0].Children) != 1 || roots[0].Children[0].Issue.ID != "x" {
		t.Fatalf("x should hang under the stub blocker, got %+v", flattenTree(roots))
	}
}

// With every parent visible, BuildTreeStubbed is structurally identical to
// BuildTree (no stubs invented).
func TestBuildTreeStubbedIdentityWhenAllVisible(t *testing.T) {
	issues := fixture()
	board := index(issues)
	if got, want := flattenTree(BuildTreeStubbed(issues, board)), flattenTree(BuildTree(issues)); !reflect.DeepEqual(got, want) {
		t.Fatalf("stubbed forest diverged from BuildTree with all parents visible:\n got %+v\nwant %+v", got, want)
	}
}
