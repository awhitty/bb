package nlq

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/awhitty/bb/internal/bd"
)

func vocabFixture() []bd.Issue {
	issues := []bd.Issue{
		{ID: "demo-aaa", Title: "Reporting and exports", Status: "open", IssueType: "epic"},
		{ID: "demo-aaa.1", Title: "child 1", Status: "open", IssueType: "task", Parent: "demo-aaa",
			Assignee: "Alex Rivera", Owner: "alex@example.com",
			Labels: []string{"critical-path", "now"}},
		{ID: "demo-aaa.2", Title: "child 2", Status: "open", IssueType: "task", Parent: "demo-aaa",
			Assignee: "Alex Rivera", Labels: []string{"critical-path"}},
		{ID: "demo-bbb", Title: "Launch", Status: "open", IssueType: "epic"}, // epic, no children yet
		{ID: "demo-ccc", Title: "Loner task", Status: "open", IssueType: "task"},
		{ID: "demo-ddd", Title: "Sub-parent", Status: "open", IssueType: "feature", Parent: "demo-aaa"},
		{ID: "demo-ddd.1", Title: "grandchild", Status: "open", IssueType: "task", Parent: "demo-ddd"},
	}
	return issues
}

func TestDeriveVocabBasics(t *testing.T) {
	v := DeriveVocab(vocabFixture())
	if v.IDPrefix != "demo-" {
		t.Fatalf("prefix = %q", v.IDPrefix)
	}
	if !reflect.DeepEqual(v.Assignees, []string{"Alex Rivera"}) {
		t.Fatalf("assignees must be EXACT stored strings: %v", v.Assignees)
	}
	if !reflect.DeepEqual(v.Owners, []string{"alex@example.com"}) {
		t.Fatalf("owners = %v", v.Owners)
	}
	if !reflect.DeepEqual(v.Labels, []string{"critical-path(2)", "now(1)"}) {
		t.Fatalf("labels = %v", v.Labels)
	}
	if v.Types[0] != "task" { // most common first
		t.Fatalf("types = %v", v.Types)
	}
	// Epics: top-level parents by child count, plus childless top-level epics.
	// demo-ddd is a parent but NOT top-level → excluded.
	if !reflect.DeepEqual(v.Epics, []string{"demo-aaa Reporting and exports", "demo-bbb Launch"}) {
		t.Fatalf("epics = %v", v.Epics)
	}
}

func TestDeriveVocabCaps(t *testing.T) {
	var issues []bd.Issue
	for i := 0; i < 50; i++ {
		id := fmt.Sprintf("demo-e%02d", i)
		issues = append(issues,
			bd.Issue{ID: id, Title: "epic " + id, Status: "open", IssueType: "epic"},
			bd.Issue{ID: id + ".1", Title: "c", Status: "open", IssueType: "task", Parent: id,
				Labels: []string{fmt.Sprintf("label-%02d", i)}})
	}
	v := DeriveVocab(issues)
	if len(v.Epics) != maxEpics {
		t.Fatalf("epics = %d, want cap %d", len(v.Epics), maxEpics)
	}
	if len(v.Labels) != maxLabels {
		t.Fatalf("labels = %d, want cap %d", len(v.Labels), maxLabels)
	}
	r := v.Render()
	if got := len(strings.Split(r, "\n")); got > maxVocabLines {
		t.Fatalf("rendered vocab is %d lines, cap %d", got, maxVocabLines)
	}
}

func TestVocabRender(t *testing.T) {
	r := DeriveVocab(vocabFixture()).Render()
	for _, must := range []string{
		`issue ids start with "demo-"`,
		`assignees (exact, quote if spaced): "Alex Rivera"`,
		`"alex@example.com"`,
		"critical-path(2), now(1)",
		"epics/roots (parent=<id> matches their DIRECT children):",
		"  demo-aaa Reporting and exports",
	} {
		if !strings.Contains(r, must) {
			t.Fatalf("render missing %q:\n%s", must, r)
		}
	}
	if DeriveVocab(nil).Render() != "" {
		t.Fatal("empty board must render an empty vocab block")
	}
}
