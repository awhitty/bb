package mcpserv

import (
	"strings"
	"testing"

	"github.com/awhitty/bb/internal/bd"
)

func board() map[string]bd.Issue {
	mk := func(id, status string, deps ...string) bd.Issue {
		is := bd.Issue{ID: id, Title: "T-" + id, Status: status, IssueType: "task"}
		for _, d := range deps {
			is.Dependencies = append(is.Dependencies, bd.Dependency{IssueID: id, DependsOnID: d, Type: "blocks"})
		}
		return is
	}
	list := []bd.Issue{
		mk("a", "open", "b", "c"),
		mk("b", "open", "d"),
		mk("c", "closed"),
		mk("d", "closed"),
		mk("z", "open", "a"), // z depends on a
	}
	m := map[string]bd.Issue{}
	for _, is := range list {
		m[is.ID] = is
	}
	return m
}

func TestTraverseBlockerChain(t *testing.T) {
	byID := board()
	adj := func(x string) []string { return bd.BlockerIDs(byID[x]) }
	nodes, edges := traverse("a", byID, adj, 20)
	// a → b,c ; b → d. Reached: b,c,d.
	got := map[string]graphNode{}
	for _, n := range nodes {
		got[n.ID] = n
	}
	if len(got) != 3 || got["b"].Depth != 1 || got["d"].Depth != 2 {
		t.Fatalf("chain wrong: %+v", nodes)
	}
	// b and (transitively) nothing open below except b itself; c,d closed.
	if !got["b"].Blocking || got["c"].Blocking || got["d"].Blocking {
		t.Fatalf("blocking flags wrong: %+v", got)
	}
	if len(edges) == 0 {
		t.Fatal("edges should be recorded")
	}
}

func TestGraphSummaryNamesOpenBlockers(t *testing.T) {
	nodes := []graphNode{
		{ID: "b", Status: "open", Blocking: true, Depth: 1},
		{ID: "c", Status: "closed", Depth: 1},
	}
	s := graphSummary("blockers", "a", nodes)
	if !strings.Contains(s, "actively blocking a") || !strings.Contains(s, "b") {
		t.Fatalf("summary should name the open blocker: %q", s)
	}
	if !strings.Contains(graphSummary("blockers", "a", nil), "no blockers") {
		t.Fatal("empty chain should say unblocked")
	}
}

func TestSearchTermsAndSnippet(t *testing.T) {
	if got := searchTerms("Door KEYS, again!"); strings.Join(got, ",") != "door,keys,again" {
		t.Fatalf("terms = %v", got)
	}
	is := bd.Issue{Description: "The calendar import needs conflict handling across devices."}
	sn := snippet(is, []string{"calendar"})
	if !strings.Contains(sn, "calendar") {
		t.Fatalf("snippet should surround the term: %q", sn)
	}
}

// The issues() payload always carries total_count and truncated; a truncated page
// adds next_cursor and a plain-language hint so an agent never reads a page as the
// whole set.
func TestPaginateIssuesFieldsAndHint(t *testing.T) {
	list := make([]bd.Issue, 250)
	for i := range list {
		list[i] = bd.Issue{ID: string(rune('a')), Title: "t", Status: "open"}
	}

	// A truncated first page (100 of 250).
	out := paginateIssues(list, 0, 100, false, false)
	if out["total_count"] != 250 {
		t.Fatalf("total_count = %v, want 250", out["total_count"])
	}
	if out["truncated"] != true {
		t.Fatalf("truncated = %v, want true", out["truncated"])
	}
	if out["next_cursor"] != "100" {
		t.Fatalf("next_cursor = %v, want 100", out["next_cursor"])
	}
	hint, _ := out["hint"].(string)
	if !strings.Contains(hint, "100 of 250") || !strings.Contains(hint, "narrow") {
		t.Fatalf("hint should read 'N of M … narrow the query', got %q", hint)
	}
	if n := len(out["issues"].([]map[string]any)); n != 100 {
		t.Fatalf("page should hold 100 issues, got %d", n)
	}

	// The final page is not truncated: no cursor, no hint, still total_count present.
	out = paginateIssues(list, 200, 100, false, false)
	if out["truncated"] != false {
		t.Fatalf("last page truncated = %v, want false", out["truncated"])
	}
	if _, ok := out["next_cursor"]; ok {
		t.Fatal("last page must not carry next_cursor")
	}
	if _, ok := out["hint"]; ok {
		t.Fatal("last page must not carry a hint")
	}
	if out["total_count"] != 250 {
		t.Fatalf("total_count must persist on the last page, got %v", out["total_count"])
	}
}

func TestCommonPrefix(t *testing.T) {
	list := []bd.Issue{{ID: "demo-1"}, {ID: "demo-2.3"}, {ID: "other-1"}}
	if p := commonPrefix(list); p != "demo-" {
		t.Fatalf("prefix = %q", p)
	}
}
