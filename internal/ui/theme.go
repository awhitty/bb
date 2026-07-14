package ui

import (
	"time"

	"github.com/charmbracelet/lipgloss"
)

const accent = lipgloss.Color("6") // cyan, theme-mapped by the terminal — legible on both backgrounds

// Adaptive palette. Every shade that would otherwise assume one background is an
// AdaptiveColor{Light, Dark}: lipgloss resolves it at RENDER time from the
// package's current background assumption (lipgloss.SetHasDarkBackground, set by
// SetInitialTheme / toggleTheme — never a live probe). ANSI-256 grayscale ramp
// (232 darkest → 255 lightest); the Light shade is dark ink for a light
// terminal, the Dark shade light ink for a dark terminal, each picked for real
// contrast against its background (the dark-gray-on-black body was the failure).
var (
	// colText is default body / P2 text — near-ink on light, near-paper on dark
	// (mirrors glamour's own 234/252 document colors).
	colText = lipgloss.AdaptiveColor{Light: "235", Dark: "252"}
	// colDim is muted secondary text (dates, meta, counts, "open"/"defer",
	// low-priority) — a medium gray with contrast on BOTH backgrounds, replacing
	// the old Faint(true)/ANSI-"8" shades that went dark-on-dark.
	colDim = lipgloss.AdaptiveColor{Light: "242", Dark: "245"}
	// colBorderOff is the inactive panel/column border — subtler than text but
	// still visible on either background.
	colBorderOff = lipgloss.AdaptiveColor{Light: "250", Dark: "240"}
	// colDone is the "done"/closed green — a darker green on light, a lighter
	// green on dark (the old Faint green was near-invisible on dark).
	colDone = lipgloss.AdaptiveColor{Light: "28", Dark: "71"}
	// colChip is the subtle pill background behind an inline bead chiclet (the @
	// browser's mentioned excerpts) — a hair off the terminal background on both
	// themes, so a chiclet reads as a token without shouting.
	colChip = lipgloss.AdaptiveColor{Light: "254", Dark: "238"}
)

// styExcerpt is the body prose of a mentioned batch's excerpt (default text, so
// the chiclets carry the color).
var styExcerpt = lipgloss.NewStyle().Foreground(colText)

// chicletStyle renders an inline bead reference (a mentioned-excerpt chiclet) as
// a small pill: the short id, bold, tinted by the bead's priority, on the subtle
// chip background. The FOCUSED chiclet reverses to a solid priority-colored pill
// so it stands out as you tab across the excerpt's mentions.
func chicletStyle(p int, focused bool) lipgloss.Style {
	s := lipgloss.NewStyle().Bold(true).Foreground(priorityColor(p)).Background(colChip).Padding(0, 1)
	if focused {
		s = s.Reverse(true)
	}
	return s
}

// statusWords is the short lowercase status word, color-carried (statusStyles).
// It renders where there is room for a word — the detail panel, the activity
// feed's transitions, the swim/board column HEADERS, and section-header coloring
// (list.go tests statusWords membership). The compact fixed-width row/tree CELL
// renders a single glyph instead (statusGlyph), so a long status can never wrap
// its column (the needs_review overflow, Alex 2026-07-10).
var statusWords = map[string]string{
	"open":         "open",
	"in_progress":  "wip",
	"blocked":      "blocked",
	"deferred":     "defer",
	"closed":       "done",
	"needs_review": "review",
}

// statusColW is the fixed status-cell width — one glyph (statusGlyph), so the
// cell is exactly one column and never wraps. ageColW is the fixed right-aligned
// age column (widest "12mo").
const (
	statusColW = 1
	ageColW    = 4
)

// The cold ramp for the status-age column (age mode ageStatus): a bead fresh in
// its status reads dim like any age; past ageWarmAfter it warms to amber, and
// past ageColdAfter it reads loud red so stale open work pops at a glance. Only
// open/in_progress/needs_review beads ramp (statusRamps) — coldness is
// meaningless once a bead is closed or deferred. Activity mode never ramps.
const (
	ageWarmAfter = 2 * 24 * time.Hour
	ageColdAfter = 5 * 24 * time.Hour
)

func statusWord(s string) string {
	if w, ok := statusWords[s]; ok {
		return w
	}
	return s
}

// statusStyles / statusStylesF carry each status's color: open gray, wip cyan,
// blocked red, done dim green, defer faint. Focused rows use the reverse
// variant so the whole selected row highlights uniformly.
var (
	statusStyles  = map[string]lipgloss.Style{}
	statusStylesF = map[string]lipgloss.Style{}
)

func statusStyle(status string, focused bool) lipgloss.Style {
	m := statusStyles
	if focused {
		m = statusStylesF
	}
	if s, ok := m[status]; ok {
		return s
	}
	if focused {
		return styDimF
	}
	return styDim
}

// statusGlyphs is the compact fixed-width status CELL — one glyph per status,
// the SAME vocabulary the bd CLI prints so both surfaces speak one language
// (bd list: ○ open · ◐ in_progress · ● blocked · ✓ closed · ❄ deferred · ◇
// needs_review). Color is carried by statusStyle. Every glyph measures width 1
// (ansi.StringWidth, the same width lipgloss pads the cell with), so the column
// stays aligned in every layout. An unknown/custom status degrades to the
// width-1 "·" fallback in statusGlyph — never an overflow.
var statusGlyphs = map[string]string{
	"open":         "○",
	"in_progress":  "◐",
	"blocked":      "●",
	"deferred":     "❄",
	"closed":       "✓",
	"needs_review": "◇",
}

var typeGlyphs = map[string]string{
	"epic":     "◆",
	"feature":  "+",
	"bug":      "!",
	"task":     "·",
	"chore":    "~",
	"decision": "?",
}

func statusGlyph(s string) string {
	if g, ok := statusGlyphs[s]; ok {
		return g
	}
	return "·"
}

func typeGlyph(t string) string {
	if g, ok := typeGlyphs[t]; ok {
		return g
	}
	return " "
}

// P0 red, P1 yellow, P2 default text, P3+ muted. P2 was ANSI "7" (white,
// invisible on a light terminal) and P3+ ANSI "8" (dark gray, invisible on a
// dark terminal); both are now adaptive so they read on either background.
var priorityColors = []lipgloss.TerminalColor{
	lipgloss.Color("1"), // P0 red
	lipgloss.Color("3"), // P1 yellow
	colText,             // P2 default text
	colDim,              // P3 muted
	colDim,              // P4 muted
}

func priorityColor(p int) lipgloss.TerminalColor {
	if p >= 0 && p < len(priorityColors) {
		return priorityColors[p]
	}
	return colDim
}

// Styles are package-level so View never allocates them per frame; stable
// styling is also what keeps line content identical across frames for the
// renderer's line diff.
var (
	styHeader      = lipgloss.NewStyle().Bold(true).Foreground(accent)
	styDim         = lipgloss.NewStyle().Foreground(colDim)
	styColActive   = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(accent).Padding(0, 1)
	styColInactive = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colBorderOff).Padding(0, 1)
	styColTitleOn  = lipgloss.NewStyle().Bold(true).Foreground(accent)
	styColTitleOff = lipgloss.NewStyle().Bold(true)
	styDetailBox   = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(accent).Padding(0, 1)
	styDetailTitle = lipgloss.NewStyle().Bold(true).Foreground(accent)
	styBold        = lipgloss.NewStyle().Bold(true)
	styError       = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))

	// Focused-card variants: reverse video across every segment.
	styDimF   = lipgloss.NewStyle().Foreground(colDim).Reverse(true)
	styPlain  = lipgloss.NewStyle()
	styPlainF = lipgloss.NewStyle().Reverse(true)

	// Section headers for the full-width sectioned list.
	stySection      = lipgloss.NewStyle().Bold(true)
	stySectionCount = lipgloss.NewStyle().Foreground(colDim)
	// Tab strip for the detail/preview sub-views.
	styTabOn  = lipgloss.NewStyle().Bold(true).Foreground(accent).Underline(true)
	styTabOff = lipgloss.NewStyle().Foreground(colDim)
	// Focus is a left-edge accent bar in a fixed gutter — NOT reverse-video
	// (which fought the per-cell colors). All cell colors render normally on
	// the focused row too; only the bar and a bold title mark it.
	styFocusBar = lipgloss.NewStyle().Foreground(accent).Bold(true)
	// Blocked/blocking flags: blocked ↑ is the alarm (bright bold red);
	// blocking ↓ is orange (distinct from P1 yellow and the status colors).
	styBlocked  = lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Bold(true)
	styBlocking = lipgloss.NewStyle().Foreground(lipgloss.Color("208")).Bold(true)

	// Status-age cold ramp (age mode ageStatus). styAgeWarm is amber (past
	// ageWarmAfter); styAgeCold is loud bold red (past ageColdAfter) so a stale
	// bead's age reads at a glance. Fresh ages stay dim (styDim), like activity mode.
	styAgeWarm = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	styAgeCold = lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Bold(true)
)

var (
	styPriority  = make([]lipgloss.Style, 5)
	styPriorityF = make([]lipgloss.Style, 5)
)

func init() {
	for p := 0; p < 5; p++ {
		styPriority[p] = lipgloss.NewStyle().Foreground(priorityColor(p))
		styPriorityF[p] = lipgloss.NewStyle().Foreground(priorityColor(p)).Reverse(true)
	}
	// Status colors: open muted, wip cyan, blocked red, done green, defer muted,
	// review magenta. Every color is adaptive (or a theme-mapped ANSI hue) so the status word
	// reads on both backgrounds; the old Faint green/gray for done/defer went
	// invisible on dark, so faint is gone.
	specs := map[string]lipgloss.TerminalColor{
		"open":         colDim,
		"in_progress":  lipgloss.Color("6"), // cyan
		"blocked":      lipgloss.Color("1"), // red
		"closed":       colDone,             // green
		"deferred":     colDim,
		"needs_review": lipgloss.Color("5"), // magenta — the review fence, distinct from every other status hue
	}
	for st, c := range specs {
		s := lipgloss.NewStyle().Foreground(c)
		statusStyles[st] = s
		statusStylesF[st] = s.Reverse(true)
	}
}

func prioStyle(p int, focused bool) lipgloss.Style {
	if p < 0 || p >= 5 {
		p = 4
	}
	if focused {
		return styPriorityF[p]
	}
	return styPriority[p]
}

// --- theme state ---
//
// themeDark records the palette's current background assumption. It is the ONE
// source of truth for both the lipgloss AdaptiveColor resolution (mirrored into
// lipgloss.SetHasDarkBackground, which every AdaptiveColor reads at render time)
// and the glamour standard-style name. It is fixed ONCE before the Program owns
// stdin (SetInitialTheme, from main) and flipped only by the in-app toggle
// (toggleTheme, T) — never from a live termenv probe, which mid-session
// re-triggers the OSC-query deadlock that froze the detail view.
var themeDark = true

// SetInitialTheme fixes the palette background from the glamour style already
// resolved in main() (before the bubbletea Program starts reading stdin). It
// marks the background EXPLICIT on lipgloss, so no AdaptiveColor render ever
// probes termenv again — closing the deadlock hole on the BB_THEME
// override path, which previously never told lipgloss the background at all.
func SetInitialTheme(glamourStyle string) {
	themeDark = glamourStyle != "light" // notty/ascii/dark → dark palette
	lipgloss.SetHasDarkBackground(themeDark)
}

// glamourStyleFor maps the theme to glamour's explicit standard-style name. It
// never probes — WithStandardStyle takes the name verbatim (only WithAutoStyle
// probes, which is what we must avoid).
func glamourStyleFor(dark bool) string {
	if dark {
		return "dark"
	}
	return "light"
}

// themeName is the human-facing label for the current theme.
func themeName() string {
	if themeDark {
		return "dark"
	}
	return "light"
}

// toggleTheme flips the palette background (dark↔light) and rebuilds the glamour
// renderer from the NEW explicit style — no termenv probe, so no deadlock. It is
// the recovery when auto-detect was wrong or the terminal changed theme
// mid-session: one keypress re-contrasts every color and the markdown body.
func (m *Model) toggleTheme() {
	themeDark = !themeDark
	lipgloss.SetHasDarkBackground(themeDark) // AdaptiveColors re-resolve on next render
	m.md.setStyle(glamourStyleFor(themeDark))
	m.panelMD.id = ""    // drop the cached glamour preview body
	m.setDetailContent() // re-render the open detail with the rebuilt renderer
}
