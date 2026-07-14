package ui

import (
	"fmt"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/awhitty/bb/internal/bd"
	"github.com/awhitty/bb/internal/rollup"
)

// The DEFAULT view is a single-column, full-width sectioned list — one section
// header per rollup group, its rows below spanning the whole terminal so
// titles read in full (the 5-column kanban truncated them to ~10 chars). One
// page-jump window over the flattened rows, exactly like the tree.

type listRowKind int

const (
	rowHeader listRowKind = iota
	rowIssue
	rowSpacer
)

type listRow struct {
	kind  listRowKind
	title string    // header: group name
	count int       // header: group size
	key   string    // header: rollup key (for coloring)
	issue *bd.Issue // issue row
	col   int       // issue row: its section (== m.colIdx)
	row   int       // issue row: its index within the section (== m.rowIdx)
}

// buildListRows flattens the ordered rollup columns into header/issue/spacer
// rows. Focus stays (section, row) = (colIdx, rowIdx), so focusedIssue/jumpTo
// keep working unchanged; only the LAYOUT differs from the board.
func buildListRows(cols []rollup.Column) []listRow {
	var rows []listRow
	for ci, c := range cols {
		if len(c.Issues) == 0 {
			continue
		}
		rows = append(rows, listRow{kind: rowHeader, title: c.Title, count: len(c.Issues), key: c.Key})
		for ri := range c.Issues {
			is := c.Issues[ri]
			rows = append(rows, listRow{kind: rowIssue, issue: &is, col: ci, row: ri})
		}
		rows = append(rows, listRow{kind: rowSpacer})
	}
	if n := len(rows); n > 0 && rows[n-1].kind == rowSpacer {
		rows = rows[:n-1]
	}
	return rows
}

// listFocusIndex is the flat position of the focused issue in listRows.
func (m Model) listFocusIndex() int {
	for i, r := range m.listRows {
		if r.kind == rowIssue && r.col == m.colIdx && r.row == m.rowIdx {
			return i
		}
	}
	return 0
}

// --- list navigation: focus flows across sections ---

func (m *Model) listDown() {
	if len(m.columns) == 0 {
		return
	}
	if m.rowIdx < len(m.columns[m.colIdx].Issues)-1 {
		m.rowIdx++
	} else if m.colIdx < len(m.columns)-1 {
		m.colIdx, m.rowIdx = m.colIdx+1, 0
	}
}

func (m *Model) listUp() {
	if len(m.columns) == 0 {
		return
	}
	if m.rowIdx > 0 {
		m.rowIdx--
	} else if m.colIdx > 0 {
		m.colIdx--
		m.rowIdx = len(m.columns[m.colIdx].Issues) - 1
	}
}

// listSection jumps whole sections (h/l): to the header of the prev/next group.
func (m *Model) listSection(d int) {
	if len(m.columns) == 0 {
		return
	}
	m.colIdx = clamp(m.colIdx+d, 0, len(m.columns)-1)
	m.rowIdx = 0
}

func (m *Model) listTop() { m.colIdx, m.rowIdx = 0, 0 }

func (m *Model) listBottom() {
	if len(m.columns) == 0 {
		return
	}
	m.colIdx = len(m.columns) - 1
	m.rowIdx = max(0, len(m.columns[m.colIdx].Issues)-1)
}

// --- rendering ---

// viewList renders the sectioned list at exactly bodyH lines × the available
// width, with the same reserved ↑/↓ indicator lines and page-jump window as
// the tree.
func (m Model) viewList(bodyH int) string {
	w := m.boardWidth()
	if len(m.columns) == 0 {
		empty := styDim.Render(m.emptyBoardText())
		return lipgloss.Place(w, bodyH, lipgloss.Center, lipgloss.Center, empty)
	}
	rows := m.listRows
	age := m.ageCellFn(time.Now())
	showStatus := m.mode != rollup.ModeStatus // status mode → header carries it
	return m.renderNavList(bodyH, len(rows), m.listFocusIndex(), "\x00list",
		func(i int, focused bool) string {
			return m.renderListRow(rows[i], focused, w, age, showStatus)
		},
		moreTop, moreBottom)
}

func (m Model) renderListRow(r listRow, focused bool, w int, age ageFn, showStatus bool) string {
	switch r.kind {
	case rowHeader:
		return renderSectionHeader(r, w)
	case rowIssue:
		// Flush under the header — the colored header + blank-line separator
		// already delimit sections, so a decorative indent is wasted width.
		return formatRow(*r.issue, focused, w, showStatus, m.relFlagsFor(*r.issue), m.issueEmph(r.issue.ID), true, age)
	default:
		return ""
	}
}

// renderSectionHeader draws "task  44" — bold name, dim count, colored by
// status when the group is a status.
func renderSectionHeader(r listRow, w int) string {
	nameSty := stySection
	if _, isStatus := statusWords[r.key]; isStatus {
		nameSty = stySection.Foreground(statusStyle(r.key, false).GetForeground())
	}
	head := nameSty.Render(ansi.Truncate(r.title, max(1, w-8), "…")) + "  " +
		stySectionCount.Render(fmt.Sprintf("%d", r.count))
	return ansi.Truncate(head, w, "")
}
