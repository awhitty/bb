package ui

import (
	"strings"
	"testing"

	"github.com/awhitty/bb/internal/agentapi"
	"github.com/awhitty/bb/internal/bd"
)

func TestCommonIDPrefix(t *testing.T) {
	if p := commonIDPrefix([]bd.Issue{{ID: "demo-1"}, {ID: "demo-2.3"}}); p != "demo-" {
		t.Fatalf("shared prefix = %q, want demo-", p)
	}
	if p := commonIDPrefix([]bd.Issue{{ID: "demo-1"}, {ID: "other-2"}}); p != "" {
		t.Fatalf("mixed prefixes must not strip, got %q", p)
	}
	if p := commonIDPrefix([]bd.Issue{{ID: "abc"}}); p != "" {
		t.Fatalf("unprefixed id, got %q", p)
	}
	if p := commonIDPrefix(nil); p != "" {
		t.Fatal("empty board")
	}
}

func TestShortID(t *testing.T) {
	m := Model{idPrefix: "demo-"}
	if got := m.shortID("demo-pqr.4"); got != "pqr.4" {
		t.Fatalf("shortID = %q", got)
	}
	if got := m.shortID("other-x"); got != "other-x" {
		t.Fatalf("non-matching id must be kept, got %q", got)
	}
}

// deps: c depends-on b depends-on a  (a blocks b blocks c).
func depChain() []bd.Issue {
	return []bd.Issue{
		{ID: "a", Title: "A", Status: "open"},
		{ID: "b", Title: "B", Status: "open", Dependencies: []bd.Dependency{{IssueID: "b", DependsOnID: "a", Type: "blocks"}}},
		{ID: "c", Title: "C", Status: "open", Dependencies: []bd.Dependency{{IssueID: "c", DependsOnID: "b", Type: "blocks"}}},
	}
}

func TestOctopusDeepTransitive(t *testing.T) {
	const w, h = 120, 30
	m := testModel(t, depChain(), w, h)
	// octoAdj follows both dependency and hierarchy edges; depChain has only
	// dependency edges, so the transitive walk is the dependency chain.
	// ancestors of c go DEEP: b (depth 0) then a (depth 1).
	up := m.octoBuild("c", octoUp)
	if len(up) != 2 || up[0].issue.ID != "b" || up[1].issue.ID != "a" || up[1].depth != 1 {
		t.Fatalf("ancestors not deep/transitive: %+v", up)
	}
	// descendants of a go DEEP: b then c.
	down := m.octoBuild("a", octoDown)
	if len(down) != 2 || down[0].issue.ID != "b" || down[1].issue.ID != "c" || down[1].depth != 1 {
		t.Fatalf("descendants not deep: %+v", down)
	}
	if flat := m.octoFlat("b"); len(flat) != 3 { // ancestor a + focal b + descendant c
		t.Fatalf("flat = %v", flat)
	}
}

func TestOctopusCycleSafe(t *testing.T) {
	const w, h = 120, 30
	issues := []bd.Issue{
		{ID: "a", Title: "A", Status: "open", Dependencies: []bd.Dependency{{IssueID: "a", DependsOnID: "b", Type: "blocks"}}},
		{ID: "b", Title: "B", Status: "open", Dependencies: []bd.Dependency{{IssueID: "b", DependsOnID: "a", Type: "blocks"}}},
	}
	m := testModel(t, issues, w, h)
	up := m.octoBuild("a", octoUp) // a ← b ← a cycle: must terminate
	cyclic := false
	for _, r := range up {
		if r.cyclic {
			cyclic = true
		}
	}
	if len(up) == 0 || !cyclic {
		t.Fatalf("cycle not handled/marked: %+v", up)
	}
}

func TestAgentReprioritize(t *testing.T) {
	const w, h = 100, 24
	m := testModel(t, deepColumn(3), w, h) // g-000 P0, g-001 P1, g-002 P2
	m2, resp := agentDo(t, m, agentapi.ReprioritizeAction{ID: "g-000", Priority: 3})
	if resp.Err != "" {
		t.Fatalf("unexpected err: %s", resp.Err)
	}
	got := -1
	for _, is := range m2.issues {
		if is.ID == "g-000" {
			got = is.Priority
		}
	}
	if got != 3 {
		t.Fatalf("optimistic priority = %d, want 3", got)
	}
	if !strings.Contains(m2.message, "g-000 → P3") {
		t.Fatalf("footer notice = %q", m2.message)
	}
	// unknown id errors, does not mutate
	_, resp2 := agentDo(t, m, agentapi.ReprioritizeAction{ID: "nope", Priority: 1})
	if resp2.Err == "" {
		t.Fatal("reprioritize of an off-board id must error")
	}
}

func TestLeanViewCardShape(t *testing.T) {
	issues := []bd.Issue{
		{ID: "g-1", Title: "Blocker", Status: "open"},
		{ID: "g-2", Title: "Blocked", Status: "open",
			Dependencies: []bd.Dependency{{IssueID: "g-2", DependsOnID: "g-1", Type: "blocks"}}},
	}
	m := testModel(t, issues, 120, 24)
	_, resp := agentDo(t, m, agentapi.ViewAction{})
	x := resp.Text
	if strings.Contains(x, "type=") {
		t.Fatalf("lean card must not carry type=:\n%s", x)
	}
	if !strings.Contains(x, `blockers="1"`) {
		t.Fatalf("blocked card must carry its open-blocker count:\n%s", x)
	}
}
