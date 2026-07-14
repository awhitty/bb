// Package layout is the ONE geometry engine for the TUI. Given the terminal
// size and which chrome is present, it returns named rectangles whose heights
// sum to the terminal height BY CONSTRUCTION. No view recomputes the budget —
// which is what permanently prevents the frame-overflow bug class. Views
// render INTO a rect and clip to it; they never re-derive height from the
// terminal.
//
// Pure geometry: ints only, no styling, fully unit-testable.
package layout

// Rect is a region of the screen.
type Rect struct {
	X, Y, W, H int
}

// Empty reports whether the rect has no drawable area.
func (r Rect) Empty() bool { return r.W <= 0 || r.H <= 0 }

// Screen is the composed frame. Header is the top row, Footer the bottom row,
// Body everything between. When a panel is present, Body is split into Main |
// 1-col gap | Panel; otherwise Main == Body and Panel is empty.
type Screen struct {
	Header Rect
	Body   Rect
	Main   Rect
	Panel  Rect
	Footer Rect
}

// Compute lays out a w×h terminal with a 1-row header and 1-row footer. panelW
// is the desired panel width (0 = no panel); it is clamped so Main keeps at
// least minMain columns. Header.H + Body.H + Footer.H == h for h >= 3.
func Compute(w, h, panelW, minMain int) Screen {
	if w < 1 {
		w = 1
	}
	if h < 1 {
		h = 1
	}
	bodyH := h - 2
	if bodyH < 1 {
		bodyH = 1 // tiny terminals: keep one body row (View still clips)
	}
	s := Screen{
		Header: Rect{X: 0, Y: 0, W: w, H: 1},
		Body:   Rect{X: 0, Y: 1, W: w, H: bodyH},
		Footer: Rect{X: 0, Y: h - 1, W: w, H: 1},
	}
	s.Main = s.Body
	if panelW > 0 {
		if maxPanel := w - minMain - 1; panelW > maxPanel {
			panelW = maxPanel
		}
		if panelW > 0 {
			s.Main = Rect{X: 0, Y: 1, W: w - panelW - 1, H: bodyH}
			s.Panel = Rect{X: w - panelW, Y: 1, W: panelW, H: bodyH}
		}
	}
	return s
}

// Window returns the page-jump start offset for a scrolling list: the window
// stays FIXED while focus moves inside it and leaps a page only when focus
// hits an edge. A sliding window rewrites every line per keypress; fixed-
// until-edge keeps most frames to a two-line diff, which is what keeps held-
// key scrolling fast. Pass focus < 0 for "no focus" (offset only clamped).
func Window(total, winH, focus, prevStart int) int {
	maxStart := total - winH
	if maxStart < 0 {
		maxStart = 0
	}
	start := prevStart
	if start > maxStart {
		start = maxStart
	}
	if start < 0 {
		start = 0
	}
	if focus >= 0 {
		if focus < start {
			// paged up: focus at the bottom, full runway above
			start = focus - winH + 1
			if start < 0 {
				start = 0
			}
		} else if focus >= start+winH {
			// paged down: focus at the top, full runway below
			start = focus
			if start > maxStart {
				start = maxStart
			}
		}
	}
	return start
}
