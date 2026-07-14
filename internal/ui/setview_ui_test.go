package ui

import (
	"encoding/json"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/awhitty/bb/internal/agentapi"
	"github.com/awhitty/bb/internal/rollup"
)

func intp(n int) *int { return &n }

// TestSpecTraverseGroupApplyLive drives the converged algebra straight through
// the live-apply path: a single set_view carries the view, the traverse, and the
// group facet, and each lands on its knob.
func TestSpecTraverseGroupApplyLive(t *testing.T) {
	m := testModel(t, treeFixture(), 120, 30)
	attachDefault(&m) // attached to the default channel, so pushes apply live
	m.handleAgent(agentapi.SpecAction{Mode: "tree", Traverse: "deps", Group: "status", Title: "deps tree by status"})
	if m.view != ViewTree {
		t.Fatalf("view = %v, want tree", m.view)
	}
	if !m.depsTree {
		t.Fatal("traverse=deps did not set depsTree")
	}
	if m.treeFacet != rollup.FacetStatus {
		t.Fatalf("group=status did not segment the tree by status (treeFacet=%q)", m.treeFacet)
	}

	// columns threads the traverse through enterColumns (keeping the drill model).
	m.handleAgent(agentapi.SpecAction{Mode: "columns", Traverse: "deps", Title: "deps columns"})
	if m.view != ViewColumns || !m.millerDeps {
		t.Fatalf("columns traverse=deps: view=%v millerDeps=%v", m.view, m.millerDeps)
	}
}

// TestSpecBackwardCompatModeGroupSort proves an existing set_view spec — carrying
// only the pre-existing mode/group/sort knobs, with neither lane nor scope — still
// applies exactly as before: the new fields default to today's flat, unscoped
// board when absent. This is the backward-compatibility guard for the additive
// schema extension.
func TestSpecBackwardCompatModeGroupSort(t *testing.T) {
	m := testModel(t, treeFixture(), 120, 30)
	attachDefault(&m)

	m.handleAgent(agentapi.SpecAction{Mode: "kanban", Group: "type", SortKey: "title", SortDir: "asc", Title: "board by type"})
	if m.view != ViewKanban {
		t.Fatalf("view = %v, want kanban", m.view)
	}
	if m.boardFacet != rollup.FacetType {
		t.Fatalf("group=type did not set boardFacet (%q)", m.boardFacet)
	}
	if m.flatSort.Key != rollup.SortTitle || m.flatSort.Desc {
		t.Fatalf("sort not applied: %+v, want title asc", m.flatSort)
	}
	// The new fields, absent from the spec, leave the board flat and unscoped.
	if m.boardLane != "" {
		t.Fatalf("an old spec set a lane: boardLane=%q, want the flat board", m.boardLane)
	}
	if m.scopeRoot != "" {
		t.Fatalf("an old spec set a scope: scopeRoot=%q, want unscoped", m.scopeRoot)
	}
}

// TestSpecLaneAndScopeApply drives the two additive fields through the live-apply
// path: lane splits the kanban into a second facet axis (boardLane + bands), and
// scope narrows every mode to a bead's neighborhood (scopeRoot). A scope root that
// is not on the board is refused rather than scoping to nothing.
func TestSpecLaneAndScopeApply(t *testing.T) {
	// Lane: a 2×2 board (status × type) splits into multi-band columns.
	ml := testModel(t, crossTabIssues(), 120, 30)
	attachDefault(&ml)
	ml.handleAgent(agentapi.SpecAction{Mode: "kanban", Group: "status", Lane: "type", Title: "cross-tab"})
	if ml.boardLane != rollup.FacetType {
		t.Fatalf("lane=type did not set boardLane (%q)", ml.boardLane)
	}
	if len(ml.boardBands) != len(ml.columns) {
		t.Fatalf("boardBands (%d) not parallel to columns (%d)", len(ml.boardBands), len(ml.columns))
	}
	for i, bands := range ml.boardBands {
		if len(bands) < 2 {
			t.Fatalf("column %d (%q) has %d band(s), want a multi-lane split", i, ml.columns[i].Key, len(bands))
		}
	}
	// "none" clears the lane back to the flat board.
	ml.handleAgent(agentapi.SpecAction{Lane: "none"})
	if ml.boardLane != "" || ml.boardBands != nil {
		t.Fatalf("lane=none did not clear the 2D board: boardLane=%q bands=%v", ml.boardLane, ml.boardBands)
	}

	// Scope: narrow every mode to g-1's neighborhood.
	ms := testModel(t, graphFixture(), 120, 30)
	attachDefault(&ms)
	ms.handleAgent(agentapi.SpecAction{Mode: "kanban", Scope: "g-1", Title: "around g-1"})
	if ms.scopeRoot != "g-1" {
		t.Fatalf("scope=g-1 did not set scopeRoot (%q)", ms.scopeRoot)
	}
	if scoped := ms.scopedIssues(); len(scoped) == 0 {
		t.Fatal("scope produced an empty neighborhood, want g-1 and its children")
	}

	// A scope root that is not on the board is refused, leaving the board unscoped.
	resp, _ := ms2Handle(t, agentapi.SpecAction{Scope: "does-not-exist"})
	if resp.Err == "" {
		t.Fatal("a bogus scope root was accepted, want an error")
	}
}

// ms2Handle applies a spec against a fresh graphFixture model and returns the
// response, so the error path can be checked without disturbing another model.
func ms2Handle(t *testing.T, a agentapi.SpecAction) (agentapi.Response, tea.Cmd) {
	t.Helper()
	m := testModel(t, graphFixture(), 120, 30)
	attachDefault(&m) // attached, so applySpec runs its scope validation
	return m.handleAgent(a)
}

// TestShareSpecRoundTripsCollapseGroupTraverse is the persistence-gap fix: an
// agent publishes a rich tree view while the human is NOT attached; the spec is
// carried into the stream (and survives a JSON round-trip), and applying it
// (applyShare) restores collapse, min_subtree, group, and traverse — which the
// old shareSpec silently dropped.
func TestShareSpecRoundTripsCollapseGroupTraverse(t *testing.T) {
	m := testModel(t, treeFixture(), 120, 30)

	m.handleAgent(agentapi.SpecAction{
		Mode: "tree", Traverse: "deps", Group: "status",
		Collapse: &agentapi.CollapseSpec{NodeIDs: []string{"g-1"}}, MinSubtree: intp(2),
		Title: "deep tree",
	})
	if m.view == ViewTree {
		t.Fatal("publish-only must not seize the screen into the tree view")
	}
	ns := m.sharesNewestFirst()
	if len(ns) == 0 {
		t.Fatal("nothing was published to the shares stream")
	}
	s := ns[0]
	sp := s.Spec
	if sp.Traverse != "deps" || sp.Group != "status" || sp.Collapse == nil || sp.MinSubtree == nil {
		t.Fatalf("shareSpec dropped a field: %+v", sp)
	}

	// The stream persists as JSON — the new fields must survive a marshal cycle.
	raw, err := json.Marshal(sp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back shareSpec
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.Traverse != "deps" || back.Group != "status" || back.Collapse == nil ||
		len(back.Collapse.NodeIDs) != 1 || back.Collapse.NodeIDs[0] != "g-1" ||
		back.MinSubtree == nil || *back.MinSubtree != 2 {
		t.Fatalf("shareSpec did not JSON round-trip: %+v", back)
	}

	// Pulling the share (@) applies the whole spec to the board.
	m.applyShare(s)
	if !m.depsTree {
		t.Fatal("applyShare dropped traverse=deps")
	}
	if m.treeFacet != rollup.FacetStatus {
		t.Fatalf("applyShare dropped group=status (treeFacet=%q)", m.treeFacet)
	}
	if m.minSubtree != 2 {
		t.Fatalf("applyShare dropped min_subtree (minSubtree=%d)", m.minSubtree)
	}
	if !m.collapsed["g-1"] {
		t.Fatal("applyShare dropped the collapse set (g-1 should be folded)")
	}
}
