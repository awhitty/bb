package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// remark.go is the ephemeral, issue-keyed remark layer — a shared view's answer
// to "WHY was this bead selected?", shown to a reviewer without ever touching the
// issue record. It is deliberately shallow (mirroring emphasis.go): the model
// holds one ephemeral map[bead-id]text; the row gutter shows a width-1 indicator
// and the preview/detail panels render the text near their top. The remarks ride
// WITH the active share — applyShare sets them, detach/channel-switch clears them
// — so nothing here is ever persisted to Beads.

// remarkGlyph is the width-1 row indicator: a pencil (a WRITTEN note is attached
// to this bead). colRemark is its defined color — bright blue, distinct from every
// hue already in use (focus cyan, emphasis yellow/magenta/cyan, blocked red/orange,
// the status hues) and theme-mapped so it reads on both backgrounds.
const remarkGlyph = "✎"

var (
	colRemark = lipgloss.Color("12") // bright blue
	styRemark = lipgloss.NewStyle().Foreground(colRemark)
)

// setRemarks replaces the active remark layer (applied when a share is applied
// live). An empty/nil map clears it.
func (m *Model) setRemarks(r map[string]string) {
	if len(r) == 0 {
		m.remarks = nil
		return
	}
	m.remarks = r
}

// remarkFor returns a bead's active remark text (and whether it has one). A
// whitespace-only remark counts as absent, so a blank entry never draws an
// indicator or an empty callout.
func (m Model) remarkFor(id string) (string, bool) {
	if m.remarks == nil {
		return "", false
	}
	r, ok := m.remarks[id]
	if !ok || strings.TrimSpace(r) == "" {
		return "", false
	}
	return r, true
}

// hasRemark reports whether a bead carries an active remark (drives the row
// indicator in issueEmph).
func (m Model) hasRemark(id string) bool {
	_, ok := m.remarkFor(id)
	return ok
}

// remarkLines renders a bead's remark as a small callout wrapped to width w. The
// text flows through the SAME glamour markdown pipeline the detail/preview body
// uses (m.md.render): bold/italic/inline-code spans style, fenced code renders as
// a block, and wrapped bullet/ordinal lines hang under their content — so a
// remark reads exactly like the issue body it annotates. The pencil marker (✎,
// bright blue) sits in glamour's two-column left margin on the first content
// line, outside the content column, so every line stays aligned to it. Glamour's
// leading/trailing blank lines are stripped so the callout is tight. Empty when
// the bead has no remark.
func (m Model) remarkLines(id string, w int) []string {
	text, ok := m.remarkFor(id)
	if !ok || w < 4 {
		return nil
	}
	rendered := m.md.render(strings.TrimSpace(text), w)
	lines := strings.Split(rendered, "\n")
	// Trim the blank lines glamour brackets its output with, so the callout has
	// no empty gutter above or below.
	start, end := 0, len(lines)
	for start < end && strings.TrimSpace(ansi.Strip(lines[start])) == "" {
		start++
	}
	for end > start && strings.TrimSpace(ansi.Strip(lines[end-1])) == "" {
		end--
	}
	lines = lines[start:end]
	if len(lines) == 0 {
		return nil
	}
	// Drop the pencil into glamour's two-column left margin on the first line, so
	// the marker reads outside the content column and the block aligns under it.
	marker := styRemark.Render(remarkGlyph + " ")
	first := lines[0]
	if lead := leadingSpaces(ansi.Strip(first)); lead >= 2 {
		first = marker + ansi.TruncateLeft(first, 2, "")
	} else {
		first = marker + strings.TrimLeft(first, " ")
	}
	out := make([]string, 0, len(lines))
	out = append(out, first)
	out = append(out, lines[1:]...)
	return out
}

// leadingSpaces counts the leading spaces of a plain (ANSI-stripped) string.
func leadingSpaces(s string) int {
	return len(s) - len(strings.TrimLeft(s, " "))
}
