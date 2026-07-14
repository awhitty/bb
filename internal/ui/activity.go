package ui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/awhitty/bb/internal/bd"
	"github.com/awhitty/bb/internal/dolt"
)

// The activity feed (key a): a global, reverse-chronological feed of what
// changed across the WHOLE board — created / closed / reprioritized /
// status-changed / edited — each line saying WHAT changed (before→after), when,
// and which bead. enter jumps to the bead. It runs on the same Dolt history
// layer as internal/dolt (the `events` audit table).

type activityMsg struct {
	src    *dolt.Source
	events []dolt.Event
	err    error
}

// enterActivity opens the feed, loading events lazily.
func (m *Model) enterActivity() tea.Cmd {
	m.activityView = true
	m.activityIdx = 0
	m.pushLayer(layerActivity) // esc drops the overlay back to the view beneath (layers.go)
	if m.activitySrc != nil && len(m.activityEvents) > 0 {
		return nil // already loaded this session
	}
	m.setMessage("loading activity…", false)
	src := m.activitySrc
	return func() tea.Msg {
		if src == nil {
			s, err := dolt.Connect(bd.Workspace())
			if err != nil {
				return activityMsg{err: err}
			}
			src = s
		}
		events, err := src.Events(300)
		return activityMsg{src: src, events: events, err: err}
	}
}

func (m Model) handleActivity(msg activityMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.activityErr = msg.err.Error()
		m.setMessage("activity unavailable: "+msg.err.Error(), true)
		return m, nil
	}
	m.activitySrc = msg.src
	m.activityEvents = msg.events
	m.activityErr = ""
	m.setMessage("", false)
	return m, nil
}

// --- rendering ---

func (m Model) viewActivity(bodyH int) string {
	w := m.boardWidth()
	if m.activityErr != "" {
		return lipgloss.Place(w, bodyH, lipgloss.Center, lipgloss.Center,
			styError.Render("activity unavailable — "+m.activityErr))
	}
	if len(m.activityEvents) == 0 {
		return lipgloss.Place(w, bodyH, lipgloss.Center, lipgloss.Center,
			styDim.Render("loading activity…"))
	}
	now := time.Now()
	events := m.activityEvents
	return m.renderNavList(bodyH, len(events), m.activityIdx, "\x00activity",
		func(i int, focused bool) string {
			return m.activityLine(events[i], focused, w, now)
		},
		func(hidden int) string {
			if hidden > 0 {
				return styDim.Render(fmt.Sprintf("↑ %d newer", hidden))
			}
			return styDim.Render("recent activity — enter jumps to the bead")
		},
		func(hidden int) string {
			if hidden > 0 {
				return styDim.Render(fmt.Sprintf("↓ %d older", hidden))
			}
			return ""
		})
}

const activityWhenW = 8 // "12mo ago" — the relative-time column

func (m Model) activityLine(e dolt.Event, focused bool, w int, now time.Time) string {
	verb, verbSty, delta := eventVerb(e)
	when := ""
	if !e.When.IsZero() {
		when = compactAge(e.When.UTC().Format(time.RFC3339), now)
	}
	whenCell := styDim.Width(activityWhenW).Align(lipgloss.Right).Render(when)
	verbCell := verbSty.Width(13).Render(verb)
	id := styDim.Render(padRight(m.shortID(e.IssueID), 9))
	title := e.Title
	if title == "" {
		title = styDim.Render("(deleted)")
	}
	if delta != "" {
		title += styDim.Render("  " + delta)
	}
	ind := relIndicator(m.relFlagsFor(m.byID[e.IssueID]))
	fixed := focusGutter + activityWhenW + 1 + 13 + 1 + 9 + 1 + relSlot + 1
	titleW := max(4, w-fixed)
	titleCellSty := styPlain
	if focused {
		titleCellSty = styBold
	}
	return gutter(focused) + whenCell + " " + verbCell + " " + id + " " + ind + " " +
		titleCellSty.Render(ansi.Truncate(title, titleW, "…"))
}

// eventVerb turns one event into a verb, its color, and a before→after delta.
func eventVerb(e dolt.Event) (verb string, sty lipgloss.Style, delta string) {
	switch {
	case e.Type == "created":
		return "created", statusStyle("in_progress", false), ""
	case e.Type == "closed" || strOf(e.New["status"]) == "closed":
		return "closed", statusStyle("closed", false), ""
	case has(e.New, "priority"):
		return "reprioritized", prioStyle(intFrom(e.New["priority"]), false),
			fmt.Sprintf("P%d → P%d", intFrom(e.Old["priority"]), intFrom(e.New["priority"]))
	case has(e.New, "status"):
		return "status", statusStyle(strOf(e.New["status"]), false),
			statusWord(strOf(e.Old["status"])) + " → " + statusWord(strOf(e.New["status"]))
	case len(e.New) > 0:
		return "edited " + fieldList(e.New), styDim, ""
	default:
		v := e.Type
		if v == "" {
			v = "changed"
		}
		return v, styDim, ""
	}
}

// --- helpers ---

func has(m map[string]any, k string) bool { _, ok := m[k]; return ok }

func strOf(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func intFrom(v any) int {
	switch t := v.(type) {
	case float64:
		return int(t)
	case int:
		return t
	}
	return 0
}

// fieldList names up to two changed fields (title/description/notes/…).
func fieldList(m map[string]any) string {
	var ks []string
	for k := range m {
		if k == "id" || k == "updated_at" || k == "content_hash" {
			continue
		}
		ks = append(ks, k)
		if len(ks) == 2 {
			break
		}
	}
	return strings.Join(ks, "/")
}

// activityFocusID is the bead the cursor is on in the feed.
func (m Model) activityFocusID() string {
	if m.activityIdx >= 0 && m.activityIdx < len(m.activityEvents) {
		return m.activityEvents[m.activityIdx].IssueID
	}
	return ""
}
