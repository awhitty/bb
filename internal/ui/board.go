package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/awhitty/bb/internal/bd"
	"github.com/awhitty/bb/internal/rollup"
	"github.com/awhitty/bb/internal/ui/layout"
)

// minColWidth gates panel sizing; readableColW is the floor for the opt-in
// multi-column board layout — never squeeze titles below this.
const (
	minColWidth  = 14
	readableColW = 32
)

// windowStart is the stateful wrapper around the pure page-jump computation in
// package layout: it remembers each list's offset (keyed) so the window stays
// FIXED while focus moves inside it and leaps only at an edge — the load-
// bearing perf behavior (held-key scroll diffs two lines, not the whole list).
func windowStart(starts map[string]int, key string, total, cardWindow, focusRow int) int {
	s := layout.Window(total, cardWindow, focusRow, starts[key])
	starts[key] = s
	return s
}

func pad(s string, w int) string {
	if d := w - ansi.StringWidth(s); d > 0 {
		return s + strings.Repeat(" ", d)
	}
	return s
}

// boardColGap is the whitespace separator between columns — no drawn borders
// (a bold/colored header + aligned fixed-width columns + this gap delineate
// them, and the border+padding space is reclaimed for card content).
const boardColGap = 2

// laneBand is one horizontal lane within a 2D board column: the lane facet's key
// (the stable identity used to line the same lane up across columns), its title
// (what the band header shows), and the cards that fall in it. The kanban
// break-out lane control (L) turns the board into global horizontal bands, one
// per lane key, aligned across every column — a columns×lanes cross-tab.
type laneBand struct {
	key    string
	title  string
	issues []bd.Issue
}

// laneBands splits one column's issues into lane bands by the lane facet, reusing
// the same two-level Group/SortGrouped the board itself runs: the lane facet fixes
// the band order (status/blockers by their fixed lists, others by count desc) and
// the item sort orders cards within a band. A multi-valued label fans a card into
// every matching band (Group's fan-out), so the lane axis matches the flat label
// board's behavior.
func laneBands(issues []bd.Issue, lane rollup.Facet, section rollup.SectionSort, item rollup.ItemSort) []laneBand {
	cols := rollup.SortGrouped(rollup.Group(issues, lane), section, item)
	bands := make([]laneBand, len(cols))
	for i, c := range cols {
		bands[i] = laneBand{key: c.Key, title: c.Title, issues: c.Issues}
	}
	return bands
}

// flattenBands concatenates the bands' issues in band order, so the board cursor
// (rowIdx over columns[i].Issues) walks the column top-to-bottom through the lane
// bands exactly as they render.
func flattenBands(bands []laneBand) []bd.Issue {
	var out []bd.Issue
	for _, b := range bands {
		out = append(out, b.issues...)
	}
	return out
}

// globalBand is one lane rendered as a full-board-width horizontal band: the lane
// key (its identity, shared across columns), the header title, and the row budget
// its card region gets. Its cells live per column (bandCells), keyed by column
// position, so reading across a band shows how that lane plays out across columns.
type globalBand struct {
	key     string
	title   string
	regionH int
}

// laneBandOrder computes the GLOBAL lane order for the 2D board: it aggregates
// every column's bands by lane key and runs the same section sort the engine uses
// (status/blockers by their fixed lists, other facets by count desc), so one lane
// order governs the whole board rather than each column ordering independently.
// Returns the ordered lane keys with their header titles.
func laneBandOrder(boardBands [][]laneBand, lane rollup.Facet, tree rollup.Sort) []gridCol {
	agg := map[string]*rollup.Column{}
	var order []string
	for _, bands := range boardBands {
		for _, b := range bands {
			c, ok := agg[b.key]
			if !ok {
				c = &rollup.Column{Key: b.key, Title: b.title}
				agg[b.key] = c
				order = append(order, b.key)
			}
			c.Issues = append(c.Issues, b.issues...)
		}
	}
	cols := make([]rollup.Column, 0, len(order))
	for _, k := range order {
		cols = append(cols, *agg[k])
	}
	cols = rollup.SortGrouped(cols, rollup.SectionSort{Facet: lane, Tree: tree}, rollup.ItemSort{})
	out := make([]gridCol, len(cols))
	for i, c := range cols {
		out[i] = gridCol{key: c.Key, title: c.Title}
	}
	return out
}

// renderLaneBoard draws the whole 2D break-out board as GLOBAL horizontal bands.
// One column-title header row spans the top; beneath it each lane is ONE band —
// a single dim `─ <lane>` header labeling the whole row, then every visible
// column's cell for that lane rendered to the SAME rows, so reading across a band
// shows how that lane plays out across the columns. A band's height is the tallest
// cell among its columns (shrunk fairly, tallest first, only when the frame can't
// hold them all); a column empty in a lane renders aligned blank space. focusCol
// is the active shown-column position (−1 if the cursor's column is off-screen);
// focusBandKey/focusIdx pinpoint the focused card inside its lane cell.
func (m Model) renderLaneBoard(cols []rollup.Column, bands [][]laneBand, order []gridCol, colStart, nVis, bodyH, colW int, now time.Time, showStatus bool, focusCol int, focusBandKey string, focusIdx int) string {
	innerW := colW - 1
	if innerW < 4 {
		innerW = 4
	}
	gap := strings.Repeat(" ", boardColGap)

	// Per-(shown column) cell lookup: lane key -> that column's cards for the lane.
	cellOf := make([]map[string][]bd.Issue, nVis)
	for vi := 0; vi < nVis; vi++ {
		ci := colStart + vi
		cm := map[string][]bd.Issue{}
		if ci < len(bands) {
			for _, b := range bands[ci] {
				cm[b.key] = b.issues
			}
		}
		cellOf[vi] = cm
	}

	// Column-title header row (the grouper keys), identical to the 1D column head.
	var head []string
	for vi := 0; vi < nVis; vi++ {
		ci := colStart + vi
		title, count := "", styDim.Render("(0)")
		active := ci == focusCol
		if ci < len(cols) {
			title = cols[ci].Title
			count = styDim.Render(fmt.Sprintf("(%d)", len(cols[ci].Issues)))
		}
		titleStyle := styColTitleOff
		if active {
			titleStyle = styColTitleOn.Underline(true)
		}
		titleW := innerW - ansi.StringWidth(ansi.Strip(count)) - 1
		if titleW < 1 {
			titleW = 1
		}
		cell := titleStyle.Render(ansi.Truncate(title, titleW, "…")) + " " + count
		head = append(head, pad(cell, colW))
		if vi < nVis-1 {
			head = append(head, gap)
		}
	}
	lines := []string{strings.Join(head, "")}

	// Budget: give each band its natural height (tallest cell among shown columns),
	// then shrink the tallest bands one row at a time until the column-header row
	// plus every band's header + region fits bodyH. Windowing (below) reclaims any
	// remaining overflow inside a shrunk cell.
	nb := len(order)
	region := make([]int, nb)
	sum := 1 // the column-title header row
	for bi, gc := range order {
		h := 0
		for vi := 0; vi < nVis; vi++ {
			if n := len(cellOf[vi][gc.key]); n > h {
				h = n
			}
		}
		region[bi] = h
		sum += 1 + h // band header + card region
	}
	for sum > bodyH {
		tallest := -1
		for bi := range region {
			if tallest < 0 || region[bi] > region[tallest] {
				tallest = bi
			}
		}
		if tallest < 0 || region[tallest] == 0 {
			break
		}
		region[tallest]--
		sum--
	}

	for bi, gc := range order {
		lines = append(lines, styDim.Render(ansi.Truncate("─ "+gc.title, m.boardWidth(), "…")))
		regionH := region[bi]
		if regionH <= 0 {
			continue
		}
		blocks := make([][]string, nVis)
		for vi := 0; vi < nVis; vi++ {
			ci := colStart + vi
			fIdx := -1
			if ci == focusCol && gc.key == focusBandKey {
				fIdx = focusIdx
			}
			key := ""
			if ci < len(cols) {
				key = cols[ci].Key + "\x00" + gc.key
			}
			blocks[vi] = renderCell(cellOf[vi][gc.key], fIdx, regionH, innerW, colW, m.ageCellFn(now), showStatus, m.relFlagsFor, m.issueEmph, m.winStart, key)
		}
		for r := 0; r < regionH; r++ {
			var rowCells []string
			for vi := 0; vi < nVis; vi++ {
				rowCells = append(rowCells, blocks[vi][r])
				if vi < nVis-1 {
					rowCells = append(rowCells, gap)
				}
			}
			lines = append(lines, strings.Join(rowCells, ""))
		}
	}

	// Normalize to exactly bodyH lines so the board fills the frame; a lane count
	// taller than a short terminal is clipped (the frame still fits).
	for len(lines) < bodyH {
		lines = append(lines, "")
	}
	if len(lines) > bodyH {
		lines = lines[:bodyH]
	}
	return strings.Join(lines, "\n")
}

// renderCell draws one lane's cell for one column into exactly regionH lines,
// each padded to colW so columns stay aligned across the band. A cell that fits
// shows every card (blank rows fill the rest); a cell taller than regionH windows
// its cards with reserved ↑/↓ indicator rows, so a held-key scroll changes line
// CONTENT, not line COUNT. focusRow is the cell-local focused row, or −1.
func renderCell(cell []bd.Issue, focusRow, regionH, innerW, colW int, age ageFn, showStatus bool, flagsFn func(bd.Issue) relFlags, emphFn func(string) rowEmph, starts map[string]int, key string) []string {
	lines := make([]string, 0, regionH)
	total := len(cell)
	if total <= regionH {
		for i := 0; i < regionH; i++ {
			if i < total {
				lines = append(lines, formatRow(cell[i], focusRow == i, innerW, showStatus, flagsFn(cell[i]), emphFn(cell[i].ID), false, age))
			} else {
				lines = append(lines, "")
			}
		}
	} else {
		cardWindow := max(1, regionH-2) // reserved ↑/↓ rows
		start := windowStart(starts, key, total, cardWindow, focusRow)
		if start > 0 {
			lines = append(lines, styDim.Render(fmt.Sprintf("↑ %d more", start)))
		} else {
			lines = append(lines, "")
		}
		end := start + cardWindow
		for i := start; i < end; i++ {
			if i < total {
				lines = append(lines, formatRow(cell[i], focusRow == i, innerW, showStatus, flagsFn(cell[i]), emphFn(cell[i].ID), false, age))
			} else {
				lines = append(lines, "")
			}
		}
		if below := total - end; below > 0 {
			lines = append(lines, styDim.Render(fmt.Sprintf("↓ %d more", below)))
		} else {
			lines = append(lines, "")
		}
	}
	for len(lines) < regionH {
		lines = append(lines, "")
	}
	if len(lines) > regionH {
		lines = lines[:regionH]
	}
	for i, l := range lines {
		lines[i] = pad(ansi.Truncate(l, colW, "…"), colW)
	}
	return lines
}

// renderColumn draws one borderless column of exactly cardWindow+3 terminal
// rows (title 1, always-reserved ↑/↓ indicator lines 2, cards). The indicator
// lines are reserved even when empty so a window jump changes line CONTENT,
// never line COUNT — layout jitter would defeat the renderer's line diff. The
// active column is marked by its accent+underlined header (not a box), and a
// trailing pad column keeps neighbors from running together across the gap.
func renderColumn(col rollup.Column, active bool, focusRow, start, cardWindow, colW int, age ageFn, showStatus bool, flagsFn func(bd.Issue) relFlags, emphFn func(string) rowEmph) string {
	innerW := colW - 1 // one trailing pad column so content never touches the gap
	if innerW < 4 {
		innerW = 4
	}
	total := len(col.Issues)

	lines := make([]string, 0, cardWindow+3)
	titleStyle := styColTitleOff
	if active {
		titleStyle = styColTitleOn.Underline(true)
	}
	count := styDim.Render(fmt.Sprintf("(%d)", total))
	titleW := innerW - ansi.StringWidth(fmt.Sprintf("(%d)", total)) - 1
	if titleW < 1 {
		titleW = 1
	}
	lines = append(lines, titleStyle.Render(ansi.Truncate(col.Title, titleW, "…"))+" "+count)

	if start > 0 {
		lines = append(lines, styDim.Render(fmt.Sprintf("↑ %d more", start)))
	} else {
		lines = append(lines, "")
	}

	end := start + cardWindow
	for i := start; i < end; i++ {
		if i < total {
			lines = append(lines, formatRow(col.Issues[i], active && i == focusRow, innerW, showStatus, flagsFn(col.Issues[i]), emphFn(col.Issues[i].ID), false, age))
		} else {
			lines = append(lines, "")
		}
	}

	if below := total - end; below > 0 {
		lines = append(lines, styDim.Render(fmt.Sprintf("↓ %d more", below)))
	} else {
		lines = append(lines, "")
	}

	// Pad every line to the full column width so columns align and the gap
	// between them stays a clean, constant whitespace channel.
	for i, l := range lines {
		lines[i] = pad(ansi.Truncate(l, colW, "…"), colW)
	}
	return strings.Join(lines, "\n")
}
