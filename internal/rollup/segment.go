package rollup

import "github.com/awhitty/bb/internal/bd"

// segment.go is the third piece of the tree algebra: it composes the Stage 3
// grouper (Group + SortGrouped) with the Stage 4 traversal (Traverse) so a tree
// can group each level's children by a facet and apply the two-level sort
// RECURSIVELY — the same section/item ordering run at every forest level, not
// only at the flat root. Traverse walks the connectors; SegmentChildren decides,
// for one parent, what order its children come in and where the sibling-group
// boundaries fall.

// SegmentChildren groups one parent's children into facet sections (Group),
// orders the sections and the items within each section (SortGrouped), and
// returns the flattened child-id order together with the ids that LEAD a new
// sibling-group (every section's lead except the first, so a renderer draws a
// separator before them). Feeding the returned order back as a node's children
// makes Traverse render the children segmented, with its ├─/└─ connectors still
// derived from the final order.
//
// A child is emitted once, in the first section (in sorted order) that contains
// it: for the single-valued facets (status/type/priority/root/blockers) that is
// its only section; for the multi-valued label facet the Group fan-out is
// collapsed to the child's first-sorted label, so the tree never renders one
// node's subtree twice under the same parent.
func SegmentChildren(children []bd.Issue, facet Facet, section SectionSort, item ItemSort) (order []string, sectionLead map[string]string) {
	cols := SortGrouped(Group(children, facet), section, item)
	seen := make(map[string]bool, len(children))
	sectionLead = map[string]string{}
	for _, col := range cols {
		lead := true // the first NEW id in this column starts the section
		for _, is := range col.Issues {
			if seen[is.ID] {
				continue // already emitted under an earlier section (label fan-out)
			}
			seen[is.ID] = true
			if lead && len(order) > 0 {
				sectionLead[is.ID] = col.Title
			}
			order = append(order, is.ID)
			lead = false
		}
	}
	return order, sectionLead
}
