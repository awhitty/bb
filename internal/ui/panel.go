package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/awhitty/bb/internal/bd"
	"github.com/awhitty/bb/internal/ui/layout"
)

// The preview panel (space) live-previews the highlighted card and lists its
// traversable links — parent, children, depends-on, blocked-by. With tab the
// panel takes focus and enter jumps the board to a link: that is how you
// travel the graph bead-to-bead.

type linkKind string

const (
	linkParent linkKind = "parent"
	linkChild  linkKind = "child"
	linkNeeds  linkKind = "needs"  // depends-on (this card's blockers)
	linkBlocks linkKind = "blocks" // blocked-by (cards depending on this one)
)

type panelLink struct {
	kind linkKind
	id   string
}

// panelMDCache memoizes the glamour-rendered body of the previewed card.
type panelMDCache struct {
	id    string
	width int
	body  string
}

// relatedLinks derives the RELATED section from the loaded issue set only —
// children and blocked-by come from in-memory reverse indexes, never an
// extra bd call.
func (m *Model) relatedLinks(is bd.Issue) []panelLink {
	var links []panelLink
	if _, ok := m.byID[is.Parent]; ok {
		links = append(links, panelLink{linkParent, is.Parent})
	}
	for _, id := range m.childrenOf[is.ID] {
		links = append(links, panelLink{linkChild, id})
	}
	// bd.BlockerIDs handles BOTH dependency shapes — edge (bd list) and object
	// (bd show) — so the detail view's Related tab is complete too.
	for _, id := range bd.BlockerIDs(is) {
		if _, ok := m.byID[id]; ok {
			links = append(links, panelLink{linkNeeds, id})
		}
	}
	for _, id := range m.revDeps[is.ID] {
		links = append(links, panelLink{linkBlocks, id})
	}
	return links
}

// commonIDPrefix returns the "<prefix>-" shared by EVERY id on the board (e.g.
// "demo-"), or "" if ids are mixed or unprefixed. Constant across the board,
// it is 100% redundant on screen — stripped from every id DISPLAY (the full id
// stays internal for jump-to-id, MCP, and bd calls).
func commonIDPrefix(issues []bd.Issue) string {
	if len(issues) == 0 {
		return ""
	}
	dash := strings.IndexByte(issues[0].ID, '-')
	if dash < 0 {
		return ""
	}
	p := issues[0].ID[:dash+1]
	for _, is := range issues {
		if !strings.HasPrefix(is.ID, p) {
			return "" // mixed prefixes → don't strip anything
		}
	}
	return p
}

// shortID strips the board's common prefix for display.
func (m Model) shortID(id string) string {
	if m.idPrefix != "" && strings.HasPrefix(id, m.idPrefix) {
		return id[len(m.idPrefix):]
	}
	return id
}

// rebuildIndexes refreshes the in-memory relationship indexes after a load.
// It builds from m.graph (the WHOLE board), not the filtered display set, so a
// bead's parent/children/blockers always resolve even when a filter (parent=X)
// never loaded them. The id prefix still comes from the displayed set.
func (m *Model) rebuildIndexes() {
	graph := m.graph
	if graph == nil {
		graph = m.issues // pre-first-load / defensive fallback
	}
	m.idPrefix = commonIDPrefix(m.issues)
	m.byID = make(map[string]bd.Issue, len(graph))
	m.childrenOf = map[string][]string{}
	m.revDeps = map[string][]string{}
	for _, is := range graph {
		m.byID[is.ID] = is
	}
	for _, is := range graph {
		if _, ok := m.byID[is.Parent]; ok {
			m.childrenOf[is.Parent] = append(m.childrenOf[is.Parent], is.ID)
		}
		for _, d := range is.Dependencies {
			if d.Type == "parent-child" || d.DependsOnID == "" {
				continue
			}
			if _, ok := m.byID[d.DependsOnID]; ok {
				m.revDeps[d.DependsOnID] = append(m.revDeps[d.DependsOnID], is.ID)
			}
		}
	}
}

// layoutScreen computes the frame geometry ONCE from package layout — the
// single source of truth for every region's size, so no view re-derives the
// height/width budget (which is how frame overflow used to creep back in).
func (m Model) layoutScreen() layout.Screen {
	panelW := 0
	if m.panelOpen {
		panelW = m.width / 2 // ~half the terminal, fixed while open — room for real row detail
		if panelW < 30 {
			panelW = 30
		}
	}
	return layout.Compute(m.width, m.height, panelW, minColWidth)
}

// panelWidth is the side panel's width (0 when closed); boardWidth is what
// remains for the main view. Both read the one computed layout.
func (m Model) panelWidth() int { return m.layoutScreen().Panel.W }
func (m Model) boardWidth() int { return m.layoutScreen().Main.W }

func linkRow(kind linkKind, is bd.Issue, selected bool, w int) string {
	tag := fmt.Sprintf("%-6s ", kind)
	body := fmt.Sprintf("%s %s", is.ID, is.Title)
	line := pad(ansi.Truncate(tag+body, w, "…"), w)
	if selected {
		return styPlainF.Render(line)
	}
	return styDim.Render(ansi.Truncate(tag, w, "")) + ansi.Truncate(body, max(0, w-ansi.StringWidth(tag)), "…")
}

// renderPanel draws the preview box at exactly w×h terminal cells.
func (m Model) renderPanel(w, h int) string {
	innerW, innerH := w-4, h-2
	if innerW < 8 {
		innerW = 8
	}
	var lines []string
	switch {
	case m.analystActive || m.analystText != "":
		lines = m.analystLines(innerW, innerH)
	default:
		if is := m.previewIssue(); is == nil {
			lines = []string{styDim.Render("nothing highlighted")}
		} else {
			lines = m.panelLines(*is, innerW, innerH)
		}
	}
	for len(lines) < innerH {
		lines = append(lines, "")
	}
	lines = lines[:innerH]
	box := styColInactive
	if m.panelFocus {
		box = styColActive
	}
	return box.Width(w - 2).Render(strings.Join(lines, "\n"))
}

func (m Model) panelLines(is bd.Issue, w, h int) []string {
	var lines []string
	// Clean fixed header: title (bold), then one meta line (short id · status ·
	// priority · type · labels).
	lines = append(lines, styBold.Render(ansi.Truncate(is.Title, w, "…")))
	lines = append(lines, ansi.Truncate(styDetailTitle.Render(m.shortID(is.ID))+"  "+metaLine(is), w, ""))

	// The active share's remark (why this bead was selected) sits right under the
	// meta line — near the top, wrapped like the body. Absent when no remark.
	if rl := m.remarkLines(is.ID, w); len(rl) > 0 {
		lines = append(lines, "")
		lines = append(lines, rl...)
	}

	// Description gets the PRIME real estate (top, full width, glamour-wrapped);
	// enter opens the full scrollable detail for the rest.
	lines = append(lines, "")
	for _, l := range strings.Split(m.panelBody(is, w), "\n") {
		lines = append(lines, ansi.Truncate(l, w, ""))
	}

	// Related (the octopus) sits at the BOTTOM — useless up top at narrow width;
	// selectable when the panel is focused. Shown only if there's room left.
	sel := -1
	if m.panelFocus {
		sel = m.panelLinkIdx
	}
	if nb, _ := m.octopusBody(is.ID, w, sel); strings.TrimSpace(ansi.Strip(nb)) != "" {
		lines = append(lines, "", styDim.Render("relationships"))
		for _, l := range strings.Split(nb, "\n") {
			lines = append(lines, ansi.Truncate(l, w, ""))
		}
	}
	if len(lines) > h {
		lines = lines[:h]
	}
	return lines
}

// panelBody renders (and caches) the markdown body for one issue+width —
// held-j must not re-run glamour for a card it already rendered.
func (m Model) panelBody(is bd.Issue, w int) string {
	if m.panelMD.id == is.ID && m.panelMD.width == w {
		return m.panelMD.body
	}
	var parts []string
	desc := strings.TrimSpace(is.Description)
	if desc == "" {
		parts = append(parts, styDim.Render("(no description)"))
	} else {
		parts = append(parts, m.md.render(desc, w))
	}
	if notes := strings.TrimSpace(is.Notes); notes != "" {
		parts = append(parts, styBold.Render(styDim.Render("notes")), m.md.render(notes, w))
	}
	body := strings.Join(parts, "\n")
	// panelMD is a pointer field: the cache survives the value-receiver View.
	m.panelMD.id, m.panelMD.width, m.panelMD.body = is.ID, w, body
	return body
}

// analystLines renders the streamed analyst answer into the panel surface,
// tail-following while tokens arrive.
func (m Model) analystLines(w, h int) []string {
	head := "analyst · " + m.analyst.Label
	if m.analystActive {
		head += fmt.Sprintf(" · %.0fs · %d tok", time.Since(m.analystT0).Seconds(), m.analystChunks)
	}
	lines := []string{
		styBold.Render(ansi.Truncate(head, w, "…")),
		styDim.Render(ansi.Truncate("Q: "+m.analystQ, w, "…")),
		"",
	}
	text := m.analystText
	if text == "" {
		lines = append(lines, styDim.Render("thinking…"))
		return lines
	}
	wrapped := lipgloss.NewStyle().Width(w).Render(text)
	body := strings.Split(wrapped, "\n")
	room := h - len(lines)
	if len(body) > room {
		body = body[len(body)-room:] // tail-follow: the ids block lands last
	}
	return append(lines, body...)
}
