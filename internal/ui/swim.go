package ui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/awhitty/bb/internal/bd"
	"github.com/awhitty/bb/internal/rollup"
)

// The relationship swimlane board (key R): a 2D OVERVIEW rooted at one bead.
// COLUMNS are a facet (status by default — open · wip · blocked · defer · done);
// ROWS (lanes) are the relationship kind — sub-issues (children) · blocked-by
// (its blockers) · blocking (its dependents) · siblings (same parent). Each cell
// holds the related beads at that (relationship × column) intersection. Only
// lanes and columns that carry at least one card are shown.
//
// The column axis is a rollup.Facet: the built columns come from the facet's
// two-level section sort over every related bead, so status is byte-identical to
// the old fixed status order while type/priority/label become available. The
// lane axis is fixed (it REQUIRES a root, so it is not a free grouping facet);
// see the product note in the stage-6 handoff.
//
// A 2D grid is tighter than the list that already truncates, so this is an
// OVERVIEW, never a reading surface: compact cards (short id · priority color ·
// blocked/blocking arrow · truncated title), readable-min-width columns with
// horizontal paging (the same b-board machinery), and the focused card's FULL
// title in the header strip (enter opens its detail). It reuses the octopus's
// whole-board relationship indexes (byID/childrenOf/revDeps), the row
// formatter's cells, and the layout engine — it invents no geometry.

// swimLaneKind identifies one relationship row.
type swimLaneKind int

const (
	swimChildren swimLaneKind = iota
	swimBlockedBy
	swimBlocking
	swimSiblings
)

func (k swimLaneKind) label() string {
	switch k {
	case swimChildren:
		return "sub-issues"
	case swimBlockedBy:
		return "blocked-by"
	case swimBlocking:
		return "blocking"
	default:
		return "siblings"
	}
}

// gridCol is one column of the relationship grid: the facet key that buckets a
// cell's cards and the header title shown over it. For the status facet the key
// is the canonical status name, so statusWord/statusStyle render it exactly as
// the old fixed status columns did.
type gridCol struct {
	key   string
	title string
}

// swimLane is one relationship row: its kind and the count of related issues in
// it (the cards themselves live in swimMatrix.cells, keyed by lane × column).
type swimLane struct {
	kind  swimLaneKind
	total int
}

// swimMatrix is the built board: the shown lanes, the shown columns (facet keys,
// only-populated, in the facet's section order), the per-(lane × column) cards,
// and the root issue.
type swimMatrix struct {
	root    bd.Issue
	lanes   []swimLane
	columns []gridCol
	cells   map[int]map[int][]bd.Issue // laneIdx -> colIdx -> cards
}

// swimRef locates one card in the matrix: its lane, its index within that
// lane's column cell, and the issue itself.
type swimRef struct {
	lane    int
	cellIdx int
	is      bd.Issue
}

// gridFacet is the current column-axis facet, defaulting to status (the
// byte-identical everyday board). Set through the grouper control (M) /
// set_view; the zero value "" means status.
func (m Model) gridFacet() rollup.Facet {
	if m.swimFacet == "" {
		return rollup.FacetStatus
	}
	return m.swimFacet
}

// buildGrid assembles the matrix rooted at rootID from the whole-board indexes,
// bucketing each lane's related beads by the column facet. The column order and
// titles come from the facet's two-level section sort (rollup.Group +
// SortGrouped) over every related bead; empty lanes and empty columns are
// dropped so the grid shows only populated cells. With facet == status the
// columns equal the old swimStatusOrder-filtered-to-populated, so the render is
// byte-identical to the previous fixed-status board.
func (m Model) buildGrid(rootID string, facet rollup.Facet) swimMatrix {
	if facet == "" {
		facet = rollup.FacetStatus
	}
	root := m.byID[rootID]

	// Each candidate lane's related beads, deduped, with the root excluded.
	gather := func(ids []string) []bd.Issue {
		var out []bd.Issue
		seen := map[string]bool{}
		for _, id := range ids {
			if id == rootID || seen[id] {
				continue
			}
			is, ok := m.byID[id]
			if !ok {
				continue
			}
			seen[id] = true
			out = append(out, is)
		}
		return out
	}
	var sibIDs []string
	if root.Parent != "" {
		sibIDs = m.childrenOf[root.Parent]
	}
	srcs := []struct {
		kind   swimLaneKind
		issues []bd.Issue
	}{
		{swimChildren, gather(m.childrenOf[rootID])},
		{swimBlockedBy, gather(bd.BlockerIDs(root))},
		{swimBlocking, gather(m.revDeps[rootID])},
		{swimSiblings, gather(sibIDs)},
	}

	// Column order + titles: the facet's section sort over every related bead.
	var all []bd.Issue
	for _, s := range srcs {
		all = append(all, s.issues...)
	}
	ordered := rollup.SortGrouped(rollup.Group(all, facet),
		rollup.SectionSort{Facet: facet, Tree: m.treeSort}, rollup.ItemSort{})
	keyToCol := map[string]int{}
	columns := make([]gridCol, 0, len(ordered))
	for _, c := range ordered {
		keyToCol[c.Key] = len(columns)
		columns = append(columns, gridCol{key: c.Key, title: c.Title})
	}

	sm := swimMatrix{root: root, columns: columns, cells: map[int]map[int][]bd.Issue{}}
	for _, s := range srcs {
		if len(s.issues) == 0 {
			continue
		}
		laneIdx := len(sm.lanes)
		sm.lanes = append(sm.lanes, swimLane{kind: s.kind, total: len(s.issues)})
		byCol := map[int][]bd.Issue{}
		// Group buckets by the same facet keys (fanning a multi-valued facet like
		// label into every matching column); map each bucket to its column index.
		for _, gc := range rollup.Group(s.issues, facet) {
			ci, ok := keyToCol[gc.Key]
			if !ok {
				continue
			}
			byCol[ci] = append(byCol[ci], gc.Issues...)
		}
		// Stable order inside each cell: priority (highest first), then id.
		for ci := range byCol {
			cs := byCol[ci]
			sort.SliceStable(cs, func(a, b int) bool {
				if cs[a].Priority != cs[b].Priority {
					return cs[a].Priority < cs[b].Priority
				}
				return cs[a].ID < cs[b].ID
			})
			byCol[ci] = cs
		}
		sm.cells[laneIdx] = byCol
	}
	return sm
}

// colCards returns every card in the column at the given SHOWN-column position,
// lane-major (top lane first) — the vertical navigation list. Each carries its
// lane and its index within the cell so a focused card can be pinpointed for
// rendering.
func (sm swimMatrix) colCards(colPos int) []swimRef {
	if colPos < 0 || colPos >= len(sm.columns) {
		return nil
	}
	var out []swimRef
	for li := range sm.lanes {
		for ci, is := range sm.cells[li][colPos] {
			out = append(out, swimRef{lane: li, cellIdx: ci, is: is})
		}
	}
	return out
}

// --- root / focus resolution ---

// swimRootID is the current board root. It reads only swimRoot (set at entry),
// never focusedIssue — which would recurse, since focusedIssue resolves the
// focused CARD through the matrix while the swimlane is active.
func (m Model) swimRootID() string {
	if m.swimRoot != "" {
		if _, ok := m.byID[m.swimRoot]; ok {
			return m.swimRoot
		}
	}
	return ""
}

// swimFocus resolves the currently focused card reference (and its column
// position), clamping stale indexes against the live matrix.
func (m Model) swimFocus(sm swimMatrix) (swimRef, int, bool) {
	if len(sm.columns) == 0 {
		return swimRef{}, 0, false
	}
	col := clamp(m.swimCol, 0, len(sm.columns)-1)
	cards := sm.colCards(col)
	if len(cards) == 0 {
		return swimRef{}, col, false
	}
	pos := clamp(m.swimPos, 0, len(cards)-1)
	return cards[pos], col, true
}

// swimFocusedIssue is what focusedIssue returns while the swimlane is active:
// the focused card, or the root if no card is focusable.
func (m Model) swimFocusedIssue() *bd.Issue {
	sm := m.buildGrid(m.swimRootID(), m.gridFacet())
	if ref, _, ok := m.swimFocus(sm); ok {
		is := ref.is
		return &is
	}
	if r, ok := m.byID[m.swimRootID()]; ok {
		return &r
	}
	return nil
}

// --- entering / navigating ---

// enterScope records the pre-scope arrangement (once, on the first entry) and
// scopes the board to id's neighborhood. Re-rooting while already scoped (R on a
// neighbor) moves the focus but keeps the ORIGINAL return, so esc still leaves in
// one press. Called at the top of enterSwimRooted, before the view/detail mutate,
// so the captured snapshot is the true pre-scope state.
func (m *Model) enterScope(id string) {
	if id == "" {
		return
	}
	if m.scopeRoot == "" {
		m.scopeReturn = m.snapshotNav() // remember where esc returns to
		m.pushLayer(layerScope)         // esc leaves the scope via exitScope (layers.go)
	}
	m.scopeRoot = id
}

// enterSwimRooted opens the swimlane rooted at id AND scopes the board to id's
// neighborhood — the swimlane is one presentation of that scoped root. Callers
// reset the focus to the first card. The activity overlay and any open detail are
// dropped so the swimlane is visible.
func (m *Model) enterSwimRooted(id string) {
	m.enterScope(id) // capture the pre-scope return before the view/detail change
	m.swimRoot = id
	m.swimCol, m.swimPos = 0, 0
	m.activityView = false
	m.detail = nil
	m.setView(ViewSwim)
	// Rebuild so the scoped neighborhood propagates into the board/list/tree
	// structures too — cycling a mode (v/m/1-5) inside the scope then renders the
	// same neighborhood without re-deriving it.
	m.rebuild()
}

// enterSwim opens the swimlane rooted at the currently focused bead.
func (m *Model) enterSwim() {
	id := ""
	if is := m.focusedIssue(); is != nil {
		id = is.ID
	}
	m.enterSwimRooted(id)
}

// swimNav moves the focus. dRow steps within a column (through a cell's cards,
// then across lanes); dCol steps between shown columns, keeping the focused lane
// where the target column has a card in it.
func (m *Model) swimNav(dCol, dRow int) {
	sm := m.buildGrid(m.swimRootID(), m.gridFacet())
	if len(sm.columns) == 0 {
		return
	}
	m.swimCol = clamp(m.swimCol, 0, len(sm.columns)-1)
	if dRow != 0 {
		cards := sm.colCards(m.swimCol)
		m.swimPos = clamp(m.swimPos+dRow, 0, max(0, len(cards)-1))
		return
	}
	if dCol != 0 {
		cards := sm.colCards(m.swimCol)
		lane := -1
		if m.swimPos >= 0 && m.swimPos < len(cards) {
			lane = cards[m.swimPos].lane
		}
		m.swimCol = clamp(m.swimCol+dCol, 0, len(sm.columns)-1)
		next := sm.colCards(m.swimCol)
		np := clamp(m.swimPos, 0, max(0, len(next)-1))
		if lane >= 0 {
			for i, r := range next {
				if r.lane == lane {
					np = i
					break
				}
			}
		}
		m.swimPos = np
	}
}

// swimTop / swimBottom jump within the focused column.
func (m *Model) swimTop() { m.swimPos = 0 }
func (m *Model) swimBottom() {
	sm := m.buildGrid(m.swimRootID(), m.gridFacet())
	m.swimPos = max(0, len(sm.colCards(clamp(m.swimCol, 0, max(0, len(sm.columns)-1))))-1)
}

// --- horizontal window (readable-width columns, page like the b-board) ---

func (m Model) swimColumnWindow(nColumns int) (start, nVis int) {
	boardW := m.boardWidth()
	if nColumns == 0 || boardW <= 0 {
		return 0, 0
	}
	nVis = (boardW + 1) / (readableColW + 1)
	if nVis < 1 {
		nVis = 1
	}
	if nVis > nColumns {
		nVis = nColumns
	}
	start = windowStart(m.winStart, "\x00swim", nColumns, nVis, clamp(m.swimCol, 0, nColumns-1))
	return start, nVis
}

// --- rendering ---

func (m Model) viewSwim(bodyH int) string {
	w := m.boardWidth()
	rootID := m.swimRootID()
	if rootID == "" {
		return lipgloss.Place(w, bodyH, lipgloss.Center, lipgloss.Center,
			styDim.Render("no bead focused — esc, pick one, then R"))
	}
	sm := m.buildGrid(rootID, m.gridFacet())
	root := sm.root
	rootLine := styFocusBar.Render("● ") + styDetailTitle.Render(m.shortID(rootID)) + "  " +
		styBold.Render(ansi.Truncate(root.Title, max(4, w-ansi.StringWidth(m.shortID(rootID))-4), "…"))

	if len(sm.lanes) == 0 {
		body := []string{
			rootLine, "",
			styDim.Render(fmt.Sprintf("no sub-issues, blockers, dependents, or siblings for %s", m.shortID(rootID))),
			"",
			styDim.Render("esc back"),
		}
		return strings.Join(body, "\n")
	}

	ref, focusCol, hasFocus := m.swimFocus(sm)
	colStart, nVis := m.swimColumnWindow(len(sm.columns))
	colW := (w - boardColGap*(nVis-1)) / nVis
	if colW < 6 {
		colW = 6
	}

	// Focused card's full title strip.
	focusLine := styDim.Render("select a card — enter opens it")
	if hasFocus {
		fis := ref.is
		focusLine = styDim.Render("→ "+m.shortID(fis.ID)+"  ") +
			ansi.Truncate(fis.Title, max(4, w-ansi.StringWidth(m.shortID(fis.ID))-4), "…")
	}

	// Budget the lanes into the remaining height. Header strip = root(1) +
	// focus(1) + blank(1) + column-header(1).
	const headerRows = 4
	budget := bodyH - headerRows
	if budget < len(sm.lanes) {
		budget = len(sm.lanes) // degrade: at least the labels
	}
	per := budget / len(sm.lanes)
	cellH := per - 1
	if cellH < 1 {
		cellH = 1
	}

	lines := []string{rootLine, focusLine, "", m.swimHeaderRow(sm, colStart, nVis, colW)}
	for li := range sm.lanes {
		fLane, fCellIdx := -1, -1
		if hasFocus && ref.lane == li && focusCol >= colStart && focusCol < colStart+nVis {
			fLane, fCellIdx = li, ref.cellIdx
		}
		lines = append(lines, m.swimLaneBlock(sm, li, colStart, nVis, colW, cellH, fLane, focusCol, fCellIdx))
	}
	return strings.Join(lines, "\n")
}

// swimHeaderRow renders the column names over their columns; the focused
// column's name is highlighted. Status keys render through statusWord/
// statusStyle (byte-identical to the old status header); other facet keys fall
// back to the plain key + dim style.
func (m Model) swimHeaderRow(sm swimMatrix, colStart, nVis, colW int) string {
	focusCol := clamp(m.swimCol, 0, max(0, len(sm.columns)-1))
	var cells []string
	for p := colStart; p < colStart+nVis && p < len(sm.columns); p++ {
		key := sm.columns[p].key
		sty := statusStyle(key, false)
		if p == focusCol {
			sty = sty.Bold(true).Underline(true)
		}
		cells = append(cells, pad(sty.Render(statusWord(key)), colW))
		if p < colStart+nVis-1 && p < len(sm.columns)-1 {
			cells = append(cells, strings.Repeat(" ", boardColGap))
		}
	}
	return strings.Join(cells, "")
}

// swimLaneBlock renders one lane: a bold label line above a row of fixed-size
// cells (one per visible column), joined horizontally.
func (m Model) swimLaneBlock(sm swimMatrix, laneIdx, colStart, nVis, colW, cellH, focusLane, focusColPos, focusCellIdx int) string {
	lane := sm.lanes[laneIdx]
	label := styBold.Render(lane.kind.label()) + styDim.Render(fmt.Sprintf("  %d", lane.total))

	blocks := make([][]string, 0, nVis)
	for p := colStart; p < colStart+nVis && p < len(sm.columns); p++ {
		fIdx := -1
		if laneIdx == focusLane && p == focusColPos {
			fIdx = focusCellIdx
		}
		blocks = append(blocks, m.swimCell(sm.cells[laneIdx][p], colW, cellH, fIdx))
	}

	rows := []string{ansi.Truncate(label, m.boardWidth(), "…")}
	for r := 0; r < cellH; r++ {
		var cells []string
		for ci, blk := range blocks {
			cells = append(cells, blk[r])
			if ci < len(blocks)-1 {
				cells = append(cells, strings.Repeat(" ", boardColGap))
			}
		}
		rows = append(rows, strings.Join(cells, ""))
	}
	return strings.Join(rows, "\n")
}

// swimCell renders one (lane × column) cell into exactly cellH lines of width
// colW. Cards beyond the height collapse into a "+N more" line — this is an
// overview, so a full cell is expected.
func (m Model) swimCell(cards []bd.Issue, colW, cellH, focusIdx int) []string {
	lines := make([]string, cellH)
	shown := cellH
	overflow := 0
	if len(cards) > cellH {
		shown = cellH - 1
		overflow = len(cards) - shown
	}
	for i := 0; i < cellH; i++ {
		switch {
		case i < shown && i < len(cards):
			lines[i] = m.swimCard(cards[i], colW, i == focusIdx)
		case overflow > 0 && i == shown:
			lines[i] = pad(styDim.Render(fmt.Sprintf("  +%d more", overflow)), colW)
		default:
			lines[i] = ""
		}
		lines[i] = pad(ansi.Truncate(lines[i], colW, "…"), colW)
	}
	return lines
}

// swimBody renders the relationship matrix rooted at rootID as a STATIC
// overview — no card focus, no horizontal paging — for the detail view's
// relationships tab. The column axis is always status here (a fixed
// relationships overview). Every populated column is shown and every card in a
// cell is rendered (the detail viewport scrolls), so the root is a fixed
// vantage: moving the cursor never re-roots it (the re-rooting jumpiness the
// standalone R board has). It reuses the same cell/card renderers as the
// interactive board.
func (m Model) swimBody(rootID string, width int) string {
	if width < 8 {
		width = 8
	}
	sm := m.buildGrid(rootID, rollup.FacetStatus)
	if len(sm.lanes) == 0 {
		return styDim.Render("(no sub-issues, blockers, dependents, or siblings)")
	}
	n := len(sm.columns)
	colW := (width - boardColGap*(n-1)) / n
	if colW < swimStaticMinColW {
		colW = swimStaticMinColW // never cram below a readable compact-card width
	}

	// Column-name header (no focus highlight — this is static).
	var hcells []string
	for i, col := range sm.columns {
		hcells = append(hcells, pad(statusStyle(col.key, false).Render(statusWord(col.key)), colW))
		if i < n-1 {
			hcells = append(hcells, strings.Repeat(" ", boardColGap))
		}
	}
	lines := []string{strings.Join(hcells, "")}

	for li := range sm.lanes {
		lane := sm.lanes[li]
		// A cell as tall as the fullest one in the lane, so no card is hidden.
		cellH := 1
		for ci := range sm.columns {
			if c := len(sm.cells[li][ci]); c > cellH {
				cellH = c
			}
		}
		blocks := make([][]string, 0, n)
		for ci := range sm.columns {
			blocks = append(blocks, m.swimCell(sm.cells[li][ci], colW, cellH, -1))
		}
		rows := []string{styBold.Render(lane.kind.label()) + styDim.Render(fmt.Sprintf("  %d", lane.total))}
		for r := 0; r < cellH; r++ {
			var cells []string
			for ci, blk := range blocks {
				cells = append(cells, blk[r])
				if ci < len(blocks)-1 {
					cells = append(cells, strings.Repeat(" ", boardColGap))
				}
			}
			rows = append(rows, strings.Join(cells, ""))
		}
		lines = append(lines, "", strings.Join(rows, "\n"))
	}
	return strings.Join(lines, "\n")
}

// swimStaticMinColW is the compact-card floor for the static detail-tab matrix.
const swimStaticMinColW = 16

// swimIDCellW is the fixed short-id column inside a compact card, so titles
// line up down a cell.
const swimIDCellW = 6

// swimCard renders one compact card: focus/emphasis gutter · short id ·
// priority · blocked/blocking arrow · truncated title, exactly colW wide.
func (m Model) swimCard(is bd.Issue, colW int, focused bool) string {
	g := gutterEmph(focused, m.issueEmph(is.ID))
	id := styDim.Render(padRight(m.shortID(is.ID), swimIDCellW))
	pri := priorityCell(is.Priority)         // width 2
	arrow := relIndicator(m.relFlagsFor(is)) // width 2
	fixed := focusGutter + swimIDCellW + 1 + 2 + 1 + relSlot + 1
	titleW := colW - fixed
	if titleW < 3 {
		return ansi.Truncate(g+id+" "+pri, colW, "…")
	}
	return g + id + " " + pri + " " + arrow + " " + titleCell(is.Title, titleW, focused)
}
