package ui

import (
	"testing"

	"github.com/charmbracelet/lipgloss"
)

// TestAdaptivePaletteDistinct asserts every theme-sensitive color carries a
// DISTINCT light/dark pair, so no shade is stuck at one background. The old
// palette hardcoded ANSI "8" (dark gray, invisible on dark) and "7" (white,
// invisible on light); an adaptive pair with equal ends would reintroduce that.
func TestAdaptivePaletteDistinct(t *testing.T) {
	cases := []struct {
		name string
		c    lipgloss.AdaptiveColor
	}{
		{"colText", colText},
		{"colDim", colDim},
		{"colBorderOff", colBorderOff},
		{"colDone", colDone},
	}
	for _, tc := range cases {
		if tc.c.Light == "" || tc.c.Dark == "" {
			t.Errorf("%s: empty shade (Light=%q Dark=%q)", tc.name, tc.c.Light, tc.c.Dark)
		}
		if tc.c.Light == tc.c.Dark {
			t.Errorf("%s: light and dark are identical (%q) — not adaptive", tc.name, tc.c.Light)
		}
	}
}

// TestSetInitialThemeExplicit asserts the resolved glamour style drives the
// lipgloss adaptive background EXPLICITLY (light→light background, everything
// else→dark). Because it is set explicitly, no later AdaptiveColor render probes
// termenv — the OSC probe that deadlocks a running bubbletea Program.
func TestSetInitialThemeExplicit(t *testing.T) {
	t.Cleanup(func() { lipgloss.SetHasDarkBackground(true) }) // restore app default

	cases := []struct {
		style    string
		wantDark bool
	}{
		{"light", false},
		{"dark", true},
		{"notty", true},
		{"ascii", true},
	}
	for _, tc := range cases {
		SetInitialTheme(tc.style)
		if got := lipgloss.HasDarkBackground(); got != tc.wantDark {
			t.Errorf("SetInitialTheme(%q): HasDarkBackground=%v want %v", tc.style, got, tc.wantDark)
		}
		if themeDark != tc.wantDark {
			t.Errorf("SetInitialTheme(%q): themeDark=%v want %v", tc.style, themeDark, tc.wantDark)
		}
	}
}

func TestGlamourStyleFor(t *testing.T) {
	if s := glamourStyleFor(true); s != "dark" {
		t.Errorf("glamourStyleFor(true)=%q want dark", s)
	}
	if s := glamourStyleFor(false); s != "light" {
		t.Errorf("glamourStyleFor(false)=%q want light", s)
	}
}

// TestGlamourRebuildFromExplicitStyle asserts the cached renderer rebuilds from
// a NEW explicit style on setStyle (dark↔light on a theme toggle) without a
// termenv probe: setStyle only swaps the standard-style NAME and drops the
// cached TermRenderer, and render rebuilds it via WithStandardStyle (never
// WithAutoStyle, the probing/deadlocking path). Both styles must render.
func TestGlamourRebuildFromExplicitStyle(t *testing.T) {
	r := newMdRenderer("light")
	// Prose takes the reflow path, so the reflow renderer (rFlow) is the one
	// built here; the assertions track it.
	if out := r.render("# Title\n\nSome **body** text.", 40); out == "" {
		t.Fatal("light render produced empty output")
	}
	if r.rFlow == nil {
		t.Fatal("render did not build the TermRenderer")
	}

	r.setStyle("dark")
	if r.style != "dark" {
		t.Fatalf("setStyle: style=%q want dark", r.style)
	}
	if r.rFlow != nil {
		t.Fatal("setStyle must drop the cached renderer so the next render rebuilds from the new style")
	}
	if out := r.render("# Title\n\nSome **body** text.", 40); out == "" {
		t.Fatal("dark render produced empty output")
	}
	if r.rFlow == nil {
		t.Fatal("dark render did not rebuild the TermRenderer")
	}

	// Same style is a no-op: the cached renderer survives.
	cached := r.rFlow
	r.setStyle("dark")
	if r.rFlow != cached {
		t.Error("setStyle to the same style rebuilt unnecessarily")
	}
}
