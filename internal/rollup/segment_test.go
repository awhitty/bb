package rollup

import (
	"reflect"
	"testing"

	"github.com/awhitty/bb/internal/bd"
)

// SegmentChildren groups one parent's children into facet sections, orders the
// sections by the facet and the items within each by the item sort, and flags
// each section's lead (except the first) so a renderer can draw a separator.
func TestSegmentChildrenStatus(t *testing.T) {
	children := []bd.Issue{
		{ID: "a", Status: bd.StatusOpen, Priority: 1},
		{ID: "b", Status: bd.StatusInProgress, Priority: 0},
		{ID: "c", Status: bd.StatusOpen, Priority: 0},
	}
	order, leads := SegmentChildren(children, FacetStatus,
		SectionSort{Facet: FacetStatus},
		ItemSort{Flat: Sort{Key: SortPriority}})

	// statusOrder puts open ahead of in_progress; inside the open section the
	// item sort (priority asc) puts c (P0) ahead of a (P1).
	want := []string{"c", "a", "b"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
	// Only the in_progress section (the second one) has a lead; the first section
	// never draws a separator above it.
	wantLeads := map[string]string{"b": bd.StatusInProgress}
	if !reflect.DeepEqual(leads, wantLeads) {
		t.Fatalf("leads = %v, want %v", leads, wantLeads)
	}
}

// The multi-valued label facet fans an issue into every label in Group, but in a
// tree a node must appear once, so SegmentChildren emits it under its first
// sorted section only (no duplicated child, no duplicated subtree).
func TestSegmentChildrenLabelIsSingleMembership(t *testing.T) {
	children := []bd.Issue{
		{ID: "x", Status: bd.StatusOpen, Labels: []string{"api", "ui"}},
		{ID: "y", Status: bd.StatusOpen, Labels: []string{"ui"}},
	}
	order, _ := SegmentChildren(children, FacetLabel,
		SectionSort{Facet: FacetLabel},
		ItemSort{Flat: Sort{Key: SortPriority}})

	seen := map[string]int{}
	for _, id := range order {
		seen[id]++
	}
	for id, n := range seen {
		if n != 1 {
			t.Fatalf("child %s emitted %d times, want exactly 1", id, n)
		}
	}
	if len(order) != 2 {
		t.Fatalf("order = %v, want 2 distinct children", order)
	}
}
