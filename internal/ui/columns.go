package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/awhitty/bb/internal/bd"
	"github.com/awhitty/bb/internal/rollup"
)

// The Miller-columns relationship navigator (key C): a Finder-style horizontal
// stack of columns. Column 0 lists the top-level beads (roots/epics). Selecting
// a bead APPENDS a column to its right showing that bead's children — the
// hierarchy drill; d flips the appended relation to the dependency neighborhood
// (dependents then blockers). Drilling APPENDS a column, it never re-roots: the
// path columns to the left stay exactly where they are, no layout reshuffle —
// which is the whole point (it fixes the standalone swimlane's re-rooting jump).
//
// Only the last few columns that fit at a readable width are shown, plus a
// detail-preview pane pinned to the right for the selected bead; older path
// columns scroll off the left behind a breadcrumb. Each column is a navlist
// instance (the one page-jump windowed renderer), so held-key scroll stays
// smooth. It reuses the relationship indexes (childrenOf / revDeps / blockers)
// and the preview's own building blocks — it depends on nothing in octopus.go.

// millerColW is the readable-min width of one column; the fit math never
// squeezes below it, paging older columns off the left instead.
const millerColW = 26

// millerLayout is the derived, validated column stack for one render: the
// resolved columns (col 0 = roots, col i = the drilled relation of path[i-1]),
// the validated drill path, the focused (rightmost) column index, and the
// selected row within it (clamped). Pure — it never mutates the model.
type millerLayout struct {
	cols     [][]bd.Issue
	path     []string
	focusCol int
	sel      int
}

// millerRelLabel names the active drill relation (d toggles it).
func (m Model) millerRelLabel() string {
	if m.millerDeps {
		return "deps"
	}
	return "children"
}

// millerRoots is column 0: the top-level beads (no in-board parent), in the
// tree view's root order. Built from the whole graph, like the swimlane, so the
// neighborhood is the true graph regardless of the board's display filter — but
// while a relationship-focus scope is active it builds from the scoped set, so the
// navigator (like every other mode) pivots inside the neighborhood.
func (m Model) millerRoots() []bd.Issue {
	src := m.graph
	if m.scopeRoot != "" {
		src = m.visibleIssues() // the scoped neighborhood
	}
	roots := rollup.BuildTree(src)
	out := make([]bd.Issue, 0, len(roots))
	for _, r := range roots {
		out = append(out, r.Issue)
	}
	return out
}

// millerChildIDs is the relation a column drills into: hierarchy children, or
// (with d) the dependency neighborhood — what this bead blocks (its dependents)
// then what blocks it (its blockers), deduped.
func (m Model) millerChildIDs(id string) []string {
	if !m.millerDeps {
		return m.childrenOf[id]
	}
	var out []string
	seen := map[string]bool{}
	add := func(ids []string) {
		for _, x := range ids {
			if x == "" || seen[x] {
				continue
			}
			if _, ok := m.byID[x]; ok {
				seen[x] = true
				out = append(out, x)
			}
		}
	}
	add(m.revDeps[id])
	add(bd.BlockerIDs(m.byID[id]))
	return out
}

// millerResolve maps ids to their issues (dropping any off the loaded board).
func (m Model) millerResolve(ids []string) []bd.Issue {
	out := make([]bd.Issue, 0, len(ids))
	for _, id := range ids {
		if is, ok := m.byID[id]; ok {
			out = append(out, is)
		}
	}
	return out
}

// millerBuild derives the (validated) column stack from millerPath. A path
// element that no longer appears in its column — e.g. after d flips the
// relation — truncates the path there, so the view never shows a stale drill.
func (m Model) millerBuild() millerLayout {
	var lay millerLayout
	prev := m.millerRoots()
	lay.cols = append(lay.cols, prev)
	for _, pid := range m.millerPath {
		found := false
		for _, is := range prev {
			if is.ID == pid {
				found = true
				break
			}
		}
		if !found {
			break // stale drill (relation changed): stop the path here
		}
		kids := m.millerResolve(m.millerChildIDs(pid))
		lay.cols = append(lay.cols, kids)
		lay.path = append(lay.path, pid)
		prev = kids
	}
	lay.focusCol = len(lay.path)
	focus := lay.cols[lay.focusCol]
	lay.sel = clamp(m.millerSel, 0, max(0, len(focus)-1))
	return lay
}

// millerSelectedID is the bead the preview shows: the focused column's selected
// card, or (a leaf drilled into with no children) the leaf itself.
func (m Model) millerSelectedID() string {
	lay := m.millerBuild()
	focus := lay.cols[lay.focusCol]
	if len(focus) > 0 {
		return focus[lay.sel].ID
	}
	if n := len(lay.path); n > 0 {
		return lay.path[n-1]
	}
	return ""
}

// --- entering / navigating ---

// enterColumns opens the navigator with the drill relation set by deps (false =
// hierarchy children, true = the dependency neighborhood — the traverse the tree
// and the set_view share). A root id (from MCP set_view) pre-drills the path to
// that bead's hierarchy ancestors and selects it in the focused column; empty
// starts at the roots column. The per-column drill stays childrenOf(selectedNode)
// — deps only re-keys which edge that childrenOf follows (millerChildIDs).
func (m *Model) enterColumns(root string, deps bool) {
	m.activityView = false // drop the overlay so the navigator is visible
	m.setView(ViewColumns)
	m.millerDeps = deps
	m.millerPath = nil
	m.millerSel = 0
	if root == "" {
		return
	}
	is, ok := m.byID[root]
	if !ok {
		return
	}
	// Ancestor chain (top-level → root's parent) becomes the drill path; the
	// root itself is selected in the newly focused column.
	var chain []string
	for cur := is.Parent; cur != ""; {
		p, ok := m.byID[cur]
		if !ok {
			break
		}
		chain = append([]string{cur}, chain...)
		cur = p.Parent
	}
	m.millerPath = chain
	lay := m.millerBuild()
	focus := lay.cols[lay.focusCol]
	for i, x := range focus {
		if x.ID == root {
			m.millerSel = i
			break
		}
	}
}

// millerMove steps the selection within the focused column.
func (m *Model) millerMove(d int) {
	lay := m.millerBuild()
	m.millerPath = lay.path // adopt any truncation
	focus := lay.cols[lay.focusCol]
	m.millerSel = clamp(lay.sel+d, 0, max(0, len(focus)-1))
}

func (m *Model) millerTop() { m.millerSel = 0 }
func (m *Model) millerBottom() {
	lay := m.millerBuild()
	m.millerPath = lay.path
	m.millerSel = max(0, len(lay.cols[lay.focusCol])-1)
}

// millerDrill drills into the focused column's selected bead: it APPENDS a
// column and focus moves right. A leaf (no children in the active relation)
// can't be drilled — the path is unchanged.
func (m *Model) millerDrill() {
	lay := m.millerBuild()
	focus := lay.cols[lay.focusCol]
	if lay.sel >= len(focus) {
		return
	}
	id := focus[lay.sel].ID
	if len(m.millerChildIDs(id)) == 0 {
		return // leaf — nothing to drill into
	}
	m.millerPath = append(lay.path, id)
	m.millerSel = 0
}

// millerBack pops one level: the focused column collapses and focus returns to
// the left column, re-selecting the bead you had drilled into.
func (m *Model) millerBack() {
	lay := m.millerBuild()
	if len(lay.path) == 0 {
		return
	}
	popped := lay.path[len(lay.path)-1]
	m.millerPath = lay.path[:len(lay.path)-1]
	// Re-select the popped bead in the now-focused column.
	back := m.millerBuild()
	focus := back.cols[back.focusCol]
	m.millerSel = 0
	for i, x := range focus {
		if x.ID == popped {
			m.millerSel = i
			break
		}
	}
}

// --- rendering ---

func (m Model) viewColumns(bodyH int) string {
	w := m.boardWidth()
	lay := m.millerBuild()

	if len(lay.cols[0]) == 0 {
		return lipgloss.Place(w, bodyH, lipgloss.Center, lipgloss.Center,
			styDim.Render("no top-level beads — r refresh · c include closed"))
	}

	// Reserve a readable preview pane on the right; the rest holds columns.
	previewW := clamp(w/3, 34, 52)
	if maxPrev := w - millerColW - boardColGap; previewW > maxPrev {
		previewW = maxPrev
	}
	if previewW < 20 {
		previewW = 0 // too narrow for a preview — give it all to columns
	}
	colsW := w
	if previewW > 0 {
		colsW = w - previewW - boardColGap
	}

	// How many columns fit at the readable min; show the deepest (incl. focus).
	nFit := (colsW + boardColGap) / (millerColW + boardColGap)
	if nFit < 1 {
		nFit = 1
	}
	total := len(lay.cols)
	startCol := 0
	if total > nFit {
		startCol = total - nFit
	}
	visN := total - startCol
	colW := (colsW - boardColGap*(visN-1)) / visN
	if colW < millerColW {
		colW = millerColW
	}

	gap := strings.Repeat(" ", boardColGap)
	parts := make([]string, 0, visN*2+1)
	for ci := startCol; ci < total; ci++ {
		active := ci == lay.focusCol
		sel := -1
		if ci == lay.focusCol {
			sel = lay.sel
		} else if ci < len(lay.path) {
			for i, is := range lay.cols[ci] {
				if is.ID == lay.path[ci] {
					sel = i
					break
				}
			}
		}
		hiddenLeft := 0
		if ci == startCol {
			hiddenLeft = startCol // breadcrumb marker on the leftmost visible column
		}
		parts = append(parts, m.millerColumn(lay, ci, active, sel, hiddenLeft, colW, bodyH))
		if ci < total-1 {
			parts = append(parts, gap)
		}
	}
	if previewW > 0 {
		parts = append(parts, gap, m.millerPreview(lay, previewW, bodyH))
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, parts...)
}

// millerColumn renders one column: a header (the drilled-from context, or the
// roots label) over a page-jump windowed card list (navlist). The focused
// column's header is accented; hiddenLeft > 0 puts a "‹ N" breadcrumb marker on
// the leftmost visible column.
func (m Model) millerColumn(lay millerLayout, ci int, active bool, sel, hiddenLeft, colW, bodyH int) string {
	cards := lay.cols[ci]

	// Header line.
	var head string
	switch {
	case ci == 0:
		head = fmt.Sprintf("top-level (%d)", len(cards))
	default:
		rel := m.millerRelLabel()
		head = fmt.Sprintf("%s of %s (%d)", rel, m.shortID(lay.path[ci-1]), len(cards))
	}
	hStyle := styColTitleOff
	if active {
		hStyle = styColTitleOn.Underline(true)
	}
	header := hStyle.Render(ansi.Truncate(head, colW, "…"))
	if hiddenLeft > 0 {
		header = styDim.Render(fmt.Sprintf("‹%d ", hiddenLeft)) + hStyle.Render(ansi.Truncate(head, max(1, colW-4), "…"))
	}
	header = pad(ansi.Truncate(header, colW, "…"), colW)

	if len(cards) == 0 {
		lines := []string{header, styDim.Render("(leaf — no " + m.millerRelLabel() + ")")}
		for len(lines) < bodyH {
			lines = append(lines, "")
		}
		for i := range lines {
			lines[i] = pad(ansi.Truncate(lines[i], colW, "…"), colW)
		}
		return strings.Join(lines[:bodyH], "\n")
	}

	body := m.renderNavList(bodyH-1, len(cards), sel, fmt.Sprintf("\x00miller%d", ci),
		func(i int, _ bool) string {
			return m.millerCard(cards[i], colW, i == sel, active)
		}, moreTop, moreBottom)
	return header + "\n" + body
}

// millerCard renders one compact card exactly colW wide: a focus/selection
// gutter, short id, priority, blocked/blocking arrow, truncated title, and a ›
// chevron when the bead is drillable (has children in the active relation).
func (m Model) millerCard(is bd.Issue, colW int, selected, active bool) string {
	// Gutter: an accent bar for the active column's selection, a faint bar for a
	// left (path) column's selection, blank otherwise — so the drill path reads.
	gut := "  "
	switch {
	case selected && active:
		gut = styFocusBar.Render("▌") + " "
	case selected:
		gut = styDim.Render("▏") + " "
	}
	chevron := ""
	if len(m.millerChildIDs(is.ID)) > 0 {
		chevron = styDim.Render("›")
	}
	id := styDim.Render(padRight(m.shortID(is.ID), swimIDCellW))
	pri := priorityCell(is.Priority)
	arrow := relIndicator(m.relFlagsFor(is))
	chevW := ansi.StringWidth(ansi.Strip(chevron))
	fixed := focusGutter + swimIDCellW + 1 + 2 + 1 + relSlot + 1 + chevW
	titleW := colW - fixed
	if titleW < 3 {
		return pad(ansi.Truncate(gut+id+" "+pri, colW, "…"), colW)
	}
	line := gut + id + " " + pri + " " + arrow + " " + titleCell(is.Title, titleW, selected && active) + chevron
	return pad(ansi.Truncate(line, colW, "…"), colW)
}

// millerPreview is the right-hand detail pane for the selected bead: full title,
// meta, the glamour description (the preview's own cached body), and a compact
// relationship list — the preview's building blocks, none from octopus.go.
func (m Model) millerPreview(lay millerLayout, w, bodyH int) string {
	innerW, innerH := w-4, bodyH-2
	if innerW < 8 {
		innerW = 8
	}
	var lines []string
	id := m.millerSelectedID()
	is, ok := m.byID[id]
	if !ok {
		lines = []string{styDim.Render("nothing selected")}
	} else {
		lines = append(lines, styBold.Render(ansi.Truncate(is.Title, innerW, "…")))
		lines = append(lines, ansi.Truncate(styDetailTitle.Render(m.shortID(is.ID))+"  "+metaLine(is), innerW, ""))
		lines = append(lines, "")
		for _, l := range strings.Split(m.panelBody(is, innerW), "\n") {
			lines = append(lines, ansi.Truncate(l, innerW, ""))
		}
		if links := m.relatedLinks(is); len(links) > 0 {
			lines = append(lines, "", styDim.Render("relationships"))
			for _, l := range links {
				li := m.byID[l.id]
				row := styDim.Render(padRight(relLabel(l.kind), 11)) + m.shortID(l.id) + " " + li.Title
				lines = append(lines, ansi.Truncate(row, innerW, "…"))
			}
		}
	}
	for len(lines) < innerH {
		lines = append(lines, "")
	}
	lines = lines[:innerH]
	return styColInactive.Width(w - 2).Render(strings.Join(lines, "\n"))
}
