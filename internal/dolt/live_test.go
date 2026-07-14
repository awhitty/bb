package dolt

import (
	"os"
	"testing"
)

// Gated live test against the real Dolt server. Skipped unless
// BEADS_DOLT_LIVE=1; point BB_WORKSPACE at a bd workspace. Read-only.
func TestLiveAsOf(t *testing.T) {
	ws := os.Getenv("BB_WORKSPACE")
	if os.Getenv("BEADS_DOLT_LIVE") == "" || ws == "" {
		t.Skip("set BEADS_DOLT_LIVE=1 + BB_WORKSPACE")
	}
	src, err := Connect(ws)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	b, err := src.Boundaries()
	if err != nil || len(b) == 0 {
		t.Fatalf("boundaries: %d err=%v", len(b), err)
	}
	t.Logf("boundaries: %d (newest %s '%.40s')", len(b), b[0].Date.Format("15:04:05"), b[0].Message)
	// oldest boundary should have fewer/zero-ish issues; newest should have more
	oldest, err := src.IssuesAsOf(b[len(b)-1].Hash)
	if err != nil {
		t.Fatal(err)
	}
	newest, err := src.IssuesAsOf(b[0].Hash)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("AS OF oldest=%d issues, newest=%d issues", len(oldest), len(newest))
	if len(newest) == 0 {
		t.Fatal("newest snapshot empty")
	}
	// verify parent + labels wiring on the newest
	var withParent, withLabels int
	for _, is := range newest {
		if is.Parent != "" {
			withParent++
		}
		if len(is.Labels) > 0 {
			withLabels++
		}
	}
	if withParent == 0 {
		t.Fatal("no parent links reconstructed")
	}
	t.Logf("newest: %d with parent, %d with labels", withParent, withLabels)
}
