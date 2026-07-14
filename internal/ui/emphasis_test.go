package ui

import (
	"testing"

	"github.com/awhitty/bb/internal/agentapi"
	"github.com/awhitty/bb/internal/bd"
	"github.com/awhitty/bb/internal/rollup"
)

func emphBoard() []bd.Issue {
	return []bd.Issue{
		{ID: "a", Title: "A", Status: "open"},
		{ID: "b", Title: "B", Status: "open"},
		{ID: "c", Title: "C", Status: "open"},
	}
}

func TestEmphasisMarkerAndClear(t *testing.T) {
	m := testModel(t, emphBoard(), 120, 30)
	attachDefault(&m) // attached, so the agent's emphasis applies live
	resp, _ := m.handleAgent(agentapi.EmphasizeAction{Targets: []agentapi.Emphasis{
		{Kind: "issue", Ref: "b", Style: "marker"},
	}})
	if resp.Err != "" {
		t.Fatalf("emphasize err: %s", resp.Err)
	}
	if e := m.issueEmph("b"); e.marker == "" {
		t.Fatal("target b should carry a marker")
	}
	if e := m.issueEmph("a"); e.marker != "" || e.muted {
		t.Fatalf("non-target a should be undecorated, got %+v", e)
	}
	if !m.clearEmphasis() {
		t.Fatal("clearEmphasis should report it cleared something")
	}
	if e := m.issueEmph("b"); e.marker != "" {
		t.Fatal("b should be undecorated after clear")
	}
}

func TestEmphasisSpotlightMutesOthers(t *testing.T) {
	m := testModel(t, emphBoard(), 120, 30)
	attachDefault(&m)
	m.handleAgent(agentapi.EmphasizeAction{Targets: []agentapi.Emphasis{
		{Kind: "issue", Ref: "b", Style: "spotlight"},
	}})
	if !m.spotlightActive() {
		t.Fatal("spotlight should be active")
	}
	if m.issueEmph("b").muted {
		t.Fatal("the spotlight target must NOT be muted")
	}
	if !m.issueEmph("a").muted || !m.issueEmph("c").muted {
		t.Fatal("non-targets must be muted under spotlight")
	}
}

func TestSpecActionSortAndReset(t *testing.T) {
	m := testModel(t, emphBoard(), 120, 30)
	m.view = ViewTree
	before := m.treeSort.Key
	attachDefault(&m)
	m.handleAgent(agentapi.SpecAction{SortKey: "open-count", SortDir: "asc"})
	if m.treeSort.Key != rollup.SortOpenCount || m.treeSort.Desc {
		t.Fatalf("set_view sort = %+v", m.treeSort)
	}
	if !m.attach.active {
		t.Fatal("attached set_view should keep the attach layer live so esc/nav can restore")
	}
	m.detach()
	if m.treeSort.Key != before {
		t.Fatalf("reset should restore the user's sort %q, got %q", before, m.treeSort.Key)
	}
}

func TestSpecActionCollapseRoots(t *testing.T) {
	issues := []bd.Issue{
		{ID: "R", Title: "root", Status: "open"},
		{ID: "R.1", Parent: "R", Title: "child", Status: "open"},
	}
	m := testModel(t, issues, 120, 30)
	m.view = ViewTree
	attachDefault(&m)
	lvl := 0
	m.handleAgent(agentapi.SpecAction{Collapse: &agentapi.CollapseSpec{Level: &lvl}})
	if !m.collapsed["R"] {
		t.Fatal("collapse level 0 should fold the root R")
	}
}
