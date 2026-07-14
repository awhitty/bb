package rollup

import (
	"reflect"
	"testing"
)

// edges builds an edgeFn from a static adjacency map.
func edges(m map[string][]string) func(string) []string {
	return func(id string) []string { return m[id] }
}

// TestTraverseForestShape pins the pre-order flatten and the exact connector
// prefixes for a small forest (the run the tree view rendered by hand).
func TestTraverseForestShape(t *testing.T) {
	fn := edges(map[string][]string{
		"a": {"b", "c"},
		"b": {"d"},
	})
	got := Traverse([]string{"a"}, fn, TraverseOpts{})
	want := Forest{
		{ID: "a", Prefix: "", Depth: 0},
		{ID: "b", Prefix: "├─ ", Depth: 1},
		{ID: "d", Prefix: "│  └─ ", Depth: 2},
		{ID: "c", Prefix: "└─ ", Depth: 1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("forest shape mismatch:\n got %#v\nwant %#v", got, want)
	}
}

// TestTraverseMultipleRoots checks the last-sibling connector across several
// roots (a root is "last" only when it is the final root).
func TestTraverseMultipleRoots(t *testing.T) {
	fn := edges(map[string][]string{
		"r1": {"x"},
		"r2": nil,
	})
	got := Traverse([]string{"r1", "r2"}, fn, TraverseOpts{})
	want := Forest{
		{ID: "r1", Prefix: "", Depth: 0},
		{ID: "x", Prefix: "└─ ", Depth: 1},
		{ID: "r2", Prefix: "", Depth: 0},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("multi-root shape mismatch:\n got %#v\nwant %#v", got, want)
	}
}

// TestTraverseSelfCycle: a self-edge emits the node once, then a ↻ stub leaf,
// and does not loop.
func TestTraverseSelfCycle(t *testing.T) {
	fn := edges(map[string][]string{"a": {"a"}})
	got := Traverse([]string{"a"}, fn, TraverseOpts{})
	want := Forest{
		{ID: "a", Prefix: "", Depth: 0},
		{ID: "a", Prefix: "└─ ", Depth: 1, Cycle: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("self-cycle mismatch:\n got %#v\nwant %#v", got, want)
	}
}

// TestTraverseMutualCycle: a→b→a stubs the second visit to a and stops.
func TestTraverseMutualCycle(t *testing.T) {
	fn := edges(map[string][]string{
		"a": {"b"},
		"b": {"a"},
	})
	got := Traverse([]string{"a"}, fn, TraverseOpts{})
	want := Forest{
		{ID: "a", Prefix: "", Depth: 0},
		{ID: "b", Prefix: "└─ ", Depth: 1},
		{ID: "a", Prefix: "   └─ ", Depth: 2, Cycle: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("mutual-cycle mismatch:\n got %#v\nwant %#v", got, want)
	}
}

// TestTraverseCyclePerPathNotGlobal: a diamond (a→b, a→c, b→d, c→d) visits d
// twice — once under each parent — because the guard is per-path, not a global
// seen set. Neither visit is a cycle stub.
func TestTraverseCyclePerPathNotGlobal(t *testing.T) {
	fn := edges(map[string][]string{
		"a": {"b", "c"},
		"b": {"d"},
		"c": {"d"},
	})
	got := Traverse([]string{"a"}, fn, TraverseOpts{})
	var dRows int
	for _, r := range got {
		if r.ID == "d" {
			dRows++
			if r.Cycle {
				t.Fatalf("diamond leaf d marked as a cycle at depth %d", r.Depth)
			}
		}
	}
	if dRows != 2 {
		t.Fatalf("expected d to appear under both parents (2 rows), got %d", dRows)
	}
}

// TestTraverseMaxDepth: MaxDepth=1 emits roots and one level of children, and
// drops everything deeper.
func TestTraverseMaxDepth(t *testing.T) {
	fn := edges(map[string][]string{
		"a": {"b"},
		"b": {"c"},
	})
	got := Traverse([]string{"a"}, fn, TraverseOpts{MaxDepth: 1})
	want := Forest{
		{ID: "a", Prefix: "", Depth: 0},
		{ID: "b", Prefix: "└─ ", Depth: 1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("maxDepth mismatch:\n got %#v\nwant %#v", got, want)
	}
}
