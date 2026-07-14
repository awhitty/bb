package ui

import (
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/log"
	"github.com/charmbracelet/x/ansi"

	"github.com/awhitty/bb/internal/agentapi"
	"github.com/awhitty/bb/internal/bd"
	"github.com/awhitty/bb/internal/nlq"
)

func testModel(t *testing.T, issues []bd.Issue, w, h int) Model {
	t.Helper()
	// Isolate the persistent session-channel store to a temp dir so tests never
	// touch the real ~/.config/bb/sessions.json (and don't leak state
	// between tests).
	t.Setenv("BB_CONFIG_DIR", t.TempDir())
	m := New(bd.NewClient(func(args ...string) (string, error) { return "[]", nil }),
		&nlq.Provider{Model: "m", URL: "http://127.0.0.1:1", Label: "m"},
		&nlq.Analyst{Model: "a", URL: "http://127.0.0.1:1", Label: "a"},
		&nlq.FeedbackLog{Path: t.TempDir() + "/log.jsonl"},
		log.New(io.Discard), "notty")
	next, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	m = next.(Model)
	next, _ = m.Update(issuesMsg{seq: 0, issues: issues})
	tm := next.(Model)
	// Not attached by default (production publish-don't-seize): agent pushes only
	// publish. Tests that exercise the live-apply / knob-driving path call
	// attachDefault(&m) first, the stand-in for the human pressing A on a channel.
	return tm
}

// attachDefault attaches the model to the unattributed default channel ("") so
// agent pushes to it apply to the board live — the test-side stand-in for the
// human pressing A/enter on that channel in the sessions browser.
func attachDefault(m *Model) {
	m.attach.active = true
	m.attach.session = ""
	m.attach.prev = m.snapshotAttach()
	m.pushLayer(layerAttach)
}

func deepColumn(n int) []bd.Issue {
	issues := make([]bd.Issue, n)
	for i := range issues {
		issues[i] = bd.Issue{
			ID:        fmt.Sprintf("g-%03d", i),
			Title:     fmt.Sprintf("card number %d with a reasonably long title", i),
			Status:    "open",
			Priority:  i % 5,
			IssueType: "task",
		}
	}
	return issues
}

func frameLines(t *testing.T, m Model, w, h int) []string {
	t.Helper()
	view := m.View()
	lines := strings.Split(view, "\n")
	if len(lines) != h {
		t.Fatalf("frame is %d lines, terminal is %d", len(lines), h)
	}
	for i, l := range lines {
		if ansi.StringWidth(l) > w {
			t.Fatalf("line %d is %d wide, terminal is %d", i, ansi.StringWidth(l), w)
		}
	}
	return lines
}

func diffCount(a, b []string) int {
	n := 0
	for i := range a {
		if a[i] != b[i] {
			n++
		}
	}
	return n
}

// The performance bar: holding j through a 73-card column must keep frames
// to a tiny line diff (bubbletea's renderer repaints only changed lines).
// Inside a page the diff is the two cards whose focus flag flipped (plus the
// header row-independent parts staying identical); a page jump may rewrite
// the column, but only once per cardWindow presses.
func TestHeldKeyScrollDiffsStaySmall(t *testing.T) {
	const w, h = 120, 30
	m := testModel(t, deepColumn(73), w, h)

	prev := frameLines(t, m, w, h)
	cardWindow := h - 2 - 5
	var jumps, bigNonJumpFrames int
	for press := 0; press < 72; press++ {
		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
		m = next.(Model)
		cur := frameLines(t, m, w, h)
		d := diffCount(prev, cur)
		// A normal in-window move repaints the two focus-marker rows plus the
		// header's focused-id (3 lines). A page jump rewrites the whole window.
		if d > 6 { // page-jump frame
			jumps++
		} else if d > 3 {
			bigNonJumpFrames++
		}
		prev = cur
	}
	maxJumps := 72/cardWindow + 1
	if jumps > maxJumps {
		t.Fatalf("%d page-jump frames for 72 presses (cardWindow %d, max %d) — window is sliding, not page-jumping", jumps, cardWindow, maxJumps)
	}
	if bigNonJumpFrames > 0 {
		t.Fatalf("%d frames diffed >3 lines outside page jumps — held-key scroll not smooth", bigNonJumpFrames)
	}
}

// Root-style boards can have dozens of columns; the frame must still fit and
// the focused column must be inside the horizontal window.
func TestManyColumnsFitAndFollowFocus(t *testing.T) {
	const w, h = 100, 24
	// Short ids so a column title survives truncation at minimum width.
	var issues []bd.Issue
	for i := 0; i < 40; i++ {
		issues = append(issues, bd.Issue{
			ID: fmt.Sprintf("r%02d", i), Title: "x", Status: "open", IssueType: "task",
		})
	}
	m := testModel(t, issues, w, h)
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}}) // root mode
	m = next.(Model)
	frameLines(t, m, w, h)

	// March focus right across every column; each frame must contain the
	// focused column's title.
	for i := 0; i < 39; i++ {
		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'l'}})
		m = next.(Model)
		view := strings.Join(frameLines(t, m, w, h), "\n")
		want := fmt.Sprintf("r%02d", m.colIdx)
		if !strings.Contains(view, want) {
			t.Fatalf("focused column %s not visible after %d moves", want, i+1)
		}
	}
}

// The full frame must stay inside the terminal in every mode and with the
// detail view open.
func TestFramesFitInAllModes(t *testing.T) {
	const w, h = 80, 20
	m := testModel(t, deepColumn(30), w, h)
	for _, k := range []string{"1", "2", "3", "4"} {
		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)})
		m = next.(Model)
		frameLines(t, m, w, h)
	}
	// Detail view with a long markdown body.
	long := bd.Issue{ID: "g-000", Title: "Detail", Status: "open", IssueType: "task",
		Description: strings.Repeat("A paragraph with `code` and a [link](https://x.test).\n\n", 40),
		Notes:       "- one\n- two"}
	next, _ := m.Update(detailMsg{issue: long})
	m = next.(Model)
	frameLines(t, m, w, h)
	// Scroll it.
	for i := 0; i < 5; i++ {
		next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
		m = next.(Model)
		frameLines(t, m, w, h)
	}
}

// Optimistic priority: '-' lowers the focused card's priority before bd
// returns, and focus stays on the card even as it reorders within its column.
func TestPriorityChangeIsOptimistic(t *testing.T) {
	const w, h = 100, 24
	m := testModel(t, deepColumn(3), w, h)
	before := m.focusedIssue()
	if before == nil {
		t.Fatal("no focused issue")
	}
	id, p0 := before.ID, before.Priority
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'-'}})
	m = next.(Model)
	got := -1
	for _, is := range m.issues {
		if is.ID == id {
			got = is.Priority
		}
	}
	if got != p0+1 {
		t.Fatalf("priority of %s = %d, want %d", id, got, p0+1)
	}
	if f := m.focusedIssue(); f == nil || f.ID != id {
		t.Fatalf("focus moved off %s after reprioritize", id)
	}
	if want := fmt.Sprintf("%s → P%d", id, p0+1); !strings.Contains(m.message, want) {
		t.Fatalf("message = %q, want contains %q", m.message, want)
	}
}

// Raising past the ceiling is a no-op with a clear notice, not a bd call.
func TestPriorityClampAtHighest(t *testing.T) {
	const w, h = 100, 24
	m := testModel(t, deepColumn(3), w, h) // focus starts on g-000, P0
	before := m.focusedIssue()
	if before == nil || before.Priority != 0 {
		t.Fatalf("expected focus on a P0 card, got %+v", before)
	}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'+'}})
	m = next.(Model)
	if f := m.focusedIssue(); f == nil || f.Priority != 0 {
		t.Fatalf("priority changed past ceiling: %+v", f)
	}
	if !strings.Contains(m.message, "already at highest") {
		t.Fatalf("message = %q", m.message)
	}
}

// An external store change reloads while keeping focus on the same issue id
// even as priority reorders it, and announces "board updated".
func TestAutoRefreshPreservesFocusById(t *testing.T) {
	const w, h = 100, 24
	m := testModel(t, deepColumn(3), w, h)
	if !m.jumpTo("g-002") {
		t.Fatal("could not focus g-002")
	}
	// Simulate the watcher-driven reload: capture focus, mark auto, new gen.
	m.focusIDBefore = "g-002"
	m.autoRefresh = true
	m.seq = 1
	updated := []bd.Issue{
		{ID: "g-000", Status: "open", Priority: 0, IssueType: "task"},
		{ID: "g-001", Status: "open", Priority: 1, IssueType: "task"},
		{ID: "g-002", Status: "open", Priority: 0, IssueType: "task"}, // was P2, now reorders to front
		{ID: "g-003", Status: "open", Priority: 3, IssueType: "task"}, // newly created elsewhere
	}
	next, _ := m.Update(issuesMsg{seq: 1, issues: updated})
	m = next.(Model)
	if f := m.focusedIssue(); f == nil || f.ID != "g-002" {
		t.Fatalf("focus not preserved by id: %+v", f)
	}
	if !strings.Contains(m.message, "board updated") {
		t.Fatalf("expected board-updated notice, got %q", m.message)
	}
}

// The slow poll reloads unconditionally; an unchanged board must stay silent
// even when bd returns the same issues in a different order (a priority edit
// bumps updated_at, which reorders `bd list` — the signature must not care).
func TestAutoRefreshUnchangedIsSilent(t *testing.T) {
	const w, h = 100, 24
	issues := deepColumn(3)
	m := testModel(t, issues, w, h)
	m.setMessage("", false)
	m.autoRefresh = true
	m.seq = 1
	reordered := []bd.Issue{issues[2], issues[0], issues[1]} // same set, shuffled
	next, _ := m.Update(issuesMsg{seq: 1, issues: reordered})
	m = next.(Model)
	if strings.Contains(m.message, "board updated") {
		t.Fatalf("unchanged (reordered) poll should stay silent, got %q", m.message)
	}
}

// A store change arriving mid-prompt is deferred, never interrupting input.
func TestBoardChangeDefersDuringPrompt(t *testing.T) {
	const w, h = 100, 24
	m := testModel(t, deepColumn(3), w, h)
	m = press(t, m, "/") // open the query prompt
	seqBefore := m.seq
	next, _ := m.Update(BoardChangedMsg{})
	m = next.(Model)
	if !m.pendingRefresh {
		t.Fatal("store change during a prompt must defer")
	}
	if m.seq != seqBefore {
		t.Fatalf("no reload should fire while a prompt is open (seq %d→%d)", seqBefore, m.seq)
	}
}

func TestTreeViewRendersAndNavigates(t *testing.T) {
	const w, h = 120, 30
	issues := []bd.Issue{
		{ID: "g-1", Title: "Epic", Status: "open", Priority: 1, IssueType: "epic", UpdatedAt: "2026-07-07T00:00:00Z"},
		{ID: "g-1.1", Title: "Child A", Status: "in_progress", Priority: 0, IssueType: "task", Parent: "g-1", UpdatedAt: "2026-07-08T00:00:00Z"},
		{ID: "g-1.2", Title: "Child B", Status: "open", Priority: 2, IssueType: "bug", Parent: "g-1"},
		{ID: "g-2", Title: "Loner", Status: "open", Priority: 3, IssueType: "chore",
			Dependencies: []bd.Dependency{{IssueID: "g-2", DependsOnID: "g-1", Type: "blocks"}, {IssueID: "g-2", DependsOnID: "g-1.1", Type: "blocks"}}},
	}
	m := testModel(t, issues, w, h)
	m = press(t, m, "5")
	if m.view != ViewTree {
		t.Fatal("key 5 must open the tree view")
	}
	view := strings.Join(frameLines(t, m, w, h), "\n")
	for _, must := range []string{"├─", "└─", "P0", "◐", "Child A", "tree (hierarchy ↓)"} {
		if !strings.Contains(view, must) {
			t.Fatalf("tree view missing %q:\n%s", must, view)
		}
	}
	// Hierarchy: 4 rows, g-1 first with two children indented.
	if len(m.treeRows) != 4 || m.treeRows[0].issue.ID != "g-1" || m.treeRows[1].prefix == "" {
		t.Fatalf("rows = %+v", m.treeRows)
	}
	// d opens the traverse control; step to "dependencies ↓" (index 2) and commit.
	// g-2 then nests under g-1 with +1 deps and the strip flips to deps.
	m = press(t, m, "d", "j", "j", "enter")
	view = strings.Join(frameLines(t, m, w, h), "\n")
	if !strings.Contains(view, "tree (deps ↓)") || !strings.Contains(view, "+1 deps") {
		t.Fatalf("deps tree missing markers:\n%s", view)
	}
	// Linear nav + focus target.
	m = press(t, m, "j", "j")
	if is := (&m).focusedIssue(); is == nil {
		t.Fatal("tree focus lost")
	}
	m = press(t, m, "G")
	if m.treeIdx != len(m.treeRows)-1 {
		t.Fatalf("G → %d", m.treeIdx)
	}
	// Panel works in the tree too.
	m = press(t, m, " ")
	frameLines(t, m, w, h)
	// Held-j through a deep tree keeps fitting.
	m = testModel(t, deepColumn(73), w, h)
	m = press(t, m, "5")
	for i := 0; i < 72; i++ {
		m = press(t, m, "j")
		frameLines(t, m, w, h)
	}
}

// TestTreeTraverseControlInvertsDirection drives the traverse control's
// direction axis: opening it in the tree and committing "hierarchy ↑" reverses
// the nesting — a former child becomes a root and the former root sinks beneath
// it — while the strip flips its arrow and the frame keeps fitting.
func TestTreeTraverseControlInvertsDirection(t *testing.T) {
	const w, h = 120, 30
	issues := []bd.Issue{
		{ID: "g-1", Title: "Epic", Status: "open", Priority: 1, IssueType: "epic"},
		{ID: "g-1.1", Title: "Child A", Status: "open", Priority: 0, IssueType: "task", Parent: "g-1"},
		{ID: "g-1.2", Title: "Child B", Status: "open", Priority: 2, IssueType: "bug", Parent: "g-1"},
	}
	m := testModel(t, issues, w, h)
	m = press(t, m, "5")
	if m.treeRows[0].issue.ID != "g-1" || m.treeRows[0].depth != 0 {
		t.Fatalf("forward tree: first row = %+v", m.treeRows[0])
	}
	view := strings.Join(frameLines(t, m, w, h), "\n")
	if !strings.Contains(view, "tree (hierarchy ↓)") {
		t.Fatalf("forward strip missing ↓:\n%s", view)
	}

	// d opens the traverse control; "hierarchy ↑" is index 1. Step once and commit.
	m = press(t, m, "d", "j", "enter")
	if !m.treeDir || m.depsTree {
		t.Fatalf("after invert: treeDir=%v depsTree=%v", m.treeDir, m.depsTree)
	}
	// The former child g-1.1 is now a root; the former root g-1 sinks beneath it.
	if m.treeRows[0].issue.ID != "g-1.1" || m.treeRows[0].depth != 0 {
		t.Fatalf("inverted tree: first row = %+v (rows %+v)", m.treeRows[0], m.treeRows)
	}
	var g1Depth = -1
	for _, r := range m.treeRows {
		if r.issue.ID == "g-1" {
			g1Depth = r.depth
		}
	}
	if g1Depth <= 0 {
		t.Fatalf("inverted tree: g-1 should nest below a descendant, depth=%d", g1Depth)
	}
	view = strings.Join(frameLines(t, m, w, h), "\n")
	if !strings.Contains(view, "tree (hierarchy ↑)") {
		t.Fatalf("inverted strip missing ↑:\n%s", view)
	}
}

// Every nav view (viewKind) must render inside the terminal frame: exactly h
// lines, none wider than w. This is the anti-overflow floor for the whole
// view-layer refactor — a new mode that overflows fails here.
func TestEveryViewKindFramesFit(t *testing.T) {
	const w, h = 120, 30
	cases := []struct {
		name string
		keys []string
		want viewKind
	}{
		{"kanban", nil, ViewKanban}, // the boot default
		{"list", []string{"b"}, ViewList},
		{"tree", []string{"5"}, ViewTree},
		{"swim", []string{"R"}, ViewSwim},
		{"columns", []string{"C"}, ViewColumns},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := testModel(t, graphFixture(), w, h)
			m = press(t, m, tc.keys...)
			if m.view != tc.want {
				t.Fatalf("view = %v, want %v", m.view, tc.want)
			}
			frameLines(t, m, w, h) // == h lines, each ≤ w wide
		})
	}
}

// The activity feed is an OVERLAY, not a peer of the nav views: it composes over
// whatever view is beneath, and esc drops the overlay to reveal it again. So
// tree → activity → esc must land back on the tree, never the default board.
func TestActivityOverlayFallsBackToNavView(t *testing.T) {
	const w, h = 120, 30
	m := testModel(t, graphFixture(), w, h)
	m = press(t, m, "5") // enter the tree
	if m.view != ViewTree {
		t.Fatalf("5 should enter the tree, view = %v", m.view)
	}
	m = press(t, m, "a") // the activity feed overlays the tree
	if !m.activityView {
		t.Fatal("a should open the activity feed")
	}
	if m.view != ViewTree {
		t.Fatal("entering activity must not change the nav view beneath it")
	}
	frameLines(t, m, w, h) // the overlay still fits the frame
	m = press(t, m, "esc")
	if m.activityView {
		t.Fatal("esc should drop the activity overlay")
	}
	if m.view != ViewTree {
		t.Fatalf("esc should fall back to the tree beneath, got view = %v", m.view)
	}
}

// The shares browser is the second overlay with the same contract: swim →
// shares browser → esc returns to the swimlane, not the default board.
func TestSharesBrowserOverlayFallsBackToNavView(t *testing.T) {
	const w, h = 120, 30
	m := testModel(t, graphFixture(), w, h) // not attached: publish-don't-seize
	m = press(t, m, "R")                    // enter the relationship swimlane
	if m.view != ViewSwim {
		t.Fatalf("R should enter the swimlane, view = %v", m.view)
	}
	// Publish a share so the browser has a section to show.
	m.handleAgent(agentapi.SpecAction{Mode: "tree", Title: "a shared tree"})
	if m.view != ViewSwim {
		t.Fatal("a follow-off publish must not seize the view")
	}
	m = press(t, m, "@") // open the shares browser over the swimlane
	if !m.sharesBrowse {
		t.Fatal("@ should open the shares browser")
	}
	frameLines(t, m, w, h)
	m = press(t, m, "esc")
	if m.sharesBrowse {
		t.Fatal("esc should close the shares browser")
	}
	if m.view != ViewSwim {
		t.Fatalf("esc should fall back to the swimlane beneath, got view = %v", m.view)
	}
}

func TestRelAge(t *testing.T) {
	now, _ := time.Parse(time.RFC3339, "2026-07-08T12:00:00Z")
	cases := map[string]string{
		"2026-07-08T11:59:40Z": "now",
		"2026-07-08T11:23:00Z": "37m ago",
		"2026-07-08T09:00:00Z": "3h ago",
		"2026-07-03T12:00:00Z": "5d ago",
		"2026-06-03T12:00:00Z": "5w ago",
		"2026-01-08T12:00:00Z": "6mo ago",
		"":                     "",
	}
	for in, want := range cases {
		if got := relAge(in, now); got != want {
			t.Fatalf("relAge(%q) = %q, want %q", in, got, want)
		}
	}
}
