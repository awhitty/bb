package nlq

import (
	"fmt"
	"sort"
	"strings"

	"github.com/awhitty/bb/internal/bd"
)

// Vocab is the workspace's real query vocabulary, derived from the already-
// loaded issue list (never an extra bd call). Injected into the compile
// prompt so nuanced asks ground to values that actually match — e.g.
// assignee is EXACT-match in bd, so "alex" must compile to the stored
// "Alex Rivera", quoted.
type Vocab struct {
	IDPrefix  string
	Types     []string // in use, most common first
	Labels    []string // rendered as name(count), most common first
	Assignees []string // distinct, EXACTLY as stored
	Owners    []string // distinct, EXACTLY as stored
	Epics     []string // "<id> <title>" for top-level parents, most children first
}

const (
	maxLabels     = 20
	maxEpics      = 30
	maxPeople     = 10
	maxVocabLines = 40
)

type countedKey struct {
	key   string
	count int
}

func byCountThenKey(m map[string]int) []countedKey {
	out := make([]countedKey, 0, len(m))
	for k, c := range m {
		out = append(out, countedKey{k, c})
	}
	sort.Slice(out, func(a, b int) bool {
		if out[a].count != out[b].count {
			return out[a].count > out[b].count
		}
		return out[a].key < out[b].key
	})
	return out
}

// DeriveVocab scans the board once.
func DeriveVocab(issues []bd.Issue) Vocab {
	var v Vocab
	labelCounts := map[string]int{}
	typeCounts := map[string]int{}
	prefixCounts := map[string]int{}
	assignees := map[string]bool{}
	owners := map[string]bool{}
	childCounts := map[string]int{}
	byID := map[string]bd.Issue{}

	for _, is := range issues {
		byID[is.ID] = is
		for _, l := range is.Labels {
			labelCounts[l]++
		}
		if is.IssueType != "" {
			typeCounts[is.IssueType]++
		}
		if is.Assignee != "" {
			assignees[is.Assignee] = true
		}
		if is.Owner != "" {
			owners[is.Owner] = true
		}
		if i := strings.Index(is.ID, "-"); i > 0 {
			prefixCounts[is.ID[:i+1]]++
		}
		if is.Parent != "" {
			childCounts[is.Parent]++
		}
	}

	if p := byCountThenKey(prefixCounts); len(p) > 0 {
		v.IDPrefix = p[0].key
	}
	for _, t := range byCountThenKey(typeCounts) {
		v.Types = append(v.Types, t.key)
	}
	for i, l := range byCountThenKey(labelCounts) {
		if i >= maxLabels {
			break
		}
		v.Labels = append(v.Labels, fmt.Sprintf("%s(%d)", l.key, l.count))
	}
	v.Assignees = sortedKeys(assignees, maxPeople)
	v.Owners = sortedKeys(owners, maxPeople)

	// Epics/roots: TOP-LEVEL parents (an issue with children and no parent of
	// its own), plus top-level epics even before they grow children.
	epicCounts := map[string]int{}
	for _, is := range issues {
		if is.Parent != "" || strings.Contains(is.ID, ".") {
			continue
		}
		if n := childCounts[is.ID]; n > 0 {
			epicCounts[is.ID] = n
		} else if is.IssueType == "epic" {
			epicCounts[is.ID] = 0
		}
	}
	for i, e := range byCountThenKey(epicCounts) {
		if i >= maxEpics {
			break
		}
		v.Epics = append(v.Epics, strings.TrimSpace(e.key+" "+byID[e.key].Title))
	}
	return v
}

func sortedKeys(set map[string]bool, cap int) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	if len(out) > cap {
		out = out[:cap]
	}
	return out
}

func quoteAll(vals []string) string {
	q := make([]string, len(vals))
	for i, v := range vals {
		q[i] = `"` + v + `"`
	}
	return strings.Join(q, ", ")
}

// chunk joins vals into comma-separated lines of at most per entries.
func chunk(vals []string, per int, indent string) []string {
	var lines []string
	for len(vals) > 0 {
		n := per
		if n > len(vals) {
			n = len(vals)
		}
		lines = append(lines, indent+strings.Join(vals[:n], ", "))
		vals = vals[n:]
	}
	return lines
}

// Render emits the prompt block, hard-capped at maxVocabLines lines —
// prefill latency matters at 1.7B.
func (v Vocab) Render() string {
	if v.IDPrefix == "" && len(v.Types) == 0 && len(v.Labels) == 0 &&
		len(v.Assignees) == 0 && len(v.Owners) == 0 && len(v.Epics) == 0 {
		return ""
	}
	lines := []string{"workspace vocabulary (real values from this board — prefer these EXACTLY):"}
	if v.IDPrefix != "" {
		lines = append(lines, fmt.Sprintf("issue ids start with %q", v.IDPrefix))
	}
	if len(v.Types) > 0 {
		lines = append(lines, "types in use: "+strings.Join(v.Types, ", "))
	}
	if len(v.Assignees) > 0 {
		lines = append(lines, "assignees (exact, quote if spaced): "+quoteAll(v.Assignees))
	}
	if len(v.Owners) > 0 {
		lines = append(lines, "owners (exact, quote if spaced): "+quoteAll(v.Owners))
	}
	if len(v.Labels) > 0 {
		lines = append(lines, "labels (count):")
		lines = append(lines, chunk(v.Labels, 6, "  ")...)
	}
	if len(v.Epics) > 0 {
		lines = append(lines, "epics/roots (parent=<id> matches their DIRECT children):")
		for _, e := range v.Epics {
			lines = append(lines, "  "+e)
		}
	}
	if len(lines) > maxVocabLines {
		lines = lines[:maxVocabLines]
	}
	return strings.Join(lines, "\n")
}
