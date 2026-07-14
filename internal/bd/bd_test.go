package bd

import (
	"errors"
	"reflect"
	"testing"
)

type call struct{ args []string }

func fakeRunner(out string, err error, calls *[]call) Runner {
	return func(args ...string) (string, error) {
		*calls = append(*calls, call{args})
		if err != nil {
			return "", err
		}
		return out, nil
	}
}

const listJSON = `[
  {"id":"g-1","title":"One","status":"open","priority":1,"issue_type":"epic",
   "labels":["a","b"],"dependency_count":0,"dependent_count":2,
   "dependencies":[{"issue_id":"g-1","depends_on_id":"g-0","type":"blocks"}],
   "created_at":"2026-07-01T00:00:00Z","updated_at":"2026-07-07T00:00:00Z"},
  {"id":"g-2","title":"Two","status":"in_progress","priority":0,"issue_type":"task",
   "parent":"g-1","assignee":"alex","owner":null,"labels":null,"dependencies":null}
]`

func TestListParsesIssues(t *testing.T) {
	var calls []call
	c := NewClient(fakeRunner(listJSON, nil, &calls))
	issues, err := c.List(false)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(calls[0].args, []string{"list", "--json", "--flat"}) {
		t.Fatalf("args = %v", calls[0].args)
	}
	if len(issues) != 2 {
		t.Fatalf("len = %d", len(issues))
	}
	one := issues[0]
	if one.ID != "g-1" || one.Priority != 1 || one.DependentCount != 2 ||
		!reflect.DeepEqual(one.Labels, []string{"a", "b"}) ||
		len(one.Dependencies) != 1 || one.Dependencies[0].Type != "blocks" {
		t.Fatalf("issue parsed wrong: %+v", one)
	}
	// null parent/owner/labels must decode to zero values, not error.
	two := issues[1]
	if two.Parent != "g-1" || two.Owner != "" || two.Labels != nil {
		t.Fatalf("nullable fields wrong: %+v", two)
	}
}

func TestListAllFlag(t *testing.T) {
	var calls []call
	c := NewClient(fakeRunner("[]", nil, &calls))
	if _, err := c.List(true); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(calls[0].args, []string{"list", "--json", "--flat", "--all"}) {
		t.Fatalf("args = %v", calls[0].args)
	}
}

func TestQueryArgs(t *testing.T) {
	var calls []call
	c := NewClient(fakeRunner("[]", nil, &calls))
	if _, err := c.Query(`status=open AND updated>"-7d"`); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(calls[0].args, []string{"query", `status=open AND updated>"-7d"`, "--json"}) {
		t.Fatalf("args = %v", calls[0].args)
	}
}

func TestShowReturnsFirstOfArray(t *testing.T) {
	var calls []call
	c := NewClient(fakeRunner(`[{"id":"g-9","title":"Nine","status":"open"}]`, nil, &calls))
	issue, err := c.Show("g-9")
	if err != nil || issue.ID != "g-9" {
		t.Fatalf("issue=%+v err=%v", issue, err)
	}
}

func TestShowSingleObjectFallback(t *testing.T) {
	var calls []call
	c := NewClient(fakeRunner(`{"id":"g-9","title":"Nine","status":"open"}`, nil, &calls))
	issue, err := c.Show("g-9")
	if err != nil || issue.ID != "g-9" {
		t.Fatalf("issue=%+v err=%v", issue, err)
	}
}

func TestSetPriority(t *testing.T) {
	var calls []call
	c := NewClient(fakeRunner("", nil, &calls))
	if err := c.SetPriority("g-1", 0); err != nil {
		t.Fatal(err)
	}
	if err := c.SetPriority("g-1", 4); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(calls[0].args, []string{"priority", "g-1", "0"}) {
		t.Fatalf("priority args = %v", calls[0].args)
	}
	if !reflect.DeepEqual(calls[1].args, []string{"priority", "g-1", "4"}) {
		t.Fatalf("priority args = %v", calls[1].args)
	}
}

func TestErrorPropagates(t *testing.T) {
	var calls []call
	c := NewClient(fakeRunner("", errors.New("parse error at token 3"), &calls))
	if _, err := c.Query("updated>-7d"); err == nil || err.Error() != "parse error at token 3" {
		t.Fatalf("err = %v", err)
	}
}

func TestExtractError(t *testing.T) {
	exit1 := errors.New("exit status 1")
	cases := []struct {
		name           string
		stdout, stderr string
		want           string
	}{
		{"stderr wins", `{"error":"from stdout"}`, "boom from stderr\n", "boom from stderr"},
		{"bd error JSON on stdout", `{"error":"unknown field: prio"}`, "", "unknown field: prio"},
		{"neither → generic with verb", "not json", "", "bd query failed: exit status 1"},
		{"empty error field → generic", `{"error":""}`, "", "bd query failed: exit status 1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := extractError([]byte(tc.stdout), []byte(tc.stderr), []string{"query", "x"}, exit1)
			if err.Error() != tc.want {
				t.Fatalf("got %q, want %q", err.Error(), tc.want)
			}
		})
	}
}

// bd show emits dependencies as full issue objects (no depends_on_id/type);
// BlockerIDs must handle both that and bd list's edge shape.
func TestBlockerIDsBothShapes(t *testing.T) {
	edge := Issue{ID: "g-1", Parent: "g-0", Dependencies: []Dependency{
		{IssueID: "g-1", DependsOnID: "g-0", Type: "parent-child"},
		{IssueID: "g-1", DependsOnID: "g-9", Type: "blocks"},
	}}
	if got := BlockerIDs(edge); !reflect.DeepEqual(got, []string{"g-9"}) {
		t.Fatalf("edge shape: %v", got)
	}
	object := Issue{ID: "g-1", Parent: "g-0", Dependencies: []Dependency{
		{ID: "g-0", Title: "the parent"},
		{ID: "g-9", Title: "a blocker"},
	}}
	if got := BlockerIDs(object); !reflect.DeepEqual(got, []string{"g-9"}) {
		t.Fatalf("object shape: %v", got)
	}
	if got := BlockerIDs(Issue{ID: "g-1"}); got != nil {
		t.Fatalf("no deps: %v", got)
	}
}

func TestShowParsesObjectShapedDeps(t *testing.T) {
	var calls []call
	c := NewClient(fakeRunner(`[{"id":"g-1","title":"One","status":"open","parent":"g-0",
	  "dependencies":[{"id":"g-9","title":"Blocker","status":"open","description":"long text"}]}]`, nil, &calls))
	is, err := c.Show("g-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(is.Dependencies) != 1 || is.Dependencies[0].ID != "g-9" || is.Dependencies[0].DependsOnID != "" {
		t.Fatalf("deps parsed wrong: %+v", is.Dependencies)
	}
	if got := BlockerIDs(is); !reflect.DeepEqual(got, []string{"g-9"}) {
		t.Fatalf("blockers: %v", got)
	}
}

func TestHistoryCollapsesUnchangedSnapshots(t *testing.T) {
	// newest-first: closed now, closed (dup), then open earlier at P2.
	raw := `[
		{"Committer":"beads","CommitDate":"2026-07-08T09:00:00Z","Issue":{"id":"g-1","title":"Thing","status":"closed","priority":1}},
		{"Committer":"beads","CommitDate":"2026-07-08T08:00:00Z","Issue":{"id":"g-1","title":"Thing","status":"closed","priority":1}},
		{"Committer":"beads","CommitDate":"2026-07-07T08:00:00Z","Issue":{"id":"g-1","title":"Thing","status":"open","priority":2}}
	]`
	c := NewClient(func(args ...string) (string, error) { return raw, nil })
	h, err := c.History("g-1")
	if err != nil {
		t.Fatal(err)
	}
	// The two identical closed snapshots collapse to one; the open state is a
	// distinct boundary.
	if len(h) != 2 {
		t.Fatalf("want 2 change boundaries, got %d: %+v", len(h), h)
	}
	if h[0].Status != "closed" || h[0].Priority != 1 || h[1].Status != "open" || h[1].Priority != 2 {
		t.Fatalf("trajectory = %+v", h)
	}
}
