package ui

// row.go is the ONE shared row formatter. Columns are fixed-width lipgloss
// column cells composed with JoinHorizontal:
//
//	▌ P1 wip     Calendar sync polish                    3h
//
// focus-gutter · priority · status · title(flex) · age(right). Focus is a
// left-edge accent bar in a fixed 2-col gutter (reserved blank when
// unfocused, so rows never shift) — NOT reverse-video, which fought the
// per-cell colors. Every cell keeps its own color on the focused row; only the
// bar and a bold title mark focus. No brackets, no glyphs, no id.

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/awhitty/bb/internal/bd"
)

// styLabelChip is the compact label pill on the full-width list/tree rows: dim
// text on the subtle chip background (colChip, a hair off the terminal bg), so a
// label reads as a token without shouting. Chips appear ONLY where the flex
// title cell has spare width (formatRow / treeLine); the width-critical
// kanban/swim/miller cards suppress them.
var styLabelChip = lipgloss.NewStyle().Foreground(colDim).Background(colChip)

// labelChips renders an issue's labels as compact chips plus a "+N" marker for
// the Stage-3 fan-out DupCount (how many OTHER facet columns this card also
// lands in), bounded to maxW cells — chips that don't fit are dropped. It
// returns "" when there is nothing to show (no labels, dup 0) OR no room, so a
// row with no labels renders byte-identically to before chips existed.
func labelChips(labels []string, dup, maxW int) string {
	if maxW <= 0 || (len(labels) == 0 && dup == 0) {
		return ""
	}
	parts := make([]string, 0, len(labels)+1)
	w := 0
	for _, l := range labels {
		chip := styLabelChip.Render(l)
		cw := lipgloss.Width(chip)
		if w+cw+1 > maxW { // +1 for the leading space between chips
			break
		}
		parts = append(parts, chip)
		w += cw + 1
	}
	if dup > 0 {
		mark := styDim.Render(fmt.Sprintf("+%d", dup))
		if w+lipgloss.Width(mark)+1 <= maxW {
			parts = append(parts, mark)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " ")
}

// titleWithChips renders the title cell followed by label chips, composed to
// EXACTLY titleW cells (chips carved out of the right end). With no chips it is
// titleCell(title, titleW) verbatim, so the no-label row is byte-identical.
func titleWithChips(is bd.Issue, title string, titleW int, focused bool) string {
	chips := labelChips(is.Labels, is.DupCount, max(0, titleW-8))
	if chips == "" {
		return titleCell(title, titleW, focused)
	}
	cw := lipgloss.Width(chips)
	tw := titleW - cw - 1
	if tw < 1 {
		return titleCell(title, titleW, focused)
	}
	return titleCell(title, tw, focused) + " " + chips
}

// focusGutter is the reserved left column for the focus marker; relSlot is the
// blocked/blocking indicator column (fixed 2 cols, blank when neither, so rows
// never shift).
const (
	focusGutter = 2
	relSlot     = 2
)

// relFlags: whether an issue is BLOCKED (has an open dependency) and/or
// BLOCKING (an open issue depends on it). Precomputed per load (flagsByID).
type relFlags struct{ blocked, blocking bool }

// relFlags computes the flags for one issue from the in-memory indexes.
func (m Model) relFlagsFor(is bd.Issue) relFlags {
	var f relFlags
	for _, id := range bd.BlockerIDs(is) {
		if b, ok := m.byID[id]; ok && b.Status != bd.StatusClosed {
			f.blocked = true
			break
		}
	}
	for _, id := range m.revDeps[is.ID] {
		if d, ok := m.byID[id]; ok && d.Status != bd.StatusClosed {
			f.blocking = true
			break
		}
	}
	return f
}

// relIndicator renders the 2-col blocked ↑ / blocking ↓ marker.
func relIndicator(f relFlags) string {
	up, down := " ", " "
	if f.blocked {
		up = styBlocked.Render("↑")
	}
	if f.blocking {
		down = styBlocking.Render("↓")
	}
	return up + down
}

// rightBlockW is the fixed metadata width (priority · status · age) shared by
// the flat row and the tree's right-pinned block. Gaps are plain spaces now
// (no reverse-video to cover).
const rightBlockW = 2 + 1 + statusColW + 1 + ageColW

func clampPri(p int) int {
	if p < 0 {
		return 0
	}
	if p > 4 {
		return 4
	}
	return p
}

// gutter renders the 2-col focus marker: an accent bar when focused, blank of
// the same width otherwise (so focus movement never shifts the row).
func gutter(focused bool) string {
	if focused {
		return styFocusBar.Render("▌") + " "
	}
	return "  "
}

func priorityCell(p int) string {
	return prioStyle(p, false).Width(2).Render(fmt.Sprintf("P%d", clampPri(p)))
}

// statusCell is the compact fixed-width status cell: one color-carried glyph
// (statusGlyph) in a statusColW-wide column, so a long status name (needs_review)
// can never wrap the row. The full word lives in the detail panel, which has room.
func statusCell(status string) string {
	return statusStyle(status, false).Width(statusColW).Render(statusGlyph(status))
}

func titleCell(title string, w int, focused bool) string {
	sty := styPlain
	if focused {
		sty = styBold // subtle emphasis on the focused title
	}
	return sty.Width(w).Render(ansi.Truncate(title, w, "…"))
}

func ageCell(ts string, now time.Time) string {
	return styDim.Width(ageColW).Align(lipgloss.Right).Render(compactAge(ts, now))
}

// ageFn resolves one issue's right-hand age cell — which timestamp it shows and
// how loud it reads — per the active age mode. It is built once per frame
// (Model.ageCellFn) and threaded down beside flagsFn/emphFn, the same closure
// seam the board already uses, so the row formatters stay free functions.
type ageFn func(bd.Issue) string

// formatRow renders one flat issue row at exactly width w. showStatus is false
// when the list is ALREADY grouped by status (the section header carries it).
// emph is the render-time emphasis decoration (zero = none): a gutter marker,
// or (spotlight) a muted row when this issue is not a spotlight target. chips is
// true ONLY for the full-width list rows (list.go), where the flex title cell
// has spare width for label chips; the kanban card (board.go) passes false so a
// chip never overflows the narrow innerW or annihilates the truncated title.
func formatRow(is bd.Issue, focused bool, w int, showStatus bool, flags relFlags, emph rowEmph, chips bool, age ageFn) string {
	if w <= 0 {
		return ""
	}
	if emph.muted {
		// Spotlight: dim everything that is not a target. Render plain (no
		// per-cell color) so a single faint style covers the whole row.
		plain := fmt.Sprintf("P%d %s", clampPri(is.Priority), is.Title)
		return pad("  "+styDim.Render(ansi.Truncate(plain, max(1, w-focusGutter), "…")), w)
	}
	fixed := focusGutter + 2 + 1 + relSlot + 1 + 1 + ageColW // gutter, pri, indicator, gaps, age
	if showStatus {
		fixed += statusColW + 1
	}
	titleW := w - fixed
	if titleW < 1 {
		return pad(gutterEmph(focused, emph)+ansi.Truncate(fmt.Sprintf("P%d %s", clampPri(is.Priority), is.Title), max(1, w-focusGutter), "…"), w)
	}
	cells := []string{gutterEmph(focused, emph), priorityCell(is.Priority), " "}
	if showStatus {
		cells = append(cells, statusCell(is.Status), " ")
	}
	titleWithLabels := titleCell(is.Title, titleW, focused)
	if chips {
		titleWithLabels = titleWithChips(is, is.Title, titleW, focused)
	}
	cells = append(cells, relIndicator(flags), " ", titleWithLabels, " ", age(is))
	return lipgloss.JoinHorizontal(lipgloss.Top, cells...)
}

// compactAge is the terse right-column age ("3h", "2d", "5w", "12mo", "now")
// for an RFC3339 timestamp string. Unparseable → "".
func compactAge(ts string, now time.Time) string {
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return ""
	}
	return compactDur(now.Sub(t))
}

// compactDur is the terse relative-age words for an elapsed duration, the ONE
// formatter shared by the activity-time cell (compactAge) and the status-age
// cell (age.go). Negative durations (a clock skew) read "now".
func compactDur(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d < 14*24*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	case d < 8*7*24*time.Hour:
		return fmt.Sprintf("%dw", int(d.Hours()/(24*7)))
	default:
		return fmt.Sprintf("%dmo", int(d.Hours()/(24*30)))
	}
}
