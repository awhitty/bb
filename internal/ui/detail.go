package ui

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/awhitty/bb/internal/bd"
)

// The detail view (enter) is tabbed: Overview · History · Relationships ·
// Comments, cycled with tab / shift-tab. Overview leads with a prominent title
// and a clean meta line, body via glamour. History is the collapsed change
// trajectory; Relationships is the swimlane matrix rooted (statically) on this
// bead — its sub-issues / blockers / dependents / siblings by status; Comments
// the discussion. Same sub-tabs power the focused preview panel.

type detailTab int

const (
	tabOverview detailTab = iota
	tabHistory
	tabRelated
	tabComments
)

var detailTabNames = []string{"overview", "history", "relationships", "comments"}

func (t detailTab) next() detailTab { return detailTab((int(t) + 1) % len(detailTabNames)) }
func (t detailTab) prev() detailTab {
	return detailTab((int(t) + len(detailTabNames) - 1) % len(detailTabNames))
}

// tabStrip renders the sub-view tab bar, active tab highlighted.
func tabStrip(active detailTab, w int) string {
	cells := make([]string, len(detailTabNames))
	for i, name := range detailTabNames {
		if detailTab(i) == active {
			cells[i] = styTabOn.Render(name)
		} else {
			cells[i] = styTabOff.Render(name)
		}
	}
	return lipgloss.NewStyle().Width(w).Render(strings.Join(cells, styTabOff.Render("   ")))
}

// mdRenderer is a cached glamour renderer with a FIXED style name.
//
// It must never use glamour.WithAutoStyle() (or any termenv background
// detection): detection emits an OSC query and blocks reading stdin for the
// terminal's reply — but inside a running bubbletea Program, bubbletea's input
// reader consumes that reply, so detection hangs and the whole app freezes on
// the first card open. The style is resolved ONCE in main(), before the
// Program starts reading stdin, and passed in. The renderer is rebuilt only
// when the wrap width changes (terminal resize), never per open.
type mdRenderer struct {
	style string
	width int
	r     *glamour.TermRenderer // width-keyed; glamour's own word-wrap (fallback path)
	rFlow *glamour.TermRenderer // wrap DISABLED; width-independent (prose reflow path)
}

func newMdRenderer(style string) *mdRenderer {
	return &mdRenderer{style: style}
}

// setStyle switches the glamour standard style (e.g. dark↔light on a theme
// toggle) and forces a rebuild from that EXPLICIT name on the next render. It
// never probes termenv — WithStandardStyle takes the name verbatim — so it is
// safe to call while the bubbletea Program owns stdin (the deadlock only comes
// from WithAutoStyle / a live OSC background query).
func (m *mdRenderer) setStyle(style string) {
	if style == m.style {
		return
	}
	m.style = style
	m.r = nil // next render rebuilds the TermRenderers with the new style
	m.rFlow = nil
}

// render turns bead markdown into a styled, pane-width block.
//
// glamour v1.0.0 styles markdown correctly (bold, inline-code chips, lists,
// code fences, tables) but its internal word-wrap has a defect: on prose with
// hyphenated tokens near a wrap boundary (dates like 2026-07-10, compounds like
// in-progress-column) it emits single-word ORPHAN lines mid-paragraph — the
// long-standing "bead text re-wraps horribly" complaint. glamour is also the
// single wrap authority for soft breaks (it already joins the author's hard
// newlines), so this is purely a wrapper bug, not a hard-break-preservation one.
//
// Fix: for the common case (prose / lists / inline code — no fenced code and no
// tables, which is 100% of the current bead corpus) render with glamour's wrap
// DISABLED, then re-wrap each logical line with ansi.Wrap, which has no orphan
// defect. Content that carries a code fence or a table keeps glamour's own wrap
// (its layout of those blocks is width-aware and must not be touched).
func (m *mdRenderer) render(src string, width int) string {
	if width < 20 {
		width = 20
	}
	if hasCodeFenceOrTable(src) {
		return m.renderWrapped(src, width)
	}
	return m.renderFlow(src, width)
}

// renderWrapped is glamour's own word-wrap (the fallback for fenced code /
// tables). v1.0.0 does NOT hang-indent a WRAPPED list item — the continuation
// drops back to the bullet column — so post-process to align continuations.
func (m *mdRenderer) renderWrapped(src string, width int) string {
	if m.r == nil || m.width != width {
		r, err := glamour.NewTermRenderer(
			glamour.WithStandardStyle(m.style),
			glamour.WithWordWrap(width),
		)
		if err != nil {
			return src
		}
		m.r = r
		m.width = width
	}
	out, err := m.r.Render(src)
	if err != nil {
		return src
	}
	return hangingIndent(strings.TrimRight(out, "\n"))
}

// renderFlow renders with glamour's wrap disabled — each paragraph or list item
// becomes ONE logical line (styling intact, soft breaks already joined) — then
// re-wraps to the pane width with a correct wrapper.
func (m *mdRenderer) renderFlow(src string, width int) string {
	if m.rFlow == nil {
		r, err := glamour.NewTermRenderer(
			glamour.WithStandardStyle(m.style),
			glamour.WithWordWrap(0), // 0 = disabled: paragraphs stay one line
		)
		if err != nil {
			return src
		}
		m.rFlow = r
	}
	// glamour with wrap DISABLED keeps the author's single newlines as hard
	// breaks (only glamour's own wrap joins them), so join soft breaks in the
	// source first: each paragraph / list item becomes one logical line
	// regardless of the width it was authored at. Block boundaries — blank
	// lines, list markers, headings, rules — are preserved.
	out, err := m.rFlow.Render(joinSoftBreaks(src))
	if err != nil {
		return src
	}
	return reflowLines(strings.TrimRight(out, "\n"), width)
}

var (
	headingRe = regexp.MustCompile(`^#{1,6}\s`)
	ruleRe    = regexp.MustCompile(`^(?:[-*_]\s*){3,}$`)
)

// blockStart reports whether a line begins a new markdown block that a soft-
// break join must not fold into the previous line.
func blockStart(trimmed string) bool {
	return bulletRe.MatchString(trimmed) || headingRe.MatchString(trimmed) ||
		ruleRe.MatchString(trimmed) || strings.HasPrefix(trimmed, ">")
}

// joinSoftBreaks folds the single newlines WITHIN a paragraph (or a list item's
// continuation) into spaces — soft-wrap semantics — so a paragraph authored at
// any column becomes one logical line. It preserves blank-line paragraph
// breaks, and never folds across a block boundary: a list marker, heading, or
// rule starts a fresh line, and nothing folds into a heading or rule. It runs
// only on the reflow path, which fenced code and tables never take.
func joinSoftBreaks(src string) string {
	var out []string
	for _, cur := range strings.Split(src, "\n") {
		ct := strings.TrimLeft(cur, " ")
		if len(out) > 0 {
			prev := out[len(out)-1]
			pt := strings.TrimLeft(prev, " ")
			join := strings.TrimSpace(prev) != "" && strings.TrimSpace(cur) != "" &&
				!blockStart(ct) && !headingRe.MatchString(pt) && !ruleRe.MatchString(pt)
			if join {
				out[len(out)-1] = strings.TrimRight(prev, " ") + " " + ct
				continue
			}
		}
		out = append(out, cur)
	}
	return strings.Join(out, "\n")
}

// tableRowRe matches a markdown table row (starts with a pipe, has an inner one).
var tableRowRe = regexp.MustCompile(`^\s*\|.*\|`)

// hasCodeFenceOrTable reports whether the SOURCE carries a fenced code block or
// a pipe table — the two block kinds whose glamour layout is width-aware and so
// must keep glamour's own wrap. Everything else reflows. A false positive only
// costs a fallback to glamour's wrap (today's behavior), so err loose.
func hasCodeFenceOrTable(src string) bool {
	for _, ln := range strings.Split(src, "\n") {
		t := strings.TrimSpace(ln)
		if strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			return true
		}
		if tableRowRe.MatchString(ln) {
			return true
		}
	}
	return false
}

// reflowLines re-wraps glamour's UNWRAPPED (word-wrap-disabled) output to the
// pane width. Each input line is one logical block-line: a paragraph, a list
// item, a heading, or a rule, indented by glamour and styled inline. It is
// wrapped with ansi.Wordwrap (ANSI-aware, no orphan defect), its leading indent
// re-applied to the first fragment and its continuations hung under the text —
// so a wrapped list item aligns under its own words, not the bullet.
func reflowLines(s string, width int) string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		vis := ansi.Strip(line)
		if strings.TrimSpace(vis) == "" {
			out = append(out, "")
			continue
		}
		trimmed := strings.TrimLeft(vis, " ")
		indent := len(vis) - len(trimmed)
		prefixW := indent
		if marker := bulletRe.FindString(trimmed); marker != "" {
			prefixW = indent + len([]rune(marker)) // hang under the bullet's text
		}
		if prefixW >= width {
			out = append(out, line) // pathologically narrow; leave untouched
			continue
		}
		body := ansi.TruncateLeft(line, prefixW, "")
		// ansi.Wrap word-wraps AND hard-breaks any token too long to fit, so a
		// giant unbreakable token (a long path, an env-var string) can never
		// overflow the pane — the TUI has no horizontal scroll.
		fragments := strings.Split(ansi.Wrap(body, width-prefixW, ""), "\n")
		out = append(out, ansi.Truncate(line, prefixW, "")+fragments[0])
		hang := strings.Repeat(" ", prefixW)
		for _, cont := range fragments[1:] {
			out = append(out, hang+strings.TrimLeft(cont, " "))
		}
	}
	return strings.Join(out, "\n")
}

var bulletRe = regexp.MustCompile(`^([•▪‣◦*+-]|\d+\.)\s+`)

// hangingIndent shifts wrapped list-item continuation lines to start under the
// item's TEXT (after the "• "), not under the bullet. Operates on glamour's
// ANSI output: strip for detection, prepend plain spaces for the fix.
func hangingIndent(s string) string {
	lines := strings.Split(s, "\n")
	itemIndent, textCol := 0, 0
	active := false
	for i, line := range lines {
		vis := ansi.Strip(line)
		trimmed := strings.TrimLeft(vis, " ")
		indent := len(vis) - len(trimmed)
		if marker := bulletRe.FindString(trimmed); marker != "" {
			itemIndent = indent
			textCol = indent + len([]rune(marker))
			active = true
			continue
		}
		switch {
		case trimmed == "":
			active = false
		case active && indent == itemIndent:
			lines[i] = strings.Repeat(" ", textCol-itemIndent) + line // hang under the text
		case indent < itemIndent:
			active = false
		}
	}
	return strings.Join(lines, "\n")
}

// metaLine is the clean status · priority · type · labels line.
func metaLine(is bd.Issue) string {
	parts := []string{
		statusStyle(is.Status, false).Render(statusWord(is.Status)),
		prioStyle(is.Priority, false).Render(fmt.Sprintf("P%d", is.Priority)),
		is.IssueType,
	}
	line := styDim.Render(strings.Join(parts, " · "))
	if len(is.Labels) > 0 {
		line += styDim.Render("   " + strings.Join(is.Labels, " · "))
	}
	return line
}

// detailHeaderLine is the fixed prominent title line above the tab content:
// the short id (accent) then the title (bold).
func detailHeaderLine(displayID, title string) string {
	return styDetailTitle.Render(displayID) + "  " + styBold.Render(title)
}

// --- per-tab content ---

// detailTabContent builds the scrollable body for the active tab. Every tab is
// prefixed with the active share's remark (why this bead was selected) when one
// is present, so it reads near the top of the detail on whichever tab is open —
// inside the scrollable viewport, so it needs no fixed-height surgery.
func (m *Model) detailTabContent(is bd.Issue, tab detailTab, width int) string {
	if width < 10 {
		width = 10
	}
	var body string
	switch tab {
	case tabHistory:
		body = m.historyContent(width)
	case tabRelated:
		// The swimlane rooted (statically) on this bead — a fixed vantage that
		// never re-roots as the cursor moves.
		body = m.decorateSection("related", m.swimBody(is.ID, width), width)
	case tabComments:
		body = m.commentsContent(width)
	default:
		body = m.overviewContent(is, width)
	}
	if rl := m.remarkLines(is.ID, width); len(rl) > 0 {
		return strings.Join(rl, "\n") + "\n\n" + body
	}
	return body
}

func (m *Model) overviewContent(is bd.Issue, width int) string {
	var parts []string
	parts = append(parts, metaLine(is), "")
	desc := strings.TrimSpace(is.Description)
	if desc == "" {
		parts = append(parts, styDim.Render("(no description)"))
	} else {
		parts = append(parts, m.decorateSection("description", m.md.render(desc, width), width))
	}
	if notes := strings.TrimSpace(is.Notes); notes != "" {
		parts = append(parts, "", styBold.Render("notes"), m.decorateSection("notes", m.md.render(notes, width), width))
	}
	// Comments inline — the annotation/decision channel; c adds one.
	if len(m.detailComments) > 0 {
		parts = append(parts, "", styBold.Render(fmt.Sprintf("comments (%d)", len(m.detailComments)))+styDim.Render("  ·  c to add"), m.commentsBody(width))
	} else {
		parts = append(parts, "", styDim.Render("no comments  ·  c to add"))
	}
	return strings.Join(parts, "\n")
}

// historyContent shows WHAT CHANGED between consecutive versions (a before→
// after delta), not the end-state — the trajectory is collapsed to distinct
// (status, priority, title) states, so each step names the field that moved.
func (m *Model) historyContent(width int) string {
	h := m.detailHistory // newest-first, distinct states
	if len(h) == 0 {
		return styDim.Render("(no recorded changes)")
	}
	var b strings.Builder
	b.WriteString(styBold.Render("history") + styDim.Render("  (newest first)") + "\n\n")
	for i, e := range h {
		date := e.Date
		if len(date) >= 10 {
			date = date[:10]
		}
		var delta string
		if i+1 < len(h) {
			delta = historyDelta(h[i+1], e) // older → newer
		} else {
			delta = statusStyle("in_progress", false).Render("created")
		}
		fmt.Fprintf(&b, "%s  %s\n", styDim.Render(date), delta)
	}
	return b.String()
}

// historyDelta describes the change from old→new across the tracked fields.
func historyDelta(old, cur bd.HistoryEntry) string {
	var parts []string
	if old.Status != cur.Status {
		parts = append(parts, statusStyle(old.Status, false).Render(statusWord(old.Status))+
			styDim.Render(" → ")+statusStyle(cur.Status, false).Render(statusWord(cur.Status)))
	}
	if old.Priority != cur.Priority {
		parts = append(parts, prioStyle(old.Priority, false).Render(fmt.Sprintf("P%d", old.Priority))+
			styDim.Render(" → ")+prioStyle(cur.Priority, false).Render(fmt.Sprintf("P%d", cur.Priority)))
	}
	if old.Title != cur.Title {
		parts = append(parts, styDim.Render("title edited"))
	}
	if len(parts) == 0 {
		return styDim.Render("edited")
	}
	return strings.Join(parts, styDim.Render(" · "))
}

func (m *Model) commentsContent(width int) string {
	if len(m.detailComments) == 0 {
		return styDim.Render("(no comments — press c to add)")
	}
	return styBold.Render("comments") + "\n\n" + m.commentsBody(width)
}

// commentsBody renders the comment list (shared by the Comments tab and the
// Overview inline section).
func (m *Model) commentsBody(width int) string {
	var b strings.Builder
	for _, c := range m.detailComments {
		author := firstNonEmpty(str(c["author"]), str(c["created_by"]), str(c["actor"]))
		when := str(c["created_at"])
		if len(when) >= 10 {
			when = when[:10]
		}
		body := firstNonEmpty(str(c["text"]), str(c["body"]), str(c["comment"]))
		head := strings.TrimSpace(author + " " + styDim.Render(when))
		b.WriteString(styBold.Render(head) + "\n")
		b.WriteString(m.md.render(body, width) + "\n\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// --- helpers ---

func padRight(s string, w int) string {
	if d := w - len(s); d > 0 {
		return s + strings.Repeat(" ", d)
	}
	return s
}

func relLabel(k linkKind) string {
	switch k {
	case linkParent:
		return "parent"
	case linkChild:
		return "child"
	case linkNeeds:
		return "depends-on"
	case linkBlocks:
		return "blocked-by"
	}
	return string(k)
}

func str(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
