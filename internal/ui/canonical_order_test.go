package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/awhitty/bb/internal/bd"
	"github.com/awhitty/bb/internal/rollup"
)

// The canonical facet order is the DEFAULT for every grouped surface. These are
// frame-level proofs (they read the rendered view, not the engine directly) that
// a section's position always reads as its semantic rank, never its card count.

// firstLineOf returns the index of the first frame line (past the header strip)
// containing sub, failing the test if none does.
func firstLineOf(t *testing.T, lines []string, sub string) int {
	t.Helper()
	for i := 1; i < len(lines); i++ {
		if strings.Contains(lines[i], sub) {
			return i
		}
	}
	t.Fatalf("%q not found in frame:\n%s", sub, strings.Join(lines, "\n"))
	return -1
}

// (a) Break-out lanes by priority render bands strictly P0 → P1 → P2 top to
// bottom even when P2 holds the MOST cards: canonical rank wins, count cannot.
func TestLaneBandsPriorityRankBeatsCount(t *testing.T) {
	const w, h = 140, 40
	issues := []bd.Issue{
		{ID: "u0", Title: "u0", Status: "open", Priority: 0, IssueType: "task"},
		{ID: "u1", Title: "u1", Status: "open", Priority: 1, IssueType: "task"},
		{ID: "c1", Title: "c1", Status: "open", Priority: 2, IssueType: "task"},
		{ID: "c2", Title: "c2", Status: "open", Priority: 2, IssueType: "task"},
		{ID: "c3", Title: "c3", Status: "open", Priority: 2, IssueType: "task"},
		{ID: "c4", Title: "c4", Status: "open", Priority: 2, IssueType: "task"},
	}
	m := testModel(t, issues, w, h) // ViewKanban, grouped by status
	m = press(t, m, "L")
	m = focusGroup(t, m, "priority")
	m = press(t, m, "enter")
	if m.boardLane != rollup.FacetPriority {
		t.Fatalf("boardLane = %q, want priority", m.boardLane)
	}
	lines := strings.Split(ansi.Strip(m.View()), "\n")
	p0 := firstLineOf(t, lines, "─ P0")
	p1 := firstLineOf(t, lines, "─ P1")
	p2 := firstLineOf(t, lines, "─ P2")
	if !(p0 < p1 && p1 < p2) {
		t.Fatalf("priority bands P0=%d P1=%d P2=%d, want P0 < P1 < P2 even though P2 has the most cards", p0, p1, p2)
	}
}

// (b) Board columns grouped by status render left to right in lifecycle order,
// regardless of the input order or which status has the most cards.
func TestBoardColumnsStatusLifecycleOrder(t *testing.T) {
	const w, h = 160, 40
	issues := []bd.Issue{
		{ID: "z1", Title: "z1", Status: "closed", IssueType: "task"},
		{ID: "z2", Title: "z2", Status: "closed", IssueType: "task"},
		{ID: "z3", Title: "z3", Status: "closed", IssueType: "task"}, // closed has the most cards
		{ID: "o1", Title: "o1", Status: "open", IssueType: "task"},
		{ID: "b1", Title: "b1", Status: "blocked", IssueType: "task"},
		{ID: "p1", Title: "p1", Status: "in_progress", IssueType: "task"},
	}
	m := testModel(t, issues, w, h) // default status board
	lines := strings.Split(ansi.Strip(m.View()), "\n")

	// The column headers share one body row; find it (it carries both the first
	// and last lifecycle columns) and read the statuses left to right.
	var hdr string
	for _, l := range lines {
		if strings.Contains(l, "open") && strings.Contains(l, "closed") {
			hdr = l
			break
		}
	}
	if hdr == "" {
		t.Fatalf("no column-header row carrying open and closed:\n%s", strings.Join(lines, "\n"))
	}
	prev := -1
	for _, s := range []string{"open", "in_progress", "blocked", "closed"} {
		i := strings.Index(hdr, s)
		if i < 0 {
			t.Fatalf("status column %q missing from header row %q", s, hdr)
		}
		if i <= prev {
			t.Fatalf("status columns out of lifecycle order in %q: %q at x=%d, previous at x=%d", hdr, s, i, prev)
		}
		prev = i
	}
}

// (c) A list grouped by type renders its sections top to bottom in canonical
// type order (containers first), never by card count.
func TestListSectionsTypeCanonicalOrder(t *testing.T) {
	const w, h = 120, 40
	issues := []bd.Issue{
		{ID: "b1", Title: "b1", Status: "open", IssueType: "bug"},
		{ID: "b2", Title: "b2", Status: "open", IssueType: "bug"}, // bug has the most cards
		{ID: "b3", Title: "b3", Status: "open", IssueType: "bug"},
		{ID: "e1", Title: "e1", Status: "open", IssueType: "epic"},
		{ID: "t1", Title: "t1", Status: "open", IssueType: "task"},
		{ID: "c1", Title: "c1", Status: "open", IssueType: "chore"},
	}
	m := testModel(t, issues, w, h)
	m = press(t, m, "b") // list view
	m.applyGroup(rollup.FacetType)
	lines := strings.Split(ansi.Strip(m.View()), "\n")

	prev := -1
	for _, s := range []string{"epic", "task", "bug", "chore"} {
		li := firstLineOf(t, lines, s)
		if li <= prev {
			t.Fatalf("type section %q on line %d, out of canonical order (previous section on line %d)", s, li, prev)
		}
		prev = li
	}
}

// (d) The empty bucket sorts LAST: a type facet with an untyped issue renders the
// "untyped" section after every populated section.
func TestListNoneBucketSortsLast(t *testing.T) {
	const w, h = 120, 40
	issues := []bd.Issue{
		{ID: "e1", Title: "e1", Status: "open", IssueType: "epic"},
		{ID: "u1", Title: "u1", Status: "open"}, // no type → untyped bucket
		{ID: "t1", Title: "t1", Status: "open", IssueType: "task"},
		{ID: "g1", Title: "g1", Status: "open", IssueType: "bug"},
	}
	m := testModel(t, issues, w, h)
	m = press(t, m, "b") // list view
	m.applyGroup(rollup.FacetType)
	lines := strings.Split(ansi.Strip(m.View()), "\n")

	untyped := firstLineOf(t, lines, "untyped")
	for _, s := range []string{"epic", "task", "bug"} {
		if firstLineOf(t, lines, s) > untyped {
			t.Fatalf("section %q renders after the untyped none-bucket (line %d); the empty bucket must sort last", s, untyped)
		}
	}
}
