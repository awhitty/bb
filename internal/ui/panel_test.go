package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/awhitty/bb/internal/bd"
)

func graphFixture() []bd.Issue {
	return []bd.Issue{
		{ID: "g-1", Title: "Epic one", Status: "open", Priority: 1, IssueType: "epic"},
		{ID: "g-1.1", Title: "Child task", Status: "open", Priority: 0, IssueType: "task", Parent: "g-1",
			Dependencies: []bd.Dependency{{IssueID: "g-1.1", DependsOnID: "g-1", Type: "parent-child"}}},
		{ID: "g-2", Title: "Blocked bug", Status: "open", Priority: 2, IssueType: "bug",
			Description:  "Needs g-3 first.",
			Dependencies: []bd.Dependency{{IssueID: "g-2", DependsOnID: "g-3", Type: "blocks"}}},
		{ID: "g-3", Title: "Load-bearing task", Status: "open", Priority: 1, IssueType: "task", DependentCount: 1},
	}
}

func press(t *testing.T, m Model, keys ...string) Model {
	t.Helper()
	for _, k := range keys {
		var msg tea.KeyMsg
		switch k {
		case "enter":
			msg = tea.KeyMsg{Type: tea.KeyEnter}
		case "tab":
			msg = tea.KeyMsg{Type: tea.KeyTab}
		case "esc":
			msg = tea.KeyMsg{Type: tea.KeyEscape}
		case " ":
			msg = tea.KeyMsg{Type: tea.KeySpace}
		default:
			msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
		}
		next, _ := m.Update(msg)
		m = next.(Model)
	}
	return m
}

func TestPanelToggleRendersAndFits(t *testing.T) {
	const w, h = 120, 30
	m := testModel(t, graphFixture(), w, h)
	m = press(t, m, " ")
	if !m.panelOpen {
		t.Fatal("space must open the panel")
	}
	view := strings.Join(frameLines(t, m, w, h), "\n")
	// The focused card (g-1.1, P0 sorts first in open) previews live, with its
	// octopus (ancestors ↑ → its parent Epic one).
	if !strings.Contains(view, "Child task") || !strings.Contains(view, "ancestors") || !strings.Contains(view, "Epic one") {
		t.Fatalf("panel content missing:\n%s", view)
	}
	// Board j updates the preview while board keeps focus.
	m = press(t, m, "j")
	view = strings.Join(frameLines(t, m, w, h), "\n")
	if m.panelFocus {
		t.Fatal("board j must not steal panel focus")
	}
	m = press(t, m, " ")
	if m.panelOpen {
		t.Fatal("space must close the panel")
	}
	frameLines(t, m, w, h)
}

func TestPanelTravelAcrossLinks(t *testing.T) {
	const w, h = 120, 30
	m := testModel(t, graphFixture(), w, h)
	// Focus g-2 (row 2 of the open column: g-1.1 P0, g-1 P1, g-3 P1, g-2 P2 → row 3).
	if !(&m).jumpTo("g-2") {
		t.Fatal("fixture issue g-2 not on board")
	}
	m = press(t, m, " ", "tab")
	if !m.panelFocus {
		t.Fatal("tab must focus the panel")
	}
	// g-2's octopus ancestor is its blocker g-3 (first in the flat); enter travels there.
	_, flat := (&m).octopusBody("g-2", 60, -1)
	if len(flat) == 0 || flat[0] != "g-3" {
		t.Fatalf("g-2 octopus flat = %v (want g-3 first)", flat)
	}
	m = press(t, m, "enter")
	if got := (&m).previewIssue(); got == nil || got.ID != "g-3" {
		t.Fatalf("board selection after travel = %+v", got)
	}
	if !m.panelFocus {
		t.Fatal("travel keeps the panel focused for the next hop")
	}
	// esc returns to the board; frame still fits.
	m = press(t, m, "esc")
	if m.panelFocus {
		t.Fatal("esc must return focus to the board")
	}
	frameLines(t, m, w, h)
}

func TestPanelParentAndChildLinks(t *testing.T) {
	m := testModel(t, graphFixture(), 120, 30)
	links := (&m).relatedLinks(m.byID["g-1"])
	if len(links) != 1 || links[0].kind != linkChild || links[0].id != "g-1.1" {
		t.Fatalf("g-1 links = %+v", links)
	}
	links = (&m).relatedLinks(m.byID["g-1.1"])
	if len(links) != 1 || links[0].kind != linkParent || links[0].id != "g-1" {
		t.Fatalf("g-1.1 links = %+v", links)
	}
}

func TestHeldJWithPanelStillFits(t *testing.T) {
	const w, h = 120, 30
	m := testModel(t, deepColumn(73), w, h)
	m = press(t, m, " ")
	for i := 0; i < 72; i++ {
		m = press(t, m, "j")
		frameLines(t, m, w, h)
	}
}
