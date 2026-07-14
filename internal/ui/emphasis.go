package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/awhitty/bb/internal/agentapi"
)

// emphasis.go is the render-time decoration layer. It is deliberately SHALLOW:
// the model holds one ephemeral []agentapi.Emphasis; the row formatter and the
// section renderers do a lookup per target and decorate in place. Nothing is
// removed or reordered — emphasis is orthogonal to filter/sort. esc clears it.

// rowEmph is the resolved gutter decoration for one issue row: an emphasis marker
// glyph + its color, whether spotlight is muting this row (it is NOT a target),
// and whether the active share carries a remark for this bead (remark.go). Focus
// and emphasis take the gutter slot first; the remark indicator fills it only when
// neither does (gutterEmph), so a reviewer can still scan which rows carry remarks.
type rowEmph struct {
	marker string // "" = none
	color  lipgloss.Color
	muted  bool // spotlight active and this row is not spotlighted
	remark bool // the active share carries a remark for this bead
}

// emphasis marker glyphs + colors per style.
var (
	styEmphHi     = lipgloss.Color("11") // bright yellow tint accent for highlight
	styEmphMarker = lipgloss.Color("13") // magenta star
	styEmphOut    = lipgloss.Color("14") // cyan for outline/spotlight targets
)

// allEmphasis is the emphasis decoration layer. An emphasis share applies its
// decorations into m.emphasis when applied (live on the attached channel), so
// there is one source of truth here — no separate ambient layer to merge.
func (m Model) allEmphasis() []agentapi.Emphasis {
	return m.emphasis
}

// spotlightActive reports whether any emphasis uses the spotlight style (which
// dims everything that is NOT a target).
func (m Model) spotlightActive() bool {
	for _, e := range m.allEmphasis() {
		if e.Style == "spotlight" {
			return true
		}
	}
	return false
}

// issueEmph resolves the row decoration for an issue id. Spotlight mutes every
// row that is not itself a target; a per-issue style wins for its own row.
func (m Model) issueEmph(id string) rowEmph {
	spot := m.spotlightActive()
	var re rowEmph
	re.remark = m.hasRemark(id) // orthogonal to emphasis; the gutter shows it when free
	targeted := false
	for _, e := range m.allEmphasis() {
		if e.Kind != "issue" || e.Ref != id {
			continue
		}
		targeted = true
		switch e.Style {
		case "marker":
			re.marker, re.color = "★", styEmphMarker
		case "outline":
			re.marker, re.color = "▏", styEmphOut
		case "spotlight":
			re.marker, re.color = "►", styEmphOut
		default: // highlight
			re.marker, re.color = "●", styEmphHi
		}
	}
	if spot && !targeted {
		re.muted = true
	}
	return re
}

// gutterEmph renders the 2-col gutter honoring focus first, then emphasis, then a
// remark indicator. A focused row keeps the accent bar; an emphasized unfocused
// row shows its marker glyph; a remarked row (with neither) shows the pencil, so a
// reviewer can scan which beads carry a remark. The three never fight for the same
// cell — the focused/emphasized row's remark still reads in the panel callout.
func gutterEmph(focused bool, re rowEmph) string {
	if focused {
		return styFocusBar.Render("▌") + " "
	}
	if re.marker != "" {
		return lipgloss.NewStyle().Foreground(re.color).Bold(true).Render(re.marker) + " "
	}
	if re.remark {
		return styRemark.Render(remarkGlyph) + " "
	}
	return "  "
}

// --- emphasis mutations (esc / MCP) ---

func (m *Model) setEmphasis(targets []agentapi.Emphasis) {
	m.emphasis = targets
	if len(targets) > 0 {
		m.pushLayer(layerEmphasis) // esc peels the decoration (layers.go)
	}
}

func (m *Model) clearEmphasis() bool {
	if len(m.emphasis) == 0 {
		return false
	}
	m.emphasis = nil
	return true
}

// sectionEmph returns the emphasis (if any) targeting a named detail/panel
// section (description|notes|related|overview|history|comments).
func (m Model) sectionEmph(name string) (agentapi.Emphasis, bool) {
	for _, e := range m.emphasis {
		if e.Kind == "section" && e.Ref == name {
			return e, true
		}
	}
	return agentapi.Emphasis{}, false
}

// decorateSection wraps a rendered section body per its emphasis style:
// outline draws a border box, highlight/marker prepend a colored callout bar.
// A label appends as a dim callout. Width w clamps the box.
func (m Model) decorateSection(name, body string, w int) string {
	e, ok := m.sectionEmph(name)
	if !ok {
		return body
	}
	call := ""
	if e.Label != "" {
		call = "\n" + lipgloss.NewStyle().Foreground(styEmphOut).Render("‹ "+e.Label+" ›")
	}
	switch e.Style {
	case "outline":
		box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).
			BorderForeground(styEmphOut).Padding(0, 1).Width(max(4, w-2))
		return box.Render(body) + call
	case "marker", "highlight", "spotlight":
		bar := lipgloss.NewStyle().Foreground(styEmphOut).Bold(true).Render("▍ ")
		var out strings.Builder
		for i, l := range strings.Split(body, "\n") {
			if i > 0 {
				out.WriteByte('\n')
			}
			out.WriteString(bar + l)
		}
		return out.String() + call
	}
	return body + call
}
