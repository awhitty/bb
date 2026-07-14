package ui

// view.go is the render layer: View composes the header/body/footer regions
// (sizes from the layout engine) and the per-view renderers draw INTO them.

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/awhitty/bb/internal/nlq"
	"github.com/awhitty/bb/internal/rollup"
)

func (m Model) View() string {
	if m.width == 0 || m.height == 0 {
		return "loading…"
	}
	scr := m.layoutScreen()
	bodyH := scr.Body.H

	// The full help overlay (?) replaces the body; the viewport already fits
	// bodyH, and fitBlock guarantees the frame never overflows the terminal.
	if m.showHelp {
		return lipgloss.JoinVertical(lipgloss.Left,
			m.viewHeader(),
			fitBlock(m.helpVP.View(), m.width, bodyH),
			m.viewFooter())
	}

	var body string
	// The overlays (shares browser, activity feed) draw on top of whatever nav
	// view (m.view) is beneath them.
	var main func(int) string
	switch m.view {
	case ViewKanban:
		main = m.viewBoard
	case ViewTree:
		main = m.viewTree
	case ViewSwim:
		main = m.viewSwim
	case ViewColumns:
		main = m.viewColumns
	default: // ViewList
		main = m.viewList
	}
	switch {
	case m.sharesBrowse:
		main = m.viewSharesBrowse
	case m.activityView:
		main = m.viewActivity
	}
	switch {
	case m.detail != nil:
		body = m.viewDetail(bodyH)
	case m.panelOpen:
		body = lipgloss.JoinHorizontal(lipgloss.Top,
			main(bodyH), " ", m.renderPanel(scr.Panel.W, bodyH))
	default:
		body = main(bodyH)
	}

	// The facet control (S) composes OVER the body: its small menu is spliced onto
	// the board's top-left, anchored under its header strip segment, so the board
	// stays visible and re-sorts live behind the open menu.
	if m.control.open {
		body = m.overlayControl(body)
	}

	// The layout guarantees Header(1)+Body(bodyH)+Footer(1) == height; fitBlock
	// clamps the body to exactly bodyH lines so the frame can never overflow.
	return lipgloss.JoinVertical(lipgloss.Left,
		m.viewHeader(),
		fitBlock(body, m.width, bodyH),
		m.viewFooter())
}

// columnWindow page-jumps horizontally the same way cards do vertically, so
// the focused column is always on screen (root mode can have dozens of
// columns). windowStart is idempotent for a given focus, so header and board
// may both call this within one frame.
func (m Model) columnWindow() (start, nVis int) {
	n := len(m.columns)
	boardW := m.boardWidth()
	if n == 0 || boardW <= 0 {
		return 0, 0
	}
	// Only render as many columns as fit at a READABLE width — never squeeze
	// five skinny columns into a laptop terminal; page horizontally instead.
	nVis = (boardW + 1) / (readableColW + 1)
	if nVis < 1 {
		nVis = 1
	}
	if nVis > n {
		nVis = n
	}
	start = windowStart(m.winStart, "\x00cols", n, nVis, m.colIdx)
	return start, nVis
}

// modeLabels are the clean header names for each grouping (no "(kanban)" — the
// default layout is a list).
var modeLabels = map[rollup.Mode]string{
	rollup.ModeStatus:   "status",
	rollup.ModeType:     "type",
	rollup.ModeRoot:     "ancestor",
	rollup.ModeBlockers: "blockers",
}

// facetLabels name the board/list grouping when a grouper/set_view facet override
// is active (boardFacet) — including label/priority, which have no legacy Mode.
// Derived from rollup.FacetBindings (the ONE canonical facet registry) so the
// header labels cannot drift from the grouper's facets; the empty "none" binding
// is skipped since every reader guards the unset facet before indexing here.
var facetLabels = buildFacetLabels()

func buildFacetLabels() map[rollup.Facet]string {
	labels := make(map[rollup.Facet]string, len(rollup.FacetBindings))
	for _, b := range rollup.FacetBindings {
		if b.Facet == "" {
			continue
		}
		labels[b.Facet] = b.Name
	}
	return labels
}

// modeSegment is the view-identity segment of the header strip — the "mode" in
// mode · grouper · lanes · sort · scope. It names the active nav view (list/board/
// tree, the relationship views, an overlay) plus its per-view context (the tree's
// relation + progress, a rooted phrase), but NOT the grouping facet — grouperSegment
// carries that.
func (m Model) modeSegment() string {
	if m.activityView {
		return "activity"
	}
	if m.sharesBrowse {
		return fmt.Sprintf("live sessions · %d channel(s)", len(m.shareSections))
	}
	switch m.view {
	case ViewKanban:
		return "board"
	case ViewList:
		return "list"
	case ViewSwim:
		label := "relationship board"
		if is := m.focusedIssue(); is != nil {
			label += " · " + m.shortID(m.swimRootID())
		}
		return label
	case ViewColumns:
		label := "columns · " + m.millerRelLabel()
		if is := m.focusedIssue(); is != nil {
			label += " · " + m.shortID(is.ID)
		}
		return label
	case ViewTree:
		rel := "hierarchy"
		if m.depsTree {
			rel = "deps"
		}
		arrow := "↓" // forward: roots over descendants
		if m.treeDir {
			arrow = "↑" // invert: descendants over ancestors / under what they block
		}
		pct := 0
		if n := len(m.treeRows); n > 0 {
			pct = (m.treeIdx + 1) * 100 / n
		}
		return fmt.Sprintf("tree (%s %s) — %d%% (%d/%d)", rel, arrow, pct, m.treeIdx+1, len(m.treeRows))
	}
	return ""
}

// grouperSegment is the grouping-facet segment of the header strip — the grouper
// in mode · grouper · lanes · sort · scope — named in the modeLabels/facetLabels
// vocabulary. The
// board is always grouped (by boardFacet, else its mode); the tree reads its
// segmentation facet ("none" when unsegmented), the swim its column-axis facet.
// It is empty for the views with no grouping axis (columns, activity, the shares
// browser), so no grouper segment is drawn there.
func (m Model) grouperSegment() string {
	if m.activityView || m.sharesBrowse {
		return ""
	}
	switch m.view {
	case ViewColumns:
		return ""
	case ViewTree:
		if m.treeFacet == "" {
			return "none"
		}
		return facetLabels[m.treeFacet]
	case ViewSwim:
		if m.swimFacet == "" {
			return "status"
		}
		return facetLabels[m.swimFacet]
	default: // list / kanban
		if m.boardFacet == "" {
			return modeLabels[m.mode]
		}
		return facetLabels[m.boardFacet]
	}
}

// laneSegment is the break-out-lane segment of the header strip — the lanes in
// mode · grouper · lanes · sort, named in the facetLabels vocabulary. It is drawn
// only on the kanban when a lane facet is set (the 1D board shows no lanes
// segment); the lane control opens under it.
func (m Model) laneSegment() string {
	if m.view != ViewKanban || m.boardLane == "" {
		return ""
	}
	return facetLabels[m.boardLane]
}

// grouperTail is the header strip up to (and including) the grouper segment. The
// lanes segment anchors after it, so laneSegmentX measures it.
func (m Model) grouperTail() string {
	s := styHeader.Render("bb") + styDim.Render(" · "+m.modeSegment())
	if g := m.grouperSegment(); g != "" {
		s += styDim.Render(" · " + g)
	}
	return s
}

// headerLead is the header strip up to the sort segment: "bb · <mode>",
// then the grouper segment, then the lanes segment when the kanban has a break-out
// lane. sortSegmentX measures it and viewHeader builds on it, so the grouper/
// lanes/sort anchors can never drift from what is drawn.
func (m Model) headerLead() string {
	s := m.grouperTail()
	if l := m.laneSegment(); l != "" {
		s += styDim.Render(" · lanes:" + l)
	}
	return s
}

// groupSegmentX is the column where the header's grouper segment (" · <facet>")
// begins — the anchor the grouper control opens under.
func (m Model) groupSegmentX() int {
	return lipgloss.Width(styHeader.Render("bb") + styDim.Render(" · "+m.modeSegment()))
}

// traverseSegmentX is the column where the header's mode segment (the tree's
// "tree (relation dir)" phrase) begins — the anchor the traverse control opens
// under, right after the "bb · " lead.
func (m Model) traverseSegmentX() int {
	return lipgloss.Width(styHeader.Render("bb") + styDim.Render(" · "))
}

// laneSegmentX is the column where the header's lanes segment (" · lanes:<facet>")
// begins — the anchor the lane control opens under. When no lane is set yet the
// segment is absent, so it anchors right where the segment would appear.
func (m Model) laneSegmentX() int {
	return lipgloss.Width(m.grouperTail())
}

// sortSegmentX is the column where the header's sort segment (" · key↑") begins
// — the anchor the sort control opens under.
func (m Model) sortSegmentX() int {
	return lipgloss.Width(m.headerLead())
}

// viewHeader renders the glanceable strip. The left side reads, in order,
// mode · grouper · lanes (kanban, when set) · sort · scope (when scoped), each a
// per-facet control's anchor; the right side is the issue count + resolve status.
// Left and right compose into the width budget with a gap between; when they can't
// both fit, the left is hard-truncated with an ellipsis and the right is dropped.
func (m Model) viewHeader() string {
	left := m.headerLead()
	// Active sort (S cycles it) — hidden only in the activity feed, which has
	// no sortable rows.
	if !m.activityView {
		s := m.activeSort()
		arrow := "↑"
		if s.Desc {
			arrow = "↓"
		}
		left += styDim.Render(" · " + string(s.Key) + arrow)
		if m.minSubtree > 0 && m.view == ViewTree {
			left += styDim.Render(fmt.Sprintf(" · ≥%d", m.minSubtree))
		}
	}
	// Relationship-focus scope: while a scope is active every mode is narrowed to
	// this bead's neighborhood, so the strip names the root the whole board pivots
	// inside. esc leaves it.
	if m.scopeRoot != "" {
		left += styDim.Render(" · scope:" + m.shortID(m.scopeRoot))
	}
	// The focused issue id lives here (short form) — reachable, uncluttered.
	if is := m.focusedIssue(); is != nil {
		left += styDim.Render(" · " + m.shortID(is.ID))
	}
	// A persistent badge for "attached to an agent channel" — so a channel's view
	// that legitimately matches zero reads as an attached view, not a broken board,
	// and the attach state is always visible. It names the SESSION being driven
	// (the conversation name Alex gave it), not the view title, so the human
	// always knows whose activity is on the board.
	if m.attach.active && !m.sharesBrowse {
		left += styDetailTitle.Render(" · ⦿ attached: " + m.sessionLabel(m.attach.session))
	}
	if start, nVis := m.columnWindow(); m.view == ViewKanban && !m.sharesBrowse && nVis > 0 && nVis < len(m.columns) {
		left += styDim.Render(fmt.Sprintf(" · cols %d-%d/%d", start+1, start+nVis, len(m.columns)))
	}
	if m.view == ViewSwim {
		if n := len(m.buildGrid(m.swimRootID(), m.gridFacet()).columns); n > 0 {
			if start, nVis := m.swimColumnWindow(n); nVis > 0 && nVis < n {
				left += styDim.Render(fmt.Sprintf(" · cols %d-%d/%d", start+1, start+nVis, n))
			}
		}
	}
	right := fmt.Sprintf("%d issues", len(m.issues))
	if m.showClosed {
		right += " (incl. closed)"
	}
	switch {
	case m.resolveSummary != "":
		right += " · " + m.resolveSummary
	case m.resolving:
		right += " · finding models…"
	case m.resolveErr != "":
		right += " · models off — bb status"
	}
	if m.loading || m.compiling || m.resolving {
		right = m.spin.View() + " " + right
	}
	right = styDim.Render(right)
	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		return ansi.Truncate(left, m.width, "…")
	}
	return left + strings.Repeat(" ", gap) + right
}

// reviewCount renders the pre-executed row count (and repair count) for the
// review line — a compile the user can trust because it already ran.
func reviewCount(r nlq.Result) string {
	var s string
	switch {
	case r.Count < 0:
		s = " · query error"
	case r.Count == 0:
		s = " · no matches"
	case r.Count == 1:
		s = " · 1 result"
	default:
		s = fmt.Sprintf(" · %d results", r.Count)
	}
	if n := r.Repairs(); n > 0 {
		s += fmt.Sprintf(" · repaired ×%d", n)
	}
	return s
}

func (m Model) viewFooter() string {
	if m.showHelp {
		return ansi.Truncate(styDim.Render("? or esc close · j/k scroll"), m.width, "…")
	}
	var line string
	switch m.prompt {
	case promptQuery, promptNLEdit, promptAnalyst, promptJump, promptComment:
		if m.compiling {
			line = styDim.Render(fmt.Sprintf("%s %.0fs · esc cancels", m.message, time.Since(m.compileT0).Seconds()))
		} else {
			line = m.input.View()
		}
	case promptNLReview:
		if m.compiling {
			line = styDim.Render(fmt.Sprintf("%s %.0fs · esc cancels", m.message, time.Since(m.compileT0).Seconds()))
		} else {
			meta := " · " + m.provider.Label + reviewCount(m.review)
			if m.compileElapsed > slowCompile {
				meta += fmt.Sprintf(" · slow (%.0fs)", m.compileElapsed.Seconds())
			}
			line = "compiled: " + styBold.Render(m.review.Query) +
				styDim.Render(meta+"   [enter/y run · e edit · r re-roll · n reject]")
		}
	default:
		switch {
		case m.message != "" && m.msgIsError:
			line = styError.Render(m.message)
		case m.message != "":
			line = styDim.Render(m.message)
		case m.detail != nil:
			line = m.help.View(m.detailKeys)
		default:
			line = m.help.View(m.keys)
		}
	}
	return ansi.Truncate(line, m.width, "…")
}

func (m Model) viewBoard(bodyH int) string {
	boardW := m.boardWidth()
	if len(m.columns) == 0 {
		empty := styDim.Render(m.emptyBoardText())
		return lipgloss.Place(boardW, bodyH, lipgloss.Center, lipgloss.Center, empty)
	}
	colStart, nVis := m.columnWindow()
	colW := (boardW - boardColGap*(nVis-1)) / nVis
	// Borderless: title(1) + reserved ↑/↓ indicator lines(2) + cards.
	cardWindow := max(1, bodyH-3)
	now := time.Now()
	showStatus := m.mode != rollup.ModeStatus

	// 2D board: the break-out lane control (L) is set, so the board renders as
	// GLOBAL horizontal bands — one lane order across every column, each lane a
	// single full-width band aligned across the columns (a columns×lanes cross-tab).
	if m.boardLane != "" && len(m.boardBands) == len(m.columns) {
		order := laneBandOrder(m.boardBands, m.boardLane, m.treeSort)
		// Locate the focused card in its column's lane cell: which lane (key) the
		// cursor sits in and its index there, so the global band can highlight it.
		focusBandKey, focusIdx := "", -1
		if m.colIdx >= 0 && m.colIdx < len(m.boardBands) {
			rowBase := 0
			for _, b := range m.boardBands[m.colIdx] {
				if m.rowIdx >= rowBase && m.rowIdx < rowBase+len(b.issues) {
					focusBandKey, focusIdx = b.key, m.rowIdx-rowBase
					break
				}
				rowBase += len(b.issues)
			}
		}
		return m.renderLaneBoard(m.columns, m.boardBands, order, colStart, nVis, bodyH, colW, now, showStatus, m.colIdx, focusBandKey, focusIdx)
	}

	gap := strings.Repeat(" ", boardColGap)
	cols := make([]string, 0, nVis*2-1)
	for i := colStart; i < colStart+nVis; i++ {
		col := m.columns[i]
		active := i == m.colIdx
		focusRow := -1
		if active {
			focusRow = m.rowIdx
		}
		start := windowStart(m.winStart, col.Key, len(col.Issues), cardWindow, focusRow)
		cols = append(cols, renderColumn(col, active, focusRow, start, cardWindow, colW, m.ageCellFn(now), showStatus, m.relFlagsFor, m.issueEmph))
		if i < colStart+nVis-1 {
			cols = append(cols, gap)
		}
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, cols...)
}

func (m Model) viewDetail(bodyH int) string {
	innerW := max(1, m.width-4)
	inner := []string{
		detailHeaderLine(m.shortID(m.detail.ID), m.detail.Title),
		tabStrip(m.detailTab, innerW),
		"", // breathing room between the tabs and the content
		m.detailVP.View(),
	}
	return styDetailBox.Width(m.width - 2).Render(strings.Join(inner, "\n"))
}

// renderHelpContent builds the full help overlay body from the keybinding
// registry (keys.go): one titled block per category, packed into rows of
// columns that fit the width. It is auto-generated, so it can never drift from
// dispatch or the footer. The caller wraps it in a viewport, so a grid taller
// than the body scrolls rather than overflowing.
func (m Model) renderHelpContent(width int) string {
	cats := m.keys.categories()
	blocks := make([]string, 0, len(cats))
	for _, c := range cats {
		// bubbles/help renders the aligned "keys  desc" pairs for the category;
		// the header is ours (the help package has no notion of group titles).
		body := m.help.FullHelpView([][]key.Binding{c.bindings})
		header := styBold.Render(c.name)
		blocks = append(blocks, lipgloss.JoinVertical(lipgloss.Left, header, body))
	}

	// Pack blocks left-to-right, wrapping to a new row of columns when the next
	// block would exceed the width — a responsive cheatsheet grid.
	const gap = "    "
	var rows []string
	var cur []string
	curW := 0
	flush := func() {
		if len(cur) > 0 {
			rows = append(rows, lipgloss.JoinHorizontal(lipgloss.Top, cur...))
			cur, curW = nil, 0
		}
	}
	for _, blk := range blocks {
		w := lipgloss.Width(blk)
		sep := 0
		if len(cur) > 0 {
			sep = len(gap)
		}
		if len(cur) > 0 && curW+sep+w > width {
			flush()
			sep = 0
		}
		if sep > 0 {
			cur = append(cur, gap)
			curW += sep
		}
		cur = append(cur, blk)
		curW += w
	}
	flush()

	parts := []string{styHeader.Render("Keybindings"), ""}
	for i, r := range rows {
		if i > 0 {
			parts = append(parts, "")
		}
		parts = append(parts, r)
	}
	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

// fitBlock hard-fits a block to width×height: every line truncated (never
// wrapped by the terminal), the line count padded/clipped to exactly h.
// View() must always fit the terminal — bubbletea won't save us from an
// overgrown frame.
func fitBlock(s string, w, h int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > h {
		lines = lines[:h]
	}
	for i, l := range lines {
		if ansi.StringWidth(l) > w {
			lines[i] = ansi.Truncate(l, w, "")
		}
	}
	for len(lines) < h {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}
