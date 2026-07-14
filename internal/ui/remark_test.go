package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/awhitty/bb/internal/agentapi"
)

// A shared view's remarks show a row indicator and render near the top of the
// preview and detail panels — without the text ever reaching the issue record.
func TestRemarksIndicatorAndPanels(t *testing.T) {
	m := testModel(t, emphBoard(), 120, 30)
	attachDefault(&m) // attached, so the agent's remarked view applies live
	remarks := map[string]string{"b": "selected because it blocks the release"}
	resp, _ := m.handleAgent(agentapi.SpecAction{IDs: []string{"a", "b", "c"}, Title: "review set", Remarks: remarks})
	if resp.Err != "" {
		t.Fatalf("set_view with remarks err: %s", resp.Err)
	}

	// Row indicator: b carries it, a does not; an unfocused remarked row shows the
	// ✎ gutter glyph.
	if !m.hasRemark("b") || !m.issueEmph("b").remark {
		t.Fatal("b should carry a remark indicator")
	}
	if m.issueEmph("a").remark {
		t.Fatal("a has no remark — it must not show the indicator")
	}
	if !strings.Contains(gutterEmph(false, m.issueEmph("b")), remarkGlyph) {
		t.Fatal("an unfocused remarked row should show the ✎ gutter glyph")
	}

	// The remark never reaches the issue record (it lives only in m.remarks).
	is := m.byID["b"]
	if strings.Contains(is.Title+is.Description+is.Notes, "selected because") {
		t.Fatal("the remark leaked into the issue record — it must stay ephemeral")
	}

	// Preview panel: the remark text is present near the top.
	m.jumpTo("b")
	m.panelOpen = true
	panel := ansi.Strip(m.renderPanel(60, 24))
	if !strings.Contains(panel, "selected because it blocks the release") {
		t.Fatalf("preview panel missing the remark:\n%s", panel)
	}
	if !strings.Contains(panel, remarkGlyph) {
		t.Fatal("preview panel should show the ✎ callout glyph")
	}

	// Detail panel: the remark leads the scrollable body on whichever tab is open.
	for _, tab := range []detailTab{tabOverview, tabHistory, tabRelated} {
		body := ansi.Strip(m.detailTabContent(m.byID["b"], tab, 80))
		if !strings.Contains(body, "selected because") {
			t.Fatalf("detail tab %d missing the remark:\n%s", tab, body)
		}
	}
}

// A remark authored as markdown flows through the same glamour pipeline the body
// uses: inline spans style, raw markers are consumed, and a wrapped bullet hangs
// under its own text with the ✎ marker sitting in the outer margin.
func TestRemarkRendersMarkdown(t *testing.T) {
	m := testModel(t, emphBoard(), 120, 30)
	m.md = newMdRenderer("dark") // exercise the real styled pipeline (testModel uses plain "notty")
	m.setRemarks(map[string]string{"b": "Run `bd ready` then **restart** the node.\n\n" +
		"- first step that is quite long and will certainly wrap onto a second visual line here\n" +
		"- second"})
	lines := m.remarkLines("b", 44)
	if len(lines) == 0 {
		t.Fatal("expected rendered remark lines")
	}

	// The pencil marker leads the first line, in the outer margin (its content
	// starts at the same column as every other line's content).
	if !strings.HasPrefix(ansi.Strip(lines[0]), remarkGlyph+" ") {
		t.Fatalf("first line should lead with the ✎ marker:\n%q", ansi.Strip(lines[0]))
	}
	// No leading OR trailing blank line — glamour's bracketing blanks are stripped.
	if strings.TrimSpace(ansi.Strip(lines[0])) == "" || strings.TrimSpace(ansi.Strip(lines[len(lines)-1])) == "" {
		t.Fatalf("remark callout must be tight (no bracketing blank lines):\n%q\n%q", lines[0], lines[len(lines)-1])
	}

	joined := strings.Join(lines, "\n")
	plain := ansi.Strip(joined)
	// Markdown markers are consumed by the renderer, never shown literally.
	if strings.Contains(plain, "**") || strings.Contains(plain, "`") {
		t.Fatalf("raw markdown markers leaked into the rendered remark:\n%s", plain)
	}
	// Inline styling actually happened (bold + inline code emit ANSI escapes).
	if !strings.Contains(joined, "\x1b[") {
		t.Fatalf("expected ANSI styling in the rendered remark:\n%s", plain)
	}

	// The long bullet wraps; its continuation hangs under the bullet's TEXT (marker
	// width past the bullet glyph), not back under the glyph.
	bulletIdx := -1
	for i, ln := range lines {
		if strings.Contains(ansi.Strip(ln), "•") {
			bulletIdx = i
			break
		}
	}
	if bulletIdx < 0 || bulletIdx+1 >= len(lines) {
		t.Fatalf("expected a wrapped bullet with a continuation line:\n%s", plain)
	}
	bulletLead := leadingSpaces(ansi.Strip(lines[bulletIdx]))
	contLead := leadingSpaces(ansi.Strip(lines[bulletIdx+1]))
	if contLead <= bulletLead {
		t.Fatalf("bullet continuation must hang under the text (bullet lead %d, cont lead %d):\n%s", bulletLead, contLead, plain)
	}
}

// Switching away from a share removes its remarks from the active board;
// reopening the share restores them; nothing is ever written to Beads.
func TestRemarksClearOnSwitchAndRestoreOnReopen(t *testing.T) {
	m := testModel(t, sharesBoard(), 120, 30)
	remarks := map[string]string{"b": "blocks the release"}

	// Publish a remarked view on session s1 — publish-only, not attached yet.
	m.handleAgent(agentapi.SpecAction{Mode: "list", IDs: []string{"a", "b", "c"}, Title: "s1 review", Session: "s1", Remarks: remarks})
	if m.hasRemark("b") {
		t.Fatal("a publish must not apply remarks before the human attaches")
	}

	// Attaching pulls the share — its remarks apply to the active board.
	m = attachSession(t, m, "s1")
	if !m.hasRemark("b") {
		t.Fatal("attaching the share should apply its remarks")
	}
	if r, _ := m.remarkFor("b"); r != "blocks the release" {
		t.Fatalf("remark text = %q", r)
	}

	// Switching away (detach) removes the remarks from the active board.
	m.detach()
	if m.hasRemark("b") {
		t.Fatal("switching away from the share should clear its remarks")
	}
	if m.remarks != nil {
		t.Fatalf("detached board should carry no remark layer, got %+v", m.remarks)
	}

	// Reopening the share restores them.
	m = attachSession(t, m, "s1")
	if r, ok := m.remarkFor("b"); !ok || r != "blocks the release" {
		t.Fatal("reopening the share should restore its remarks")
	}
}

// A share published WITHOUT remarks parses and renders exactly as before — no
// remark layer, no ✎ anywhere.
func TestShareWithoutRemarksRenders(t *testing.T) {
	m := testModel(t, sharesBoard(), 120, 30)
	attachDefault(&m)
	resp, _ := m.handleAgent(agentapi.SpecAction{Mode: "list", IDs: []string{"a", "b", "c"}, Title: "no remarks"})
	if resp.Err != "" {
		t.Fatalf("set_view err: %s", resp.Err)
	}
	if m.remarks != nil {
		t.Fatalf("a share without remarks must leave the remark layer nil, got %+v", m.remarks)
	}
	if m.hasRemark("b") {
		t.Fatal("no remark should be present")
	}
	frame := ansi.Strip(m.View())
	if strings.Contains(frame, remarkGlyph) {
		t.Fatal("no ✎ should render for a share without remarks")
	}
	if !strings.Contains(frame, "B") {
		t.Fatal("the board should still render its beads")
	}
}
