package ui

import (
	"reflect"
	"strings"
	"testing"

	"github.com/awhitty/bb/internal/agentapi"
	"github.com/awhitty/bb/internal/bd"
	"github.com/awhitty/bb/internal/rollup"
)

// swimFixture builds a board where g-R has all four relationship kinds
// populated across several statuses — the shape the swimlane board exists to
// show. g-R is a child of epic g-E (so it has siblings), has two sub-issues,
// is blocked by g-B1, and blocks g-D1.
func swimFixture() []bd.Issue {
	pc := func(id, parent string) bd.Dependency {
		return bd.Dependency{IssueID: id, DependsOnID: parent, Type: "parent-child"}
	}
	blocks := func(id, on string) bd.Dependency {
		return bd.Dependency{IssueID: id, DependsOnID: on, Type: "blocks"}
	}
	return []bd.Issue{
		{ID: "g-E", Title: "Epic", Status: "open", Priority: 1, IssueType: "epic"},
		{ID: "g-R", Title: "Root feature", Status: "in_progress", Priority: 0, IssueType: "feature", Parent: "g-E",
			Dependencies: []bd.Dependency{pc("g-R", "g-E"), blocks("g-R", "g-B1")}},
		// sub-issues of g-R (open + closed)
		{ID: "g-c1", Title: "Sub open", Status: "open", Priority: 2, IssueType: "task", Parent: "g-R",
			Dependencies: []bd.Dependency{pc("g-c1", "g-R")}},
		{ID: "g-c2", Title: "Sub done", Status: "closed", Priority: 2, IssueType: "task", Parent: "g-R",
			Dependencies: []bd.Dependency{pc("g-c2", "g-R")}},
		// blocker of g-R
		{ID: "g-B1", Title: "Blocker", Status: "in_progress", Priority: 1, IssueType: "task"},
		// dependent of g-R (it depends on g-R → g-R blocks it)
		{ID: "g-D1", Title: "Dependent", Status: "blocked", Priority: 1, IssueType: "task",
			Dependencies: []bd.Dependency{blocks("g-D1", "g-R")}},
		// siblings of g-R (children of g-E), open + deferred
		{ID: "g-S1", Title: "Sibling open", Status: "open", Priority: 3, IssueType: "task", Parent: "g-E",
			Dependencies: []bd.Dependency{pc("g-S1", "g-E")}},
		{ID: "g-S2", Title: "Sibling deferred", Status: "deferred", Priority: 3, IssueType: "chore", Parent: "g-E",
			Dependencies: []bd.Dependency{pc("g-S2", "g-E")}},
	}
}

// laneIdxOf / colIdxOf locate a lane / column in the built matrix.
func laneIdxOf(sm swimMatrix, kind swimLaneKind) int {
	for i, l := range sm.lanes {
		if l.kind == kind {
			return i
		}
	}
	return -1
}

func colIdxOf(sm swimMatrix, key string) int {
	for i, c := range sm.columns {
		if c.key == key {
			return i
		}
	}
	return -1
}

// cellIDs is a test helper: the ids in one lane's cell for the column with the
// given facet key.
func cellIDs(sm swimMatrix, kind swimLaneKind, key string) []string {
	li, ci := laneIdxOf(sm, kind), colIdxOf(sm, key)
	if li < 0 || ci < 0 {
		return nil
	}
	var out []string
	for _, is := range sm.cells[li][ci] {
		out = append(out, is.ID)
	}
	return out
}

// gridShape reduces a matrix to lane-kind → column-key → ordered ids, the whole
// bucketing in a comparable form (ignoring geometry / render state).
func gridShape(sm swimMatrix) map[swimLaneKind]map[string][]string {
	out := map[swimLaneKind]map[string][]string{}
	for li, lane := range sm.lanes {
		cols := map[string][]string{}
		for ci, col := range sm.columns {
			var ids []string
			for _, is := range sm.cells[li][ci] {
				ids = append(ids, is.ID)
			}
			if len(ids) > 0 {
				cols[col.key] = ids
			}
		}
		out[lane.kind] = cols
	}
	return out
}

func colKeys(sm swimMatrix) []string {
	var out []string
	for _, c := range sm.columns {
		out = append(out, c.key)
	}
	return out
}

func TestSwimMatrixBucketing(t *testing.T) {
	m := testModel(t, swimFixture(), 140, 40)
	sm := m.buildGrid("g-R", rollup.FacetStatus)

	if sm.root.ID != "g-R" {
		t.Fatalf("root = %q", sm.root.ID)
	}
	// All four relationship kinds are populated, in the fixed lane order.
	wantLanes := []swimLaneKind{swimChildren, swimBlockedBy, swimBlocking, swimSiblings}
	if len(sm.lanes) != len(wantLanes) {
		t.Fatalf("got %d lanes, want %d", len(sm.lanes), len(wantLanes))
	}
	for i, want := range wantLanes {
		if sm.lanes[i].kind != want {
			t.Fatalf("lane %d = %v, want %v", i, sm.lanes[i].kind, want)
		}
	}

	// Bucketing by (relationship, status).
	if got := cellIDs(sm, swimChildren, "open"); len(got) != 1 || got[0] != "g-c1" {
		t.Fatalf("children/open = %v", got)
	}
	if got := cellIDs(sm, swimChildren, "closed"); len(got) != 1 || got[0] != "g-c2" {
		t.Fatalf("children/closed = %v", got)
	}
	if got := cellIDs(sm, swimBlockedBy, "in_progress"); len(got) != 1 || got[0] != "g-B1" {
		t.Fatalf("blocked-by/in_progress = %v", got)
	}
	if got := cellIDs(sm, swimBlocking, "blocked"); len(got) != 1 || got[0] != "g-D1" {
		t.Fatalf("blocking/blocked = %v", got)
	}
	if got := cellIDs(sm, swimSiblings, "open"); len(got) != 1 || got[0] != "g-S1" {
		t.Fatalf("siblings/open = %v", got)
	}
	if got := cellIDs(sm, swimSiblings, "deferred"); len(got) != 1 || got[0] != "g-S2" {
		t.Fatalf("siblings/deferred = %v", got)
	}

	// The root never appears anywhere in the matrix.
	for li := range sm.lanes {
		for _, cell := range sm.cells[li] {
			for _, x := range cell {
				if x.ID == "g-R" {
					t.Fatal("root leaked into its own matrix")
				}
			}
		}
	}

	// Only-populated status columns: all five appear here, in the fixed order.
	if want := []string{"open", "in_progress", "blocked", "deferred", "closed"}; !reflect.DeepEqual(colKeys(sm), want) {
		t.Fatalf("columns = %v, want %v", colKeys(sm), want)
	}
}

// The status-facet grid (the default) buckets exactly as the old fixed-status
// swimlane did: a DeepEqual on the whole lane × status × ids shape.
func TestBuildGridStatusMatchesOld(t *testing.T) {
	m := testModel(t, swimFixture(), 140, 40)
	sm := m.buildGrid("g-R", rollup.FacetStatus)

	want := map[swimLaneKind]map[string][]string{
		swimChildren:  {"open": {"g-c1"}, "closed": {"g-c2"}},
		swimBlockedBy: {"in_progress": {"g-B1"}},
		swimBlocking:  {"blocked": {"g-D1"}},
		swimSiblings:  {"open": {"g-S1"}, "deferred": {"g-S2"}},
	}
	if got := gridShape(sm); !reflect.DeepEqual(got, want) {
		t.Fatalf("status grid shape = %v, want %v", got, want)
	}
}

// A non-status column facet re-buckets the same lanes: type puts every card in
// its issue-type column, ordered count-desc then key.
func TestBuildGridTypeFacet(t *testing.T) {
	m := testModel(t, swimFixture(), 140, 40)
	sm := m.buildGrid("g-R", rollup.FacetType)

	// task (5 cards) before chore (1 card).
	if want := []string{"task", "chore"}; !reflect.DeepEqual(colKeys(sm), want) {
		t.Fatalf("type columns = %v, want %v", colKeys(sm), want)
	}
	want := map[swimLaneKind]map[string][]string{
		swimChildren:  {"task": {"g-c1", "g-c2"}},
		swimBlockedBy: {"task": {"g-B1"}},
		swimBlocking:  {"task": {"g-D1"}},
		swimSiblings:  {"task": {"g-S1"}, "chore": {"g-S2"}},
	}
	if got := gridShape(sm); !reflect.DeepEqual(got, want) {
		t.Fatalf("type grid shape = %v, want %v", got, want)
	}
}

func TestSwimOnlyPopulated(t *testing.T) {
	// A root with ONLY siblings, all open: exactly one lane, and the open column
	// is NOT shown (g-S1's siblings are in_progress + deferred).
	m := testModel(t, swimFixture(), 140, 40)
	sm := m.buildGrid("g-S1", rollup.FacetStatus) // g-S1's only relations are its siblings under g-E
	for _, lane := range sm.lanes {
		if lane.kind == swimChildren || lane.kind == swimBlockedBy || lane.kind == swimBlocking {
			t.Fatalf("empty lane %v was shown", lane.kind)
		}
	}
	if len(sm.lanes) != 1 || sm.lanes[0].kind != swimSiblings {
		t.Fatalf("expected one siblings lane, got %d lanes", len(sm.lanes))
	}
	// g-S1's siblings are g-R (in_progress) and g-S2 (deferred) — open column is
	// NOT shown even though it exists elsewhere on the board.
	if colIdxOf(sm, "open") >= 0 {
		t.Fatal("open column shown with no open sibling")
	}
}

func TestSwimNavigationAndFocus(t *testing.T) {
	m := testModel(t, swimFixture(), 140, 40)
	mp := &m
	mp.enterSwimRooted("g-R")

	// Focus lands on a real card, and it is one of g-R's relations (never g-R).
	first := mp.focusedIssue()
	if first == nil || first.ID == "g-R" {
		t.Fatalf("initial focus = %v", first)
	}

	// hjkl move focus and never panic / never leave the matrix. Every focused
	// issue is a relation of the root.
	rels := map[string]bool{"g-c1": true, "g-c2": true, "g-B1": true, "g-D1": true, "g-S1": true, "g-S2": true}
	for _, seq := range [][2]int{{0, 1}, {1, 0}, {0, 1}, {1, 0}, {0, -1}, {-1, 0}, {0, -1}, {-1, 0}} {
		mp.swimNav(seq[0], seq[1])
		is := mp.focusedIssue()
		if is == nil {
			t.Fatal("focus went nil during navigation")
		}
		if !rels[is.ID] {
			t.Fatalf("focus %q is not a relation of the root", is.ID)
		}
	}

	// l walks toward the last column; the focus stays valid the whole way.
	sm := mp.buildGrid("g-R", rollup.FacetStatus)
	for i := 0; i < len(sm.columns)+2; i++ {
		mp.swimNav(1, 0)
		if mp.focusedIssue() == nil {
			t.Fatal("focus nil while paging columns right")
		}
	}
}

// The grid renders inside the frame with either column facet.
func TestSwimGridFramesFitBothFacets(t *testing.T) {
	const w, h = 140, 40
	for _, facet := range []rollup.Facet{rollup.FacetStatus, rollup.FacetType} {
		m := testModel(t, swimFixture(), w, h)
		m.enterSwimRooted("g-R")
		m.swimFacet = facet
		frameLines(t, m, w, h) // == h lines, each ≤ w wide
	}
}

func TestSwimMCPRoundTrip(t *testing.T) {
	m := testModel(t, swimFixture(), 140, 40)
	attachDefault(&m) // attached, so the agent's set_view applies live

	// An agent opens the relationship board for g-R via set_view(mode:relationship).
	m.handleAgent(agentapi.SpecAction{Mode: "relationship", Root: "g-R", Title: "g-R relationships"})
	if m.view != ViewSwim || m.swimRootID() != "g-R" {
		t.Fatalf("relationship board not active on g-R (view=%v root=%q)", m.view, m.swimRootID())
	}

	// view() reports it: the structured mirror carries the root + lanes.
	resp := m.agentView()
	v := resp.Data.(*agentapi.View)
	if v.Swim == nil {
		t.Fatal("view() has no relationship_board")
	}
	if v.Swim.Root != "g-R" {
		t.Fatalf("reported root = %q", v.Swim.Root)
	}
	if v.Mode.Value != "relationship-board" {
		t.Fatalf("mode label = %q", v.Mode.Value)
	}
	if len(v.Swim.Lanes) != 4 {
		t.Fatalf("reported %d lanes", len(v.Swim.Lanes))
	}
	// The XML names the board + its root and nests lanes/cells/cards.
	for _, must := range []string{
		`<relationship-board root="g-R"`,
		`<lane rel="sub-issues"`,
		`<lane rel="blocked-by"`,
		`<lane rel="blocking"`,
		`<lane rel="siblings"`,
		`<cell status="closed">`,
	} {
		if !strings.Contains(resp.Text, must) {
			t.Fatalf("view XML missing %q:\n%s", must, resp.Text)
		}
	}

	// reset() restores the human's view (swimlane dropped).
	m.handleAgent(agentapi.ResetAction{})
	if m.view == ViewSwim {
		t.Fatal("reset did not drop the agent's relationship board")
	}
}
