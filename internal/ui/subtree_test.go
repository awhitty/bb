package ui

import (
	"errors"
	"testing"

	"github.com/awhitty/bb/internal/bd"
)

// subtree=<id> resolves to the epic PLUS its whole descendant subtree, against
// the full board — the in-process stand-in for the `parent=<id> OR id=<id>`
// query bd rejects.
func TestSubtreeIssuesIncludesRootAndDescendants(t *testing.T) {
	board := []bd.Issue{
		{ID: "E", Title: "epic"},
		{ID: "c1", Parent: "E"},
		{ID: "c2", Parent: "E"},
		{ID: "g1", Parent: "c1"}, // grandchild — transitive
		{ID: "u1", Title: "unrelated"},
	}
	got := subtreeIssues(board, "E")
	want := map[string]bool{"E": true, "c1": true, "c2": true, "g1": true}
	if len(got) != len(want) {
		t.Fatalf("subtree size = %d (%v), want %d", len(got), ids(got), len(want))
	}
	for _, is := range got {
		if !want[is.ID] {
			t.Fatalf("subtree included %q, not in %v", is.ID, want)
		}
	}
	// The root itself is included (the whole point — bd's parent=E omits it).
	if subtreeIssues(board, "E")[0].ID != "E" {
		t.Fatalf("root E must be first/present: %v", ids(got))
	}
	// Unrelated bead never leaks in.
	for _, is := range got {
		if is.ID == "u1" {
			t.Fatal("unrelated bead leaked into the subtree")
		}
	}
}

// boardQuery resolves subtree=<id> by loading the FULL board (bd list --all)
// and filtering in-process — never sending subtree= to bd. A normal query is
// passed straight through.
func TestBoardQueryResolvesSubtreeViaFullList(t *testing.T) {
	var sawArgs [][]string
	runner := func(args ...string) (string, error) {
		sawArgs = append(sawArgs, args)
		switch args[0] {
		case "list": // bd list --json --flat --all → the full board
			return `[{"id":"E","title":"epic"},{"id":"c1","parent":"E"},{"id":"g1","parent":"c1"},{"id":"u1","title":"other"}]`, nil
		case "query":
			t.Fatalf("subtree must NOT be sent to bd query, but got: %v", args)
		}
		return "[]", nil
	}
	client := bd.NewClient(runner)

	issues, graph, err := boardQuery(client, "subtree=E")
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(issues); len(got) != 3 {
		t.Fatalf("subtree=E resolved to %v, want E,c1,g1", got)
	}
	if len(graph) != 4 {
		t.Fatalf("graph should be the full board (4), got %d", len(graph))
	}
	// It loaded via `list --all`, not a bd query.
	if len(sawArgs) != 1 || sawArgs[0][0] != "list" {
		t.Fatalf("expected one `list` call, got %v", sawArgs)
	}
}

func TestSubtreeRootParses(t *testing.T) {
	cases := []struct {
		in   string
		root string
		ok   bool
	}{
		{"subtree=demo-pqr", "demo-pqr", true},
		{" subtree = demo-pqr ", "demo-pqr", true},
		{"parent=demo-pqr", "", false},
		{"subtree=demo-pqr AND status=open", "", false}, // combined → not a subtree
		{"id=demo-pqr", "", false},
		{"", "", false},
	}
	for _, tc := range cases {
		root, ok := subtreeRoot(tc.in)
		if ok != tc.ok || root != tc.root {
			t.Fatalf("subtreeRoot(%q) = (%q,%v), want (%q,%v)", tc.in, root, ok, tc.root, tc.ok)
		}
	}
}

func ids(list []bd.Issue) []string {
	out := make([]string, len(list))
	for i, is := range list {
		out[i] = is.ID
	}
	return out
}

// A bd-rejected query surfaces a clean one-line footer error and leaves the
// board on the PRIOR view — the filter reverts to the last query bd accepted,
// so a bad query never blanks or wedges the board.
func TestRejectedQueryKeepsPriorViewAndReverts(t *testing.T) {
	board := []bd.Issue{
		{ID: "a", Title: "A", Status: "open"},
		{ID: "b", Title: "B", Status: "open"},
	}
	m := testModel(t, board, 120, 30)
	if len(m.columns) == 0 {
		t.Fatal("precondition: board should have rows")
	}
	// The user applies a query bd accepts; that becomes the standing filter.
	m.lastAppliedQuery = "status=open"
	m.activeQuery = "status=open"

	// Now a query bd REJECTS arrives (unknown field / unsupported operator).
	m.activeQuery = "parent!=none" // what got typed/compiled and dispatched
	next, _ := m.Update(issuesMsg{
		seq:   m.seq,
		query: "parent!=none",
		err:   errors.New("parent only supports = operator"),
	})
	m = next.(Model)

	if len(m.columns) == 0 {
		t.Fatal("board blanked on a rejected query — must stay on the prior view")
	}
	if got := ids(m.visibleIssues()); len(got) != 2 {
		t.Fatalf("prior view lost: visible = %v, want the 2 prior beads", got)
	}
	if !m.msgIsError || m.message == "" {
		t.Fatalf("no footer error for the rejected query: %q", m.message)
	}
	if want := "query error: parent only supports = operator"; m.message != want {
		t.Fatalf("footer = %q, want %q", m.message, want)
	}
	if m.activeQuery != "status=open" {
		t.Fatalf("filter did not revert to the last accepted query: %q", m.activeQuery)
	}
}
