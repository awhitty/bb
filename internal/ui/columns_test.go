package ui

import (
	"strings"
	"testing"

	"github.com/awhitty/bb/internal/agentapi"
	"github.com/awhitty/bb/internal/bd"
)

// millerSelect points the focused column at the card with id (test helper).
func millerSelect(t *testing.T, mp *Model, id string) {
	t.Helper()
	lay := mp.millerBuild()
	focus := lay.cols[lay.focusCol]
	for i, is := range focus {
		if is.ID == id {
			mp.millerSel = i
			return
		}
	}
	t.Fatalf("%s not in the focused column", id)
}

// chainFixture is a straight hierarchy r0 → a → b → c → d → e for drill/window
// tests, plus a second root z0 so column 0 has more than one card.
func chainFixture() []bd.Issue {
	pc := func(id, parent string) bd.Dependency {
		return bd.Dependency{IssueID: id, DependsOnID: parent, Type: "parent-child"}
	}
	mk := func(id, parent string) bd.Issue {
		is := bd.Issue{ID: id, Title: "node " + id, Status: "open", Priority: 1, IssueType: "task", Parent: parent}
		if parent != "" {
			is.Dependencies = []bd.Dependency{pc(id, parent)}
		}
		return is
	}
	return []bd.Issue{
		mk("r0", ""), mk("a", "r0"), mk("b", "a"), mk("c", "b"), mk("d", "c"), mk("e", "d"),
		mk("z0", ""),
	}
}

func TestMillerDrillAppendsAndBack(t *testing.T) {
	m := testModel(t, chainFixture(), 160, 40)
	mp := &m
	mp.enterColumns("", false)
	if mp.view != ViewColumns {
		t.Fatal("columns view not active")
	}

	// Column 0 is the top-level beads (r0, z0).
	lay := mp.millerBuild()
	if len(lay.cols) != 1 || lay.focusCol != 0 {
		t.Fatalf("initial layout: cols=%d focus=%d", len(lay.cols), lay.focusCol)
	}

	// Drill r0 → a → b: each drill APPENDS a column, focus moves right, and the
	// path columns to the left stay put (r0 stays column 0, a stays column 1).
	millerSelect(t, mp, "r0")
	mp.millerDrill()
	millerSelect(t, mp, "a")
	mp.millerDrill()
	lay = mp.millerBuild()
	if got := strings.Join(lay.path, ","); got != "r0,a" {
		t.Fatalf("path after two drills = %q, want r0,a", got)
	}
	if lay.focusCol != 2 || len(lay.cols) != 3 {
		t.Fatalf("focus=%d cols=%d, want focus 2 / 3 cols", lay.focusCol, len(lay.cols))
	}
	// Column 0 still the roots, column 1 still a's context (children of r0).
	if lay.cols[0][0].ID != "r0" && lay.cols[0][0].ID != "z0" {
		t.Fatalf("column 0 lost the roots: %v", lay.cols[0])
	}
	if len(lay.cols[1]) != 1 || lay.cols[1][0].ID != "a" {
		t.Fatalf("column 1 = %v, want [a]", lay.cols[1])
	}
	if len(lay.cols[2]) != 1 || lay.cols[2][0].ID != "b" {
		t.Fatalf("column 2 (focus) = %v, want [b]", lay.cols[2])
	}
	if mp.millerSelectedID() != "b" {
		t.Fatalf("selected = %q, want b", mp.millerSelectedID())
	}

	// Back pops one level; focus returns to the left column re-selecting a.
	mp.millerBack()
	lay = mp.millerBuild()
	if strings.Join(lay.path, ",") != "r0" || lay.focusCol != 1 {
		t.Fatalf("after back: path=%v focus=%d", lay.path, lay.focusCol)
	}
	if mp.millerSelectedID() != "a" {
		t.Fatalf("after back selected = %q, want a", mp.millerSelectedID())
	}

	// A leaf can't be drilled: e (bottom of the chain) has no children.
	mp.enterColumns("e", false) // pre-drill to e; focus column is empty (e is a leaf)
	if mp.millerSelectedID() != "e" {
		t.Fatalf("enterColumns(e) selected = %q", mp.millerSelectedID())
	}
	pathBefore := strings.Join(mp.millerBuild().path, ",")
	mp.millerDrill() // no-op: e is a leaf
	if got := strings.Join(mp.millerBuild().path, ","); got != pathBefore {
		t.Fatalf("drilling a leaf changed the path: %q → %q", pathBefore, got)
	}
}

// TestBackWalksHistoryAcrossDrills drives the columns navigator through real
// keys and proves `[` walks the history timeline across a drill: entering the
// navigator and drilling each checkpoint the pre-transition position, so `[`
// steps back out of the drill (and `]` redoes it) — the "back key walks history
// across drills" half of the two-stack model.
func TestBackWalksHistoryAcrossDrills(t *testing.T) {
	m := testModel(t, chainFixture(), 160, 40)
	m = press(t, m, "C") // enter the Miller-columns navigator — a real transition
	if m.view != ViewColumns {
		t.Fatalf("C should enter the columns navigator, view=%v", m.view)
	}
	// Focus r0 in column 0 and drill into it with `l` (append a column).
	millerSelect(t, &m, "r0")
	m = press(t, m, "l")
	if got := strings.Join(m.millerBuild().path, ","); got != "r0" {
		t.Fatalf("drill did not append r0 to the path, path=%q", got)
	}
	if len(m.navBack) == 0 {
		t.Fatal("a drill should checkpoint the pre-drill position on the history stack")
	}
	drilledDepth := len(m.navBack)

	// [ walks back across the drill: the path pops back to the pre-drill root.
	m = press(t, m, "[")
	if got := strings.Join(m.millerBuild().path, ","); got != "" {
		t.Fatalf("[ should walk back across the drill (empty path), got %q", got)
	}
	// ] redoes the drill.
	m = press(t, m, "]")
	if got := strings.Join(m.millerBuild().path, ","); got != "r0" {
		t.Fatalf("] should redo the drill (path r0), got %q", got)
	}
	if len(m.navBack) != drilledDepth {
		t.Fatalf("redo did not restore the history depth: %d vs %d", len(m.navBack), drilledDepth)
	}
}

func TestMillerPathWindowing(t *testing.T) {
	// A narrow terminal fits only the deepest column(s) + the preview; older
	// path columns scroll off the left behind the breadcrumb.
	m := testModel(t, chainFixture(), 80, 30)
	mp := &m
	mp.enterColumns("e", false) // deepest bead: a 5-deep path (r0/a/b/c/d), 6 columns
	ci := mp.serializeColumns(28)
	if ci.TotalColumns < 5 {
		t.Fatalf("expected a deep column stack, got %d", ci.TotalColumns)
	}
	if len(ci.Columns) >= ci.TotalColumns {
		t.Fatalf("all %d columns visible at width 80 — windowing did not clip", ci.TotalColumns)
	}
	if ci.FirstVisible == 1 {
		t.Fatal("first visible column is 1 — the left path did not scroll off")
	}
	// The focused/rightmost column is always the last one shown.
	if ci.FirstVisible+len(ci.Columns)-1 != ci.TotalColumns {
		t.Fatalf("visible window %d..%d does not end at the focused column %d",
			ci.FirstVisible, ci.FirstVisible+len(ci.Columns)-1, ci.TotalColumns)
	}
}

func TestMillerDepsToggleTruncates(t *testing.T) {
	// swimFixture: g-E → g-R (hierarchy). Drill into it, then flip to deps —
	// g-R is not a hierarchy child of g-E under the deps relation, so the stale
	// tail of the path truncates rather than showing a dead column.
	m := testModel(t, swimFixture(), 160, 40)
	mp := &m
	mp.enterColumns("", false)
	millerSelect(t, mp, "g-E")
	mp.millerDrill()
	if strings.Join(mp.millerBuild().path, ",") != "g-E" {
		t.Fatalf("expected path g-E, got %v", mp.millerBuild().path)
	}
	mp.millerDeps = true
	mp.millerSel = 0
	// g-E has no dependents/blockers, so drilling produced nothing under the deps
	// relation; the path must not resurrect a column with stale hierarchy kids.
	lay := mp.millerBuild()
	if lay.focusCol >= 1 {
		if len(lay.cols[1]) > 0 && lay.cols[1][0].ID == "g-R" {
			t.Fatal("deps column still shows the hierarchy child g-R")
		}
	}
}

func TestMillerMCPRoundTrip(t *testing.T) {
	m := testModel(t, swimFixture(), 160, 40)
	attachDefault(&m) // attached, so the agent's set_view applies live

	// An agent opens the columns navigator pre-drilled to g-R.
	m.handleAgent(agentapi.SpecAction{Mode: "columns", Root: "g-R", Title: "explore g-R"})
	if m.view != ViewColumns {
		t.Fatal("columns view not active after set_view(mode:columns)")
	}
	if m.millerSelectedID() != "g-R" {
		t.Fatalf("pre-drill selected = %q, want g-R", m.millerSelectedID())
	}

	// view() reports the path + focus, and the XML names the columns.
	resp := m.agentView()
	v := resp.Data.(*agentapi.View)
	if v.Columns == nil {
		t.Fatal("view() has no columns block")
	}
	if v.Mode.Value != "columns" {
		t.Fatalf("mode label = %q", v.Mode.Value)
	}
	if strings.Join(v.Columns.Path, ",") != "g-E" {
		t.Fatalf("reported path = %v, want [g-E]", v.Columns.Path)
	}
	if v.Columns.Focus != "g-R" {
		t.Fatalf("reported focus = %q, want g-R", v.Columns.Focus)
	}
	if !strings.Contains(resp.Text, "<columns ") {
		t.Fatalf("view XML missing <columns>:\n%s", resp.Text)
	}

	// reset() restores the human's view (columns dropped).
	m.handleAgent(agentapi.ResetAction{})
	if m.view == ViewColumns {
		t.Fatal("reset did not drop the agent's columns view")
	}
}
