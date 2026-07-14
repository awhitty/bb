package hook

import (
	"strings"
	"testing"
)

// A turn naming two beads far apart yields two excerpts, each carrying the prose
// around its mention with the id span marked (and slicing that span back out of
// the excerpt text returns the short id body).
func TestExtractExcerptsSpans(t *testing.T) {
	board := []string{"demo-pqr.4", "demo-abc"}
	text := "I closed demo-pqr.4 because the client channel work landed. " +
		strings.Repeat("filler word ", 40) +
		"Next I will pick up demo-abc for the onboarding flow."
	exs := ExtractExcerpts(text, board, "demo-")
	if len(exs) != 2 {
		t.Fatalf("want 2 excerpts, got %d: %+v", len(exs), exs)
	}
	for _, ex := range exs {
		if len(ex.Mentions) != 1 {
			t.Fatalf("excerpt should mark exactly one mention: %+v", ex)
		}
		mn := ex.Mentions[0]
		if mn.Start < 0 || mn.End > len(ex.Text) || mn.Start >= mn.End {
			t.Fatalf("mention span out of range: %+v in %q", mn, ex.Text)
		}
		got := ex.Text[mn.Start:mn.End]
		if got != mn.ID {
			t.Fatalf("span sliced %q, want the id %q (text=%q)", got, mn.ID, ex.Text)
		}
	}
	if !strings.Contains(exs[0].Text, "client channel") {
		t.Fatalf("first excerpt lost its context: %q", exs[0].Text)
	}
	if !strings.Contains(exs[1].Text, "onboarding flow") {
		t.Fatalf("second excerpt lost its context: %q", exs[1].Text)
	}
}

// Two beads in the SAME sentence share one excerpt, both marked, spans exact.
func TestExtractExcerptsGroupsNearby(t *testing.T) {
	board := []string{"demo-pqr.4", "demo-abc"}
	text := "Both demo-pqr.4 and demo-abc block the release."
	exs := ExtractExcerpts(text, board, "demo-")
	if len(exs) != 1 {
		t.Fatalf("nearby mentions should be one excerpt, got %d", len(exs))
	}
	if len(exs[0].Mentions) != 2 {
		t.Fatalf("want 2 mentions in the excerpt, got %d", len(exs[0].Mentions))
	}
	for _, mn := range exs[0].Mentions {
		if exs[0].Text[mn.Start:mn.End] != mn.ID {
			t.Fatalf("span mismatch for %s in %q", mn.ID, exs[0].Text)
		}
	}
	// No leading ellipsis (the sentence is short — the whole thing is the window).
	if strings.HasPrefix(exs[0].Text, "…") {
		t.Fatalf("short whole-text excerpt should not be elided: %q", exs[0].Text)
	}
}

// Whitespace (newlines, runs) collapses but mention spans still land on the id.
func TestExtractExcerptsNormalizesWhitespace(t *testing.T) {
	board := []string{"demo-pqr.4"}
	text := "line one\n\n  working on   demo-pqr.4   now\nline three"
	exs := ExtractExcerpts(text, board, "demo-")
	if len(exs) != 1 || len(exs[0].Mentions) != 1 {
		t.Fatalf("want 1 excerpt/1 mention, got %+v", exs)
	}
	if strings.Contains(exs[0].Text, "\n") || strings.Contains(exs[0].Text, "  ") {
		t.Fatalf("excerpt text not collapsed: %q", exs[0].Text)
	}
	mn := exs[0].Mentions[0]
	if exs[0].Text[mn.Start:mn.End] != "demo-pqr.4" {
		t.Fatalf("span wrong after collapse: %q from %q", exs[0].Text[mn.Start:mn.End], exs[0].Text)
	}
}

func TestExtractExcerptsNoMentions(t *testing.T) {
	if got := ExtractExcerpts("nothing here about demo-based work", []string{"demo-pqr"}, "demo-"); got != nil {
		t.Fatalf("want nil, got %+v", got)
	}
}

func TestConvoNameFirstUserPrompt(t *testing.T) {
	transcript := `{"type":"user","message":{"role":"user","content":"<command-name>/model</command-name>"},"cwd":"/Users/example/work/project"}
{"type":"user","message":{"role":"user","content":"Rework the mentioned entries so I can see context and source"}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"ok"}]}}`
	got := ConvoName([]byte(transcript), "804f231e-f820-4c94")
	want := "Rework the mentioned entries so I can see context and source"
	if got != want {
		t.Fatalf("convo name = %q, want %q", got, want)
	}
}

func TestConvoNameTruncates(t *testing.T) {
	long := "Please help me redesign the whole agent shares browser end to end with a lot of detail and words"
	transcript := `{"type":"user","message":{"role":"user","content":"` + long + `"}}`
	got := ConvoName([]byte(transcript), "s")
	if !strings.HasSuffix(got, "…") || len([]rune(got)) > convoNameLen+1 {
		t.Fatalf("expected a truncated name, got %q (len %d)", got, len([]rune(got)))
	}
}

// When there is no usable prose (only slash commands), fall back to the project
// basename + a short session id.
func TestConvoNameFallsBackToProjectAndSession(t *testing.T) {
	transcript := `{"type":"user","message":{"role":"user","content":"<command-name>/clear</command-name>"},"cwd":"/Users/example/work/project"}`
	got := ConvoName([]byte(transcript), "804f231e-f820-4c94-b676")
	if got != "project · 804f231e" {
		t.Fatalf("fallback name = %q, want %q", got, "project · 804f231e")
	}
}

func TestConvoNameFallsBackToSessionAlone(t *testing.T) {
	got := ConvoName([]byte(""), "abc1234")
	if got != "session abc1234" {
		t.Fatalf("bare fallback = %q, want %q", got, "session abc1234")
	}
}
