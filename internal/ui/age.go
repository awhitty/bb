package ui

import (
	"path/filepath"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/awhitty/bb/internal/bd"
	"github.com/awhitty/bb/internal/journal"
)

// age.go owns the right-hand age column's TWO meanings and the cold ramp that
// makes stale work pop.
//
//   - ageActivity (the default) is updated_at — "last touched". Any comment or
//     edit resets it, so it answers "is anyone poking this", never "how long has
//     it sat". It renders dim and never ramps — byte-identical to the column
//     before this feature.
//   - ageStatus is time-IN-STATUS: now − the bead's last status transition
//     (from the interactions journal), falling back to created_at for a bead
//     that never transitioned. It ramps dim → amber → red as the bead sits cold,
//     but only for open work (statusRamps) — a closed or deferred bead's age is
//     not a coldness signal.
//
// `t` cycles the two (input.go). The map of last transitions is parsed once per
// data refresh in the async load (loadIssues) and carried on the Model, never
// re-read per frame.

// ageMode selects what the age column means. The zero value is ageActivity, so
// a fresh Model preserves the pre-feature behavior with no initialization.
type ageMode int

const (
	ageActivity ageMode = iota // updated_at — last touched
	ageStatus                  // time in the current status
)

// interactionsPath is the journal location under a bd workspace.
func interactionsPath(workspace string) string {
	return filepath.Join(workspace, ".beads", "interactions.jsonl")
}

// statusRamps reports whether a bead's status is one where sitting cold is
// meaningful — open work waiting to move. Closed, deferred, and blocked beads
// never ramp (a blocked bead is cold by definition; a closed one is done).
func statusRamps(status string) bool {
	switch status {
	case bd.StatusOpen, bd.StatusInProgress, "needs_review":
		return true
	default:
		return false
	}
}

// statusAgeTime is the bead's time-in-status anchor: its last status-transition
// timestamp, or created_at when it never transitioned. Zero when neither parses
// (the cell then renders blank).
func statusAgeTime(is bd.Issue, since map[string]time.Time) time.Time {
	if t, ok := since[is.ID]; ok {
		return t
	}
	if t, err := time.Parse(time.RFC3339, is.CreatedAt); err == nil {
		return t
	}
	return time.Time{}
}

// ageRampStyle picks the age cell's color from how long a rampable bead has sat.
// Only ageStatus mode on a rampable status ramps; everything else stays dim.
func ageRampStyle(status string, age time.Duration, mode ageMode) lipgloss.Style {
	if mode != ageStatus || !statusRamps(status) {
		return styDim
	}
	switch {
	case age >= ageColdAfter:
		return styAgeCold
	case age >= ageWarmAfter:
		return styAgeWarm
	default:
		return styDim
	}
}

// ageCellFor renders one issue's fixed-width age cell for the given mode. Width
// is always ageColW (colors never change width), so the column stays aligned in
// every layout and toggling the mode never reflows a row.
func ageCellFor(is bd.Issue, now time.Time, mode ageMode, since map[string]time.Time) string {
	if mode != ageStatus {
		return ageCell(is.UpdatedAt, now) // dim updated_at — identical to before
	}
	t := statusAgeTime(is, since)
	if t.IsZero() {
		return styDim.Width(ageColW).Align(lipgloss.Right).Render("")
	}
	d := now.Sub(t)
	sty := ageRampStyle(is.Status, d, mode)
	return sty.Width(ageColW).Align(lipgloss.Right).Render(compactDur(d))
}

// ageCellFn builds the per-frame age renderer threaded through the row
// formatters, binding now + the active mode + the parsed transition map.
func (m Model) ageCellFn(now time.Time) ageFn {
	return func(is bd.Issue) string {
		return ageCellFor(is, now, m.ageMode, m.statusSince)
	}
}

// toggleAgeMode cycles the age column between last-activity and time-in-status.
// A footer line names the new meaning so the toggle is discoverable.
func (m *Model) toggleAgeMode() {
	if m.ageMode == ageStatus {
		m.ageMode = ageActivity
		m.setMessage("age: last activity (t to switch to time-in-status)", false)
	} else {
		m.ageMode = ageStatus
		m.setMessage("age: time in status (t to switch to last activity)", false)
	}
}

// loadStatusAges parses the interactions journal for the workspace and returns
// id → last status-transition time. Best-effort: a missing/unreadable journal
// yields an empty map, so the age column falls back to created_at everywhere.
func loadStatusAges() map[string]time.Time {
	return journal.LastStatusChange(interactionsPath(bd.Workspace()))
}
