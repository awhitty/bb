package hook

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestLatestAssistantText(t *testing.T) {
	transcript := `{"type":"user","message":{"role":"user","content":"do demo-pqr.4"}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"first turn"}]}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"working on demo-pqr.4 now"}]}}
`
	got := LatestAssistantText([]byte(transcript))
	if got != "working on demo-pqr.4 now" {
		t.Fatalf("latest assistant text = %q", got)
	}
}

func TestLatestAssistantTextStringContent(t *testing.T) {
	transcript := `{"role":"assistant","message":{"role":"assistant","content":"bare string content demo-abc"}}`
	if got := LatestAssistantText([]byte(transcript)); got != "bare string content demo-abc" {
		t.Fatalf("string-content text = %q", got)
	}
}

func TestExtractBeadIDs(t *testing.T) {
	board := []string{"demo-pqr", "demo-pqr.4", "demo-abc", "demo-xyz.6.4"}
	text := "I'll close demo-pqr.4 and look at abc, plus xyz.6.4. Ignore demo-based tooling and demo-zzz (not real). Also pqr is the epic."
	got := ExtractBeadIDs(text, board, "demo-")
	want := []string{"demo-abc", "demo-pqr", "demo-pqr.4", "demo-xyz.6.4"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("extracted = %v, want %v", got, want)
	}
}

// TestExtractBeadIDsBareShorthand covers the real-world case (demo-rst.15):
// conversations name-drop bare shorthand far more than the fully-qualified id.
// A bare candidate counts iff prefix+candidate is a real board id; the existence
// check — not a regex char-class — is the false-positive guard.
func TestExtractBeadIDsBareShorthand(t *testing.T) {
	board := []string{"demo-rst.5", "demo-def", "demo-pqr.14", "demo-uvw.1.5"}
	text := "rst.5, demo-def, pqr.14, version 3.5, port 8813, uvw.1.5"
	got := ExtractBeadIDs(text, board, "demo-")
	want := []string{"demo-def", "demo-pqr.14", "demo-rst.5", "demo-uvw.1.5"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("extracted = %v, want %v", got, want)
	}
}

// TestExtractBeadIDsBareUnknownDropped: a shape-matching bare token whose
// qualified form is NOT on the board is dropped (existence is the guard).
func TestExtractBeadIDsBareUnknownDropped(t *testing.T) {
	board := []string{"demo-rst.5"}
	// "9zz" and "abc" are id-shaped but not real; "3.5"/"v1.2" are version-ish.
	got := ExtractBeadIDs("touch 9zz and abc, bump to 3.5 or v1.2", board, "demo-")
	if len(got) != 0 {
		t.Fatalf("unknown bare tokens must be dropped, got %v", got)
	}
}

// TestExtractBeadIDsBareAllAlphaCore: a real core with no digits (e.g. an epic
// like demo-mnp) must register from its bare mention — the old digit-guard
// silently dropped these.
func TestExtractBeadIDsBareAllAlphaCore(t *testing.T) {
	board := []string{"demo-mnp", "demo-mnp.3.1"}
	got := ExtractBeadIDs("the mnp epic blocks mnp.3.1", board, "demo-")
	want := []string{"demo-mnp", "demo-mnp.3.1"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("all-alpha core: extracted = %v, want %v", got, want)
	}
}

// TestExtractBeadIDsCaseInsensitive: bare shorthand matches regardless of case
// (board ids are lowercase).
func TestExtractBeadIDsCaseInsensitive(t *testing.T) {
	board := []string{"demo-rst.5"}
	got := ExtractBeadIDs("closing RST.5 now", board, "demo-")
	want := []string{"demo-rst.5"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("case-insensitive: extracted = %v, want %v", got, want)
	}
}

func TestExtractBeadIDsRejectsWords(t *testing.T) {
	board := []string{"demo-pqr"}
	// "demo-based" must not match (body "based" has no digit/dot and prefix+word isn't real).
	got := ExtractBeadIDs("this is demo-based work about reporting", board, "demo-")
	if len(got) != 0 {
		t.Fatalf("plain words must not extract, got %v", got)
	}
}

func TestCommonPrefix(t *testing.T) {
	if p := CommonPrefix([]string{"demo-1", "demo-2.3"}); p != "demo-" {
		t.Fatalf("prefix = %q", p)
	}
	if p := CommonPrefix([]string{"demo-1", "other-2"}); p != "" {
		t.Fatalf("mixed prefixes must be empty, got %q", p)
	}
}

func TestInstallUninstallPreservesSettings(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	// Pre-existing settings with an unrelated key + an unrelated Stop hook.
	existing := `{"model":"opus","hooks":{"Stop":[{"hooks":[{"type":"command","command":"other-tool"}]}]}}`
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}

	changed, err := Install("/usr/local/bin/bb")
	if err != nil || !changed {
		t.Fatalf("install: changed=%v err=%v", changed, err)
	}
	if !Installed() {
		t.Fatal("Installed() should be true after install")
	}
	// Idempotent.
	if again, _ := Install("/usr/local/bin/bb"); again {
		t.Fatal("second install must be a no-op")
	}
	// The unrelated key and unrelated Stop hook survive.
	raw, _ := os.ReadFile(filepath.Join(dir, "settings.json"))
	s := string(raw)
	if !contains(s, `"model": "opus"`) || !contains(s, "other-tool") {
		t.Fatalf("install clobbered existing settings:\n%s", s)
	}

	// Both hooks land: the per-turn Stop (register/refresh) AND the once-per-
	// conversation SessionEnd (archive).
	if s := string(raw); !contains(s, HookMarker) || !contains(s, SessionEndMarker) || !contains(s, `"SessionEnd"`) {
		t.Fatalf("install must add both the Stop and SessionEnd hooks:\n%s", s)
	}

	changed, err = Uninstall()
	if err != nil || !changed {
		t.Fatalf("uninstall: changed=%v err=%v", changed, err)
	}
	if Installed() {
		t.Fatal("Installed() should be false after uninstall")
	}
	raw, _ = os.ReadFile(filepath.Join(dir, "settings.json"))
	if s := string(raw); !contains(s, "other-tool") || contains(s, HookMarker) || contains(s, SessionEndMarker) {
		t.Fatalf("uninstall must keep the unrelated hook and drop both of ours:\n%s", s)
	}
}

// TestInstallBackfillsSessionEnd covers upgrading from an older install that has
// only the Stop hook: a re-run adds the missing SessionEnd hook and reports a
// change, then is idempotent.
func TestInstallBackfillsSessionEnd(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	// An old install: only the Stop hook present.
	old := `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"/bin/bb hook-ingest"}]}]}}`
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	changed, err := Install("/bin/bb")
	if err != nil || !changed {
		t.Fatalf("backfill install: changed=%v err=%v", changed, err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "settings.json"))
	if s := string(raw); !contains(s, SessionEndMarker) || !contains(s, `"SessionEnd"`) {
		t.Fatalf("re-install must backfill the SessionEnd hook:\n%s", s)
	}
	if again, _ := Install("/bin/bb"); again {
		t.Fatal("install after backfill must be a no-op")
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}
