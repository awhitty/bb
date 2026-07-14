package nlq

import (
	"io"
	"os"
	"testing"

	"github.com/awhitty/bb/internal/bd"
	"github.com/awhitty/bb/internal/discover"
	"github.com/charmbracelet/log"
)

// TestLiveNLQBench compiles a fixed ask set two ways against the LIVE model
// server and the real board (read-only): plain single-shot vs grounded +
// execution-feedback repair. Skipped unless BB_LIVE_BENCH=1; point
// BB_WORKSPACE at the board. It mutates nothing — only bd list/query.
func TestLiveNLQBench(t *testing.T) {
	if os.Getenv("BB_LIVE_BENCH") == "" {
		t.Skip("set BB_LIVE_BENCH=1 (needs a model server + BB_WORKSPACE)")
	}
	r := discover.Resolve(log.New(io.Discard))
	if r.Err != "" {
		t.Fatalf("no model server: %s", r.Err)
	}
	p := &Provider{Model: r.Compiler.Model, URL: r.Compiler.URL, Key: r.Compiler.Key, Label: LabelFor(r.Compiler.Model)}
	client := bd.NewClient(bd.SpawnRunner(bd.Workspace()))
	issues, err := client.List(false)
	if err != nil {
		t.Fatal(err)
	}
	v := DeriveVocab(issues)
	exec := func(q string) (int, []string, error) {
		is, err := client.Query(q)
		if err != nil {
			return 0, nil, err
		}
		s := []string{}
		for i := 0; i < len(is) && i < 3; i++ {
			s = append(s, is[i].ID+" "+is[i].Title)
		}
		return len(is), s, nil
	}

	asks := []string{
		"reporting stuff",
		"what has alex shipped this week",
		"stuff about the onboarding flow",
		"urgent export problems",
	}
	repaired := 0
	for _, nl := range asks {
		before, berr := p.Compile(nl, v, nil)
		bcount := -1
		if berr == nil {
			if c, _, e := exec(before.Query); e == nil {
				bcount = c
			}
		}
		after, aerr := p.CompileWithRepair(nl, v, LexicalGrounder{}, exec)
		if after.Repairs() > 0 {
			repaired++
		}
		t.Logf("\nASK: %s\n  grounding: %v\n  BEFORE: %q  (%d rows, err=%v)\n  AFTER:  %q  (%d rows, repairs=%d, err=%v)",
			nl, LexicalGrounder{}.Ground(nl, v), before.Query, bcount, berr,
			after.Query, after.Count, after.Repairs(), aerr)
	}
	t.Logf("\nrepair rate: %d/%d asks triggered a revision", repaired, len(asks))
}
