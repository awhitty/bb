package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/awhitty/bb/internal/agentapi"
	"github.com/awhitty/bb/internal/bd"
)

// The overlay + footer are generated from the ONE registry, so every binding's
// keys and help text must appear in the rendered overlay content — no drift.
func TestHelpOverlayGeneratedFromRegistry(t *testing.T) {
	m := testModel(t, deepColumn(3), 120, 30)
	content := m.renderHelpContent(120)

	if !strings.Contains(content, "Keybindings") {
		t.Fatal("overlay missing title")
	}
	for _, cat := range m.keys.categories() {
		if !strings.Contains(content, cat.name) {
			t.Fatalf("overlay missing category %q", cat.name)
		}
		for _, b := range cat.bindings {
			h := b.Help()
			if h.Key != "" && !strings.Contains(content, h.Key) {
				t.Fatalf("overlay missing key %q (%s)", h.Key, h.Desc)
			}
			if h.Desc != "" && !strings.Contains(content, h.Desc) {
				t.Fatalf("overlay missing desc %q", h.Desc)
			}
		}
	}
}

// The overlay must fit the terminal at any height — short terminals scroll the
// viewport, they never overflow the frame. frameLines fails if the frame is
// not exactly h lines or any line exceeds w.
func TestHelpOverlayFitsTerminal(t *testing.T) {
	for _, h := range []int{8, 12, 24, 50} {
		const w = 100
		m := testModel(t, deepColumn(3), w, h)
		m = press(t, m, "?")
		if !m.showHelp {
			t.Fatalf("? did not open the overlay (h=%d)", h)
		}
		frameLines(t, m, w, h) // asserts == h lines, each ≤ w wide
	}
}

func TestHelpToggleAndClose(t *testing.T) {
	m := testModel(t, deepColumn(3), 100, 30)
	m = press(t, m, "?")
	if !m.showHelp {
		t.Fatal("? should open help")
	}
	m = press(t, m, "?")
	if m.showHelp {
		t.Fatal("? again should close help")
	}
	m = press(t, m, "?")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEscape})
	m = next.(Model)
	if m.showHelp {
		t.Fatal("esc should close help")
	}
}

// The footer short help is driven by the same registry.
func TestFooterHintsFromRegistry(t *testing.T) {
	m := testModel(t, deepColumn(3), 120, 30)
	m.resolving = false
	m.setMessage("", false)
	foot := m.viewFooter()
	for _, want := range []string{"move", "filter or ask", "?", "help"} {
		if !strings.Contains(foot, want) {
			t.Fatalf("footer missing %q\nfooter: %q", want, foot)
		}
	}
}

// `/` is the smart prompt: labelled "filter or ask", it routes a query-shaped
// string to a raw filter and plain English to the NL compile flow.
func TestSmartPromptRouting(t *testing.T) {
	m := testModel(t, deepColumn(3), 120, 30)
	m = press(t, m, "/")
	if m.prompt != promptQuery {
		t.Fatalf("/ should open the query prompt, got %v", m.prompt)
	}
	if m.input.Prompt != "filter or ask> " {
		t.Fatalf("prompt label = %q", m.input.Prompt)
	}

	// Query-shaped → raw filter, no NL compile, no review.
	m.input.SetValue("status=open")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.activeQuery != "status=open" {
		t.Fatalf("query-shaped input should set activeQuery, got %q", m.activeQuery)
	}
	if m.compiling || m.previewing || m.prompt == promptNLReview {
		t.Fatal("query-shaped input must not enter the NL compile flow")
	}

	// Plain English → NL compile flow (needs a resolved model).
	m.resolved = true
	m.resolving = false
	m = press(t, m, "/")
	m.input.SetValue("urgent things about exports")
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if !m.compiling {
		t.Fatal("plain-English input should start the NL compile flow")
	}
}

// `@` opens the master-detail shares browser (a section per view, its beads
// inline, the preview following focus); `a` stays the activity feed (the old
// a/@ collision resolved).
func TestAgentSharesKey(t *testing.T) {
	board := []bd.Issue{{ID: "a", Title: "A", Status: "open"}, {ID: "b", Title: "B", Status: "open"}}
	m := testModel(t, board, 120, 30)
	m.handleAgent(agentapi.NameDropAction{SessionID: "s", TS: "2026-07-08T10:00:00Z", IDs: []string{"b"}})

	m = press(t, m, "@")
	if !m.sharesBrowse {
		t.Fatal("@ should open the shares browser")
	}
	// The mentioned section resolves its ids against the board and focus lands
	// on that bead so the preview follows.
	if len(m.shareSections) != 1 || len(m.shareSections[0].beads) != 1 || m.shareSections[0].beads[0].ID != "b" {
		t.Fatalf("section did not resolve to [b]: %+v", m.shareSections)
	}
	if is := m.shareBrowseFocused(); is == nil || is.ID != "b" {
		t.Fatalf("focus should be on the resolved bead b, got %v", is)
	}
	m = press(t, m, "esc")
	if m.sharesBrowse {
		t.Fatal("esc should close the shares browser")
	}

	// `a` must open the activity feed, not the shares surface.
	m2 := testModel(t, board, 120, 30)
	m2.handleAgent(agentapi.NameDropAction{SessionID: "s", TS: "2026-07-08T10:00:00Z", IDs: []string{"b"}})
	m2 = press(t, m2, "a")
	if m2.sharesBrowse || m2.attach.active {
		t.Fatal("a must not open the shares surface")
	}
	if !m2.activityView {
		t.Fatal("a should open the activity feed")
	}
}
