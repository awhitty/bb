package ui

import (
	"fmt"
	"strings"
)

// navlist is the ONE page-jump windowed-list renderer. Several surfaces (the
// sectioned list, the tree outline, the activity feed) render a flat stream of
// rows into a bodyH-tall region with the same shape: a reserved overflow line
// on top, a fixed-until-edge window of rows, padding, and a reserved overflow
// line on the bottom. The overflow lines are ALWAYS present (even when empty)
// so a window jump changes line CONTENT, never line COUNT — layout jitter would
// defeat bubbletea's line diff and the held-key scrolling it buys us. This is
// that shape, extracted once.
//
// The caller owns its item slice, its focus index, and how a row looks (the
// row closure); navlist owns the geometry. It is deliberately NOT a widget
// framework — one function taking a renderer, per internal/ui/ARCHITECTURE.md.

// renderNavList windows n items into a bodyH-line region. focus is the selected
// index (< 0 for none); winKey names this list's persisted window offset. row
// renders item i; top/bottom render the overflow indicator lines given how many
// items are hidden above / below the window (0 = none, i.e. the resting label).
func (m Model) renderNavList(bodyH, n, focus int, winKey string,
	row func(i int, focused bool) string,
	top, bottom func(hidden int) string) string {

	win := max(1, bodyH-2)
	start := windowStart(m.winStart, winKey, n, win, focus)

	lines := make([]string, 0, bodyH)
	lines = append(lines, top(start))
	for i := start; i < start+win && i < n; i++ {
		lines = append(lines, row(i, i == focus))
	}
	for len(lines) < bodyH-1 {
		lines = append(lines, "")
	}
	below := n - (start + win)
	if below < 0 {
		below = 0
	}
	lines = append(lines, bottom(below))
	return strings.Join(lines, "\n")
}

// moreTop / moreBottom are the default "↑/↓ N more" overflow lines shared by the
// sectioned list and the tree (empty when nothing is hidden that way).
func moreTop(hidden int) string {
	if hidden > 0 {
		return styDim.Render(fmt.Sprintf("↑ %d more", hidden))
	}
	return ""
}

func moreBottom(hidden int) string {
	if hidden > 0 {
		return styDim.Render(fmt.Sprintf("↓ %d more", hidden))
	}
	return ""
}
