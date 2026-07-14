package nlq

import (
	"reflect"
	"strings"
	"testing"

	"github.com/awhitty/bb/internal/bd"
)

func TestBoardContextStableAndCompact(t *testing.T) {
	issues := []bd.Issue{
		{ID: "g-2", Title: "Second", Status: "open", Priority: 2, IssueType: "bug",
			Description: "line one\nline two   with   spaces\n" + strings.Repeat("x", 200)},
		{ID: "g-1", Title: "First", Status: "in_progress", Priority: 0, IssueType: "task",
			Labels: []string{"now", "critical-path"}, Parent: "g-0",
			Dependencies: []bd.Dependency{
				{IssueID: "g-1", DependsOnID: "g-0", Type: "parent-child"},
				{IssueID: "g-1", DependsOnID: "g-9", Type: "blocks"}}},
	}
	a := BoardContext(issues)
	// Stable: same input in a different order renders byte-identically.
	b := BoardContext([]bd.Issue{issues[1], issues[0]})
	if a != b {
		t.Fatal("BoardContext must be byte-identical regardless of input order (prefix cache)")
	}
	lines := strings.Split(a, "\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[0], "g-1 | ") {
		t.Fatalf("ordering wrong: %v", lines)
	}
	if !strings.Contains(lines[0], "task | P0 | in_progress | now,critical-path | parent=g-0 | dep→g-9 | First") {
		t.Fatalf("g-1 line = %q", lines[0])
	}
	if strings.Contains(lines[0], "g-0,g-9") {
		t.Fatal("parent-child edges must not appear as deps")
	}
	// Newlines flattened, description capped ~120 runes.
	if strings.Contains(lines[1], "\n") || strings.Contains(lines[1], "line one line two with spaces xxxx") == false {
		t.Fatalf("g-2 desc not flattened: %q", lines[1])
	}
	if got := len([]rune(lines[1])); got > 220 {
		t.Fatalf("g-2 line too long: %d runes", got)
	}
	// Prefix embeds the board after the instructions.
	if !strings.HasSuffix(AnalystPrefix(a), a) || !strings.Contains(AnalystPrefix(a), "```ids") {
		t.Fatal("AnalystPrefix must end with the board and teach the ids block")
	}
}

func TestParseIDs(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"basic", "Blah demo-1 blah.\n```ids\ndemo-1 demo-2\n```", []string{"demo-1", "demo-2"}},
		{"multiline and commas", "```ids\ndemo-1, demo-2\ndemo-3\n```", []string{"demo-1", "demo-2", "demo-3"}},
		{"last block wins", "```ids\ndemo-0\n```\nmore prose\n```ids\ndemo-9\n```", []string{"demo-9"}},
		{"dedup", "```ids\ndemo-1 demo-1\n```", []string{"demo-1"}},
		{"empty block", "nothing matches\n```ids\n```", nil},
		{"no block", "just prose, mentioning demo-1", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ParseIDs(tc.in); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("ParseIDs = %v, want %v", got, tc.want)
			}
		})
	}
}
