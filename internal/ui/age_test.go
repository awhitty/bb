package ui

import (
	"testing"
	"time"

	"github.com/awhitty/bb/internal/bd"
	"github.com/charmbracelet/x/ansi"
)

// statusAgeTime prefers the journal's last-transition time and falls back to
// created_at when the bead never transitioned.
func TestStatusAgeTimeFallback(t *testing.T) {
	trans := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	since := map[string]time.Time{"has-transition": trans}

	is := bd.Issue{ID: "has-transition", CreatedAt: "2026-07-01T00:00:00Z"}
	if got := statusAgeTime(is, since); !got.Equal(trans) {
		t.Errorf("with a transition row: got %v, want the transition time %v", got, trans)
	}

	fresh := bd.Issue{ID: "never-moved", CreatedAt: "2026-07-05T08:00:00Z"}
	want := time.Date(2026, 7, 5, 8, 0, 0, 0, time.UTC)
	if got := statusAgeTime(fresh, since); !got.Equal(want) {
		t.Errorf("no transition: got %v, want created_at %v", got, want)
	}

	none := bd.Issue{ID: "nothing"}
	if got := statusAgeTime(none, since); !got.IsZero() {
		t.Errorf("no transition and no created_at: got %v, want zero", got)
	}
}

// The cold ramp crosses at ~2d (amber) and ~5d (red), and ONLY in status mode
// on a rampable status. Activity mode, closed/deferred beads, and fresh ages
// stay dim.
func TestAgeRampStyle(t *testing.T) {
	cases := []struct {
		name   string
		status string
		age    time.Duration
		mode   ageMode
		want   string // "dim" | "warm" | "cold"
	}{
		{"fresh open", bd.StatusInProgress, 6 * time.Hour, ageStatus, "dim"},
		{"just under warm", bd.StatusOpen, ageWarmAfter - time.Minute, ageStatus, "dim"},
		{"at warm", bd.StatusOpen, ageWarmAfter, ageStatus, "warm"},
		{"between", "needs_review", 3 * 24 * time.Hour, ageStatus, "warm"},
		{"at cold", bd.StatusInProgress, ageColdAfter, ageStatus, "cold"},
		{"well past cold", bd.StatusOpen, 30 * 24 * time.Hour, ageStatus, "cold"},
		{"closed never ramps", bd.StatusClosed, 30 * 24 * time.Hour, ageStatus, "dim"},
		{"deferred never ramps", bd.StatusDeferred, 30 * 24 * time.Hour, ageStatus, "dim"},
		{"blocked never ramps", bd.StatusBlocked, 30 * 24 * time.Hour, ageStatus, "dim"},
		{"activity mode never ramps", bd.StatusOpen, 30 * 24 * time.Hour, ageActivity, "dim"},
	}
	// Compare by foreground color (the test env strips ANSI, so rendered
	// strings collide): dim=colDim, warm=styAgeWarm, cold=styAgeCold.
	wantFg := map[string]any{
		"dim":  styDim.GetForeground(),
		"warm": styAgeWarm.GetForeground(),
		"cold": styAgeCold.GetForeground(),
	}
	for _, c := range cases {
		got := ageRampStyle(c.status, c.age, c.mode).GetForeground()
		if got != wantFg[c.want] {
			t.Errorf("%s: ramp fg = %v, want %s (%v)", c.name, got, c.want, wantFg[c.want])
		}
	}
}

// The age cell is always exactly ageColW wide, in every mode and whether or not
// a status-age anchor exists — so toggling the column never reflows a row.
func TestAgeCellWidthStable(t *testing.T) {
	now := time.Date(2026, 7, 11, 0, 0, 0, 0, time.UTC)
	since := map[string]time.Time{
		"cold": now.Add(-9 * 24 * time.Hour), // red
		"warm": now.Add(-3 * 24 * time.Hour), // amber
	}
	issues := []bd.Issue{
		{ID: "cold", Status: bd.StatusInProgress, UpdatedAt: "2026-07-10T00:00:00Z", CreatedAt: "2026-07-01T00:00:00Z"},
		{ID: "warm", Status: bd.StatusOpen, UpdatedAt: "2026-07-10T00:00:00Z", CreatedAt: "2026-07-01T00:00:00Z"},
		{ID: "no-anchor", Status: bd.StatusOpen}, // no since, no created_at → blank cell
	}
	for _, mode := range []ageMode{ageActivity, ageStatus} {
		for _, is := range issues {
			cell := ageCellFor(is, now, mode, since)
			if got := ansi.StringWidth(cell); got != ageColW {
				t.Errorf("width(ageCellFor %s mode=%d) = %d, want %d", is.ID, mode, got, ageColW)
			}
		}
	}
}

// The two modes read different anchors: activity shows updated_at, status shows
// time-in-status. A bead touched recently but cold in status must read fresh in
// activity mode and loud in status mode — the whole point of the toggle.
func TestAgeCellModesDiffer(t *testing.T) {
	now := time.Date(2026, 7, 11, 0, 0, 0, 0, time.UTC)
	is := bd.Issue{ID: "x", Status: bd.StatusInProgress, UpdatedAt: "2026-07-11T00:00:00Z"} // touched "now"
	since := map[string]time.Time{"x": now.Add(-8 * 24 * time.Hour)}                        // 8d in status

	activity := ansi.Strip(ageCellFor(is, now, ageActivity, since))
	status := ansi.Strip(ageCellFor(is, now, ageStatus, since))
	if got := trim(activity); got != "now" {
		t.Errorf("activity cell = %q, want now (updated_at is fresh)", got)
	}
	if got := trim(status); got != "8d" {
		t.Errorf("status cell = %q, want 8d (time-in-status)", got)
	}
}

func trim(s string) string {
	out := s
	for len(out) > 0 && out[0] == ' ' {
		out = out[1:]
	}
	for len(out) > 0 && out[len(out)-1] == ' ' {
		out = out[:len(out)-1]
	}
	return out
}
