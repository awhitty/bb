package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/awhitty/bb/internal/bd"
)

// The actual screenshot case (demo-uvw.1.8): a coordinator comment — one long
// prose paragraph, heavy with hyphenated tokens, dates, and inline-code chips —
// exactly the shape that tripped glamour's word-wrap into orphan lines.
const h4zComment = "**BUILT (2026-07-10, coordinator):** live parts active on Alex's machine — " +
	"`~/.zshrc` guard (BEADS_ACTOR=claude:$CLAUDE_CODE_SESSION_ID when unset), " +
	"`~/.local/bin/bd-blame` (executable, on PATH; joins interactions.jsonl actors to " +
	"reconvo titles, prints reconvo-read + fork-ask lines, honest dead ends for " +
	"manual/pre-attribution actors), reconvo-skill bullet, chezmoi source updated " +
	"(uncommitted — dotfiles repo had unrelated pending changes)."

// visLines strips ANSI and returns the visible lines of a rendered block.
func visLines(rendered string) []string {
	out := make([]string, 0)
	for _, ln := range strings.Split(rendered, "\n") {
		out = append(out, ansi.Strip(ln))
	}
	return out
}

func firstWord(s string) string {
	s = strings.TrimLeft(s, " ")
	if i := strings.IndexByte(s, ' '); i >= 0 {
		return s[:i]
	}
	return s
}

// assertNoOverflowOrOrphan is the core reflow contract: (1) no visible line
// exceeds the pane width, and (2) no mid-paragraph orphan — for two consecutive
// prose lines in the same paragraph, the second line's first word must NOT have
// fit on the first (otherwise it was orphaned early, the bug we are fixing).
func assertNoOverflowOrOrphan(t *testing.T, label string, rendered string, width int) {
	t.Helper()
	lines := visLines(rendered)
	for i, ln := range lines {
		if w := ansi.StringWidth(strings.TrimRight(ln, " ")); w > width {
			t.Errorf("%s w=%d: line %d overflows (%d cells): %q", label, width, i, w, ln)
		}
	}
	for i := 0; i+1 < len(lines); i++ {
		a, b := lines[i], lines[i+1]
		bt := strings.TrimLeft(b, " ")
		if strings.TrimSpace(a) == "" || bt == "" {
			continue // paragraph boundary
		}
		if bulletRe.MatchString(bt) { // b starts a new list item, not a continuation
			continue
		}
		av := ansi.StringWidth(strings.TrimRight(a, " "))
		if av+1+ansi.StringWidth(firstWord(b)) <= width {
			t.Errorf("%s w=%d: orphan — line %d (%d cells) had room for %q from the next line:\n  %q\n  %q",
				label, width, i, av, firstWord(b), a, b)
		}
	}
}

// TestReflowFlowsCleanly is the demo-rst.18 regression: the uvw.1.8 comment,
// and a dated-note block with --- separators and a list, must flow to the pane
// width with no overflow and no orphaned single-word lines, at several widths.
func TestReflowFlowsCleanly(t *testing.T) {
	notes := "**CREATED (2026-07-10):** scoped from the in-progress-column retro conversation; not started.\n\n" +
		"---\n\n" +
		"**DESIGN UNDERWAY (2026-07-10):** design subagent dispatched from the retros session; it reports back, this session writes the design into the bead.\n\n" +
		"- a bullet item that is quite long and will absolutely need to wrap onto more than one line at this width\n" +
		"- short one"
	md := newMdRenderer("dark")
	for _, tc := range []struct{ name, src string }{
		{"uvw.1.8-comment", h4zComment},
		{"dated-notes-with-list", notes},
	} {
		for _, w := range []int{40, 60, 80, 110} {
			assertNoOverflowOrOrphan(t, tc.name, md.render(tc.src, w), w)
		}
	}
}

// TestReflowJoinsAuthorHardWraps: text authored with hard newlines at one width
// must re-flow to the pane width, not keep the stored breaks.
func TestReflowJoinsAuthorHardWraps(t *testing.T) {
	src := "This paragraph was hard-wrapped by its author at a narrow\n" +
		"column, so it carries single newlines mid-sentence that must\n" +
		"be treated as soft and joined before wrapping to the pane."
	md := newMdRenderer("dark")
	out := md.render(src, 100)
	var textLines int
	for _, ln := range visLines(out) {
		if strings.TrimSpace(ln) != "" {
			textLines++
		}
	}
	if textLines > 2 {
		// at width 100 the ~170-char paragraph should be ~2 lines, not the 3 stored
		t.Errorf("author hard-wraps not joined: got %d text lines at width 100:\n%s", textLines, out)
	}
	assertNoOverflowOrOrphan(t, "joined", out, 100)
}

// TestReflowPreservesCodeFence: fenced code takes glamour's own wrap (the guard)
// so its lines stay verbatim and are never merged into surrounding prose.
func TestReflowPreservesCodeFence(t *testing.T) {
	src := "Intro prose before the block.\n\n```\nkeep this  spacing   verbatim\n```\n\nOutro prose after."
	md := newMdRenderer("dark")
	out := md.render(src, 60)
	if !strings.Contains(ansi.Strip(out), "keep this  spacing   verbatim") {
		t.Fatalf("code fence not preserved verbatim:\n%s", out)
	}
	if md.r == nil {
		t.Fatal("fenced content must go through glamour's width-keyed wrap renderer")
	}
}

// TestReflowPreservesList: a wrapped list item hangs under its own text, not the
// bullet, and separate items stay separate.
func TestReflowPreservesList(t *testing.T) {
	src := "- first item that is long enough to wrap across at least two lines at this pane width here today\n- second"
	md := newMdRenderer("dark")
	lines := visLines(md.render(src, 50))
	var bullets, hung int
	for _, ln := range lines {
		trimmed := strings.TrimLeft(ln, " ")
		if bulletRe.MatchString(trimmed) {
			bullets++
		} else if strings.HasPrefix(ln, "    ") && strings.TrimSpace(ln) != "" {
			hung++ // a continuation indented under the bullet's text
		}
	}
	if bullets != 2 {
		t.Errorf("expected 2 bullet lines, got %d:\n%s", bullets, strings.Join(lines, "\n"))
	}
	if hung == 0 {
		t.Errorf("wrapped list item did not hang-indent under its text:\n%s", strings.Join(lines, "\n"))
	}
}

// TestReflowKeepsRuleSeparator: the repo's dated-note --- separator survives as
// its own rule line and does not glue the paragraphs it divides together.
func TestReflowKeepsRuleSeparator(t *testing.T) {
	src := "First dated note paragraph here.\n\n---\n\nSecond dated note paragraph here."
	md := newMdRenderer("dark")
	lines := visLines(md.render(src, 60))
	var rule bool
	for _, ln := range lines {
		if t := strings.TrimSpace(ln); t != "" && strings.Trim(t, "-─") == "" {
			rule = true
		}
	}
	if !rule {
		t.Errorf("--- separator did not render as a rule line:\n%s", strings.Join(lines, "\n"))
	}
}

// Regression for the detail-view freeze: glamour must run with a FIXED style
// and a CACHED renderer. Auto-style detection inside a running bubbletea
// Program deadlocks (its OSC reply is eaten by bubbletea's stdin reader), and
// per-open construction was where that detection lived.
//
// Prose (the common case) goes through the wrap-disabled reflow renderer, which
// is width-INDEPENDENT: cached once, reused at every width. Content with a code
// fence or table takes glamour's own width-keyed wrap: cached per width, one
// rebuild when the width changes.
func TestMdRendererIsCachedPerWidth(t *testing.T) {
	md := newMdRenderer("dark")
	out1 := md.render("# hello\n\nsome *markdown*", 60)
	if out1 == "" || !strings.Contains(out1, "hello") {
		t.Fatalf("render produced %q", out1)
	}
	flow := md.rFlow
	if flow == nil {
		t.Fatal("prose reflow renderer not cached")
	}
	md.render("more text", 80)
	if md.rFlow != flow {
		t.Fatal("prose renderer must be reused across widths — it is wrap-independent")
	}

	// The fenced-code / table fallback is width-keyed and rebuilds on resize.
	fenced := "```\ncode\n```"
	md.render(fenced, 60)
	first := md.r
	if first == nil {
		t.Fatal("fenced content did not cache the wrap renderer")
	}
	md.render(fenced, 60)
	if md.r != first {
		t.Fatal("wrap renderer rebuilt at unchanged width — must be reused")
	}
	md.render(fenced, 80)
	if md.r == first {
		t.Fatal("wrap renderer must rebuild when the width changes")
	}
}

func TestDetailTabsContent(t *testing.T) {
	const w, h = 100, 24
	is := bd.Issue{
		ID: "g-1", Title: "A title", Status: "open", IssueType: "task",
		Parent:       "g-0",
		Labels:       []string{"x", "y"},
		Dependencies: []bd.Dependency{{IssueID: "g-1", DependsOnID: "g-9", Type: "blocks"}},
		Description:  "Body with `code`.",
		Notes:        "a note",
	}
	m := testModel(t, []bd.Issue{is, {ID: "g-0", Title: "Parent"}, {ID: "g-9", Title: "Blocker"}}, w, h)
	m.openDetail(is, []bd.HistoryEntry{{Date: "2026-07-08T00:00:00Z", Status: "open", Priority: 0}}, nil)

	// Overview: meta line (type, labels) + glamour body + notes.
	ov := m.overviewContent(is, 60)
	for _, must := range []string{"task", "x", "y", "code", "notes", "a note"} {
		if !strings.Contains(ov, must) {
			t.Fatalf("overview missing %q:\n%s", must, ov)
		}
	}
	if m.md.rFlow == nil {
		t.Fatal("overview did not go through the cached reflow renderer")
	}
	// Relationships tab: the swimlane rooted (statically) on this bead. g-1 is
	// blocked by g-9, so the blocked-by lane carries the blocker.
	rel := m.detailTabContent(is, tabRelated, 90)
	for _, must := range []string{"blocked-by", "Blocker"} {
		if !strings.Contains(rel, must) {
			t.Fatalf("relationships tab missing %q:\n%s", must, rel)
		}
	}
	// History: the change trajectory.
	if !strings.Contains(m.historyContent(60), "history") {
		t.Fatalf("history content:\n%s", m.historyContent(60))
	}
	// Comments: empty state.
	if !strings.Contains(m.commentsContent(60), "no comments") {
		t.Fatal("comments should show the empty state")
	}
}
