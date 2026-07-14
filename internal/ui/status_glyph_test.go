package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/awhitty/bb/internal/bd"
)

// The status CELL is one fixed-width glyph so a long status name (needs_review)
// can never wrap its column. These tests pin the mapping (the bd vocabulary),
// the width invariant, and the no-wrap guarantee at several pane widths.

// TestStatusGlyphMapping covers all six statuses plus an unknown/custom one:
// every glyph is exactly one column wide, and the unknown status degrades to the
// defined width-1 fallback rather than the raw (overflowing) string.
func TestStatusGlyphMapping(t *testing.T) {
	want := map[string]string{
		"open":         "○",
		"in_progress":  "◐",
		"blocked":      "●",
		"deferred":     "❄",
		"closed":       "✓",
		"needs_review": "◇",
	}
	for status, glyph := range want {
		if got := statusGlyph(status); got != glyph {
			t.Errorf("statusGlyph(%q) = %q, want %q", status, got, glyph)
		}
		if w := ansi.StringWidth(glyph); w != 1 {
			t.Errorf("glyph for %q is %d cells wide, want 1", status, w)
		}
	}
	// An unknown/custom status must not fall through to its raw (wide) string.
	custom := "awaiting_triage"
	fb := statusGlyph(custom)
	if fb == custom {
		t.Fatalf("unknown status %q rendered its raw string — would overflow the cell", custom)
	}
	if w := ansi.StringWidth(fb); w != 1 {
		t.Fatalf("unknown-status fallback %q is %d cells wide, want 1", fb, w)
	}
	// statusCell composes to exactly statusColW cells for every status, known or not.
	for _, s := range []string{"open", "in_progress", "blocked", "deferred", "closed", "needs_review", custom} {
		if w := ansi.StringWidth(statusCell(s)); w != statusColW {
			t.Errorf("statusCell(%q) is %d cells wide, want statusColW=%d", s, w, statusColW)
		}
	}
}

// needsReviewFixture is a small tree whose leaves sit in needs_review — the
// status whose full name (12 chars) previously wrapped the fixed status column.
func needsReviewFixture() []bd.Issue {
	return []bd.Issue{
		{ID: "g-1", Title: "Parent epic", Status: "open", Priority: 1, IssueType: "epic", UpdatedAt: "2026-07-08T00:00:00Z"},
		{ID: "g-1.1", Title: "A leaf at the review fence", Status: "needs_review", Priority: 0, IssueType: "task", Parent: "g-1", UpdatedAt: "2026-07-09T00:00:00Z"},
		{ID: "g-1.2", Title: "Another review-fence leaf", Status: "needs_review", Priority: 2, IssueType: "bug", Parent: "g-1", UpdatedAt: "2026-07-09T00:00:00Z"},
		{ID: "g-2", Title: "An in-progress loner", Status: "in_progress", Priority: 1, IssueType: "task", UpdatedAt: "2026-07-10T00:00:00Z"},
	}
}

// TestNeedsReviewNeverWrapsAcrossViewsAndWidths is the regression guard for the
// reported bug: needs_review rows rendered in the list and the tree, at a range
// of pane widths, must keep every frame line inside the terminal (no wrap) and
// show the ◇ glyph rather than the "needs_review" word.
func TestNeedsReviewNeverWrapsAcrossViewsAndWidths(t *testing.T) {
	const h = 30
	for _, w := range []int{40, 60, 80, 120} {
		for _, view := range []struct {
			name string
			keys []string
		}{
			{"list", []string{"b"}},
			{"tree", []string{"5"}},
			{"kanban", nil},
		} {
			t.Run(view.name+"-w"+itoa(w), func(t *testing.T) {
				m := testModel(t, needsReviewFixture(), w, h)
				m = press(t, m, view.keys...)
				lines := frameLines(t, m, w, h) // asserts == h lines, none wider than w
				plain := ansi.Strip(strings.Join(lines, "\n"))
				// The tree groups by hierarchy, so every row renders the fixed
				// status CELL — the exact surface the bug was screenshotted on.
				// It must show the ◇ glyph and never the raw word (whose wrap
				// into "needs_r" / "eview" was the bug). The list/kanban default
				// to grouping BY status, where the per-card cell is suppressed
				// (the column header carries the status) — so they exercise only
				// the frame-fit guarantee here; the cell itself is glyph-only and
				// width-1 by TestStatusGlyphMapping.
				if view.name == "tree" {
					if !strings.Contains(plain, "◇") {
						t.Fatalf("needs_review ◇ glyph absent in the tree at w=%d:\n%s", w, plain)
					}
					if strings.Contains(plain, "needs_r") {
						t.Fatalf("needs_review word (or a wrap fragment) leaked into the tree at w=%d:\n%s", w, plain)
					}
				}
			})
		}
	}
}

// itoa avoids pulling strconv into a table just for subtest names.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
