package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/awhitty/bb/internal/bd"
)

// The window must stay FIXED while focus moves inside it and leap only at
// the edges — a sliding window would rewrite every column line per keypress.
func TestWindowStartPageJumps(t *testing.T) {
	starts := map[string]int{}
	const total, win = 73, 10

	// Walking down inside the first window: start stays 0.
	for row := 0; row < win; row++ {
		if got := windowStart(starts, "c", total, win, row); got != 0 {
			t.Fatalf("row %d: start = %d, want 0 (fixed window)", row, got)
		}
	}
	// One past the edge: leap so focus sits at the top with runway below.
	if got := windowStart(starts, "c", total, win, win); got != win {
		t.Fatalf("edge down: start = %d, want %d", got, win)
	}
	// And stays fixed again while walking within the new page.
	for row := win; row < 2*win; row++ {
		if got := windowStart(starts, "c", total, win, row); got != win {
			t.Fatalf("row %d: start = %d, want %d", row, got, win)
		}
	}
	// Paging back up past the top edge: focus lands at the window bottom.
	if got := windowStart(starts, "c", total, win, win-1); got != 0 {
		t.Fatalf("edge up: start = %d, want 0", got)
	}
	// G (bottom): start clamps to total-win.
	if got := windowStart(starts, "c", total, win, total-1); got != total-win {
		t.Fatalf("bottom: start = %d, want %d", got, total-win)
	}
	// Unfocused column (focusRow -1) keeps its remembered start, clamped.
	if got := windowStart(starts, "c", total, win, -1); got != total-win {
		t.Fatalf("unfocused: start = %d", got)
	}
	// Shrunk column: remembered start clamps down.
	if got := windowStart(starts, "c", 5, win, -1); got != 0 {
		t.Fatalf("shrunk column: start = %d, want 0", got)
	}
}

// Rows must render at exactly the requested width — stable line content is
// what the renderer's diff depends on. Covers both the status-shown and
// status-hidden (grouped-by-status) column layouts, both the kanban card
// (chips=false) and the full-width list row (chips=true), and labeled issues so
// the label chips never push a row past its width.
func TestFormatRowExactWidth(t *testing.T) {
	now := time.Now()
	long := bd.Issue{ID: "demo-abc", Title: strings.Repeat("very long title ", 10), Status: "open", Priority: 0, UpdatedAt: "2026-07-08T00:00:00Z"}
	short := bd.Issue{ID: "g-1", Title: "hi", Status: "closed", Priority: 3}
	labeled := bd.Issue{ID: "g-2", Title: "a card with a few labels", Status: "open", Priority: 1, Labels: []string{"backend", "urgent", "infra"}, DupCount: 2}
	for _, w := range []int{20, 40, 80} {
		for _, is := range []bd.Issue{long, short, labeled} {
			for _, focused := range []bool{true, false} {
				for _, showStatus := range []bool{true, false} {
					for _, f := range []relFlags{{}, {blocked: true}, {blocked: true, blocking: true}} {
						for _, e := range []rowEmph{{}, {marker: "★", color: styEmphMarker}, {muted: true}} {
							for _, chips := range []bool{true, false} {
								// Both age modes must render the cell width-stable:
								// activity (updated_at) and time-in-status (which may
								// ramp color, but color never changes width).
								for _, mode := range []ageMode{ageActivity, ageStatus} {
									age := func(x bd.Issue) string { return ageCellFor(x, now, mode, nil) }
									line := formatRow(is, focused, w, showStatus, f, e, chips, age)
									if got := ansi.StringWidth(line); got != w {
										t.Fatalf("width(formatRow(%s, focused=%v, w=%d, showStatus=%v, emph=%+v, chips=%v, mode=%d)) = %d", is.ID, focused, w, showStatus, e, chips, mode, got)
									}
								}
							}
						}
					}
				}
			}
		}
	}
}

func TestFitBlockExactDimensions(t *testing.T) {
	in := "short\n" + strings.Repeat("x", 300) + "\nline3\nline4"
	out := fitBlock(in, 40, 3)
	lines := strings.Split(out, "\n")
	if len(lines) != 3 {
		t.Fatalf("lines = %d", len(lines))
	}
	for i, l := range lines {
		if ansi.StringWidth(l) > 40 {
			t.Fatalf("line %d overflows: %d", i, ansi.StringWidth(l))
		}
	}
	// Padding up.
	if got := strings.Count(fitBlock("one", 10, 5), "\n"); got != 4 {
		t.Fatalf("padded newlines = %d", got)
	}
}

func TestClampPriority(t *testing.T) {
	cases := []struct {
		in, want int
	}{
		{-1, 0}, {0, 0}, {2, 2}, {4, 4}, {5, 4},
	}
	for _, c := range cases {
		if got := clamp(c.in, 0, 4); got != c.want {
			t.Fatalf("clamp(%d,0,4) = %d, want %d", c.in, got, c.want)
		}
	}
}
