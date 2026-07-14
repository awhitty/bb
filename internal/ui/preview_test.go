package ui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/awhitty/bb/internal/bd"
	"github.com/awhitty/bb/internal/nlq"
)

// The `?` review live-previews the compiled query on the board behind the
// prompt: entering review applies it as a filter, re-roll/edit re-preview, and
// esc/n reverts to exactly the pre-`?` filter while enter/y commits it in place.

func fixtureBoard() []bd.Issue {
	return []bd.Issue{
		{ID: "a", Title: "A", Status: "open", IssueType: "bug"},
		{ID: "b", Title: "B", Status: "open", IssueType: "task"},
		{ID: "c", Title: "C", Status: "closed", IssueType: "bug"},
	}
}

func TestQueryPreviewApplyAndRevert(t *testing.T) {
	m := testModel(t, fixtureBoard(), 120, 30)
	m.activeQuery = "" // pre-`?` filter is empty

	// Entering the review previews the compiled query.
	next, _ := m.Update(nlCompiledMsg{seq: m.compileSeq, res: nlq.Result{NL: "open bugs", Query: "type=bug AND status=open"}})
	m = next.(Model)
	if m.prompt != promptNLReview {
		t.Fatalf("expected review prompt, got %v", m.prompt)
	}
	if !m.previewing {
		t.Fatal("preview should be live once the review opens")
	}
	if m.previewPrevQuery != "" {
		t.Fatalf("pre-`?` filter should be captured verbatim, got %q", m.previewPrevQuery)
	}
	if m.activeQuery != "type=bug AND status=open" {
		t.Fatalf("compiled query should be applied as the live filter, got %q", m.activeQuery)
	}

	// Cancel (esc) restores the exact pre-`?` filter.
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEscape})
	m = next.(Model)
	if m.previewing {
		t.Fatal("cancel must end the preview session")
	}
	if m.activeQuery != "" {
		t.Fatalf("cancel must revert to the pre-`?` filter, got %q", m.activeQuery)
	}
	if m.prompt != promptNone {
		t.Fatalf("cancel must close the prompt, got %v", m.prompt)
	}
}

func TestQueryPreviewCommit(t *testing.T) {
	m := testModel(t, fixtureBoard(), 120, 30)
	m.activeQuery = "status=open" // a pre-existing filter

	next, _ := m.Update(nlCompiledMsg{seq: m.compileSeq, res: nlq.Result{NL: "bugs", Query: "type=bug"}})
	m = next.(Model)
	if m.previewPrevQuery != "status=open" {
		t.Fatalf("prev filter = %q", m.previewPrevQuery)
	}

	// Accept (y) keeps the previewed query as the committed filter.
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	m = next.(Model)
	if m.previewing {
		t.Fatal("commit must end the preview session")
	}
	if m.activeQuery != "type=bug" {
		t.Fatalf("commit must keep the previewed filter, got %q", m.activeQuery)
	}
	if m.prompt != promptNone {
		t.Fatalf("commit must close the prompt, got %v", m.prompt)
	}
}

func TestQueryPreviewRerollKeepsRevertTarget(t *testing.T) {
	m := testModel(t, fixtureBoard(), 120, 30)
	m.activeQuery = "label=x"

	next, _ := m.Update(nlCompiledMsg{seq: m.compileSeq, res: nlq.Result{NL: "q", Query: "type=bug"}})
	m = next.(Model)
	// A re-roll produces a second candidate; the revert target must NOT drift to
	// the first previewed query.
	next, _ = m.Update(nlCompiledMsg{seq: m.compileSeq, res: nlq.Result{NL: "q", Query: "type=task"}})
	m = next.(Model)
	if m.previewPrevQuery != "label=x" {
		t.Fatalf("re-roll must preserve the original pre-`?` filter, got %q", m.previewPrevQuery)
	}
	if m.activeQuery != "type=task" {
		t.Fatalf("re-roll should preview the new candidate, got %q", m.activeQuery)
	}

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEscape})
	m = next.(Model)
	if m.activeQuery != "label=x" {
		t.Fatalf("cancel after re-roll must restore the ORIGINAL filter, got %q", m.activeQuery)
	}
}
