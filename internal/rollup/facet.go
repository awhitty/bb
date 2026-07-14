package rollup

import (
	"fmt"
	"sort"
	"strings"

	"github.com/awhitty/bb/internal/bd"
)

// Facet is a grouping dimension. Unlike Mode it is MULTI-valued: keysFor may
// return more than one key for a single issue (a label facet fans an issue out
// into one column per label), so Group can express groupings the single-column
// push() model in Rollup cannot. The four legacy facets each return exactly one
// key, byte-for-byte the same as the corresponding Mode.
type Facet string

const (
	FacetStatus   Facet = "status"
	FacetType     Facet = "type"
	FacetRoot     Facet = "root"
	FacetBlockers Facet = "blockers"
	FacetLabel    Facet = "label"
	FacetPriority Facet = "priority"
	FacetDepth    Facet = "depth"
)

// FacetBinding pairs a group/lane token with the facet it selects. "none" binds
// the empty facet (clears any grouping override).
type FacetBinding struct {
	Name  string
	Facet Facet
}

// FacetBindings is the ONE canonical facet vocabulary every facet surface
// consumes: the UI grouper and break-out-lane dropdowns (internal/ui/control.go),
// set_view spec resolution (internal/ui/agent.go), the header labels
// (internal/ui/view.go), and the MCP set_view validation + schema text
// (internal/mcpserv). "none" first, then the single-valued facets in the header's
// reading order, depth last (the computed dependency-depth axis). Adding a facet
// here reaches every surface at once, so no surface can drift from another — the
// drift that let the MCP reject depth while the UI offered it (demo-rst.11).
var FacetBindings = []FacetBinding{
	{"none", ""},
	{"status", FacetStatus},
	{"type", FacetType},
	{"ancestor", FacetRoot},
	{"blockers", FacetBlockers},
	{"label", FacetLabel},
	{"priority", FacetPriority},
	{"depth", FacetDepth},
}

// facetAliases are legacy synonyms accepted in addition to the canonical
// FacetBindings names. The UI grouper offers only the canonical name; set_view
// and the MCP also accept these so older callers keep working.
var facetAliases = map[string]Facet{
	"root": FacetRoot, // canonical name is "ancestor"
}

// FacetFromName resolves a group/lane token (a canonical FacetBindings name or a
// legacy alias) to its facet. "none" resolves to the empty facet. ok is false for
// an unknown token so the caller can leave the grouping untouched.
func FacetFromName(name string) (Facet, bool) {
	for _, b := range FacetBindings {
		if b.Name == name {
			return b.Facet, true
		}
	}
	if f, ok := facetAliases[name]; ok {
		return f, true
	}
	return "", false
}

// FacetNames returns the canonical accepted group/lane tokens in FacetBindings
// order. It is the source for the MCP's accepted-value list in schema text and
// validation errors, so the advertised vocabulary is derived, never hand-listed.
func FacetNames() []string {
	names := make([]string, len(FacetBindings))
	for i, b := range FacetBindings {
		names[i] = b.Name
	}
	return names
}

// FacetLabelFor is the header/label token for a facet — the canonical name it
// binds to in FacetBindings, or "" if the facet is unbound.
func FacetLabelFor(f Facet) string {
	for _, b := range FacetBindings {
		if b.Facet == f {
			return b.Name
		}
	}
	return ""
}

// NoLabelKey is the synthetic column for issues with no labels, so an unlabeled
// issue still lands somewhere under the label facet.
const NoLabelKey = "\x00unlabeled"

// depthReadyKey is the rank-0 depth column: the unblocked front, everything that
// can happen NOW. Deeper ranks read as "depth 1", "depth 2", … — each column is
// what the columns to its left unlock, so the board reads as a tech tree.
const depthReadyKey = "ready"

// depthKey names the depth column for a computed dependency-depth rank.
func depthKey(rank int) string {
	if rank <= 0 {
		return depthReadyKey
	}
	return fmt.Sprintf("depth %d", rank)
}

// depthKeyRank recovers the numeric rank from a depth column key so sectionLess
// can order the columns ascending (ready leftmost). Depth's value domain is
// dynamic — it grows with the longest chain in the visible set — so it can't be
// a fixed canonicalKeys list; this parse is the ordering instead.
func depthKeyRank(key string) int {
	if key == depthReadyKey {
		return 0
	}
	var n int
	if _, err := fmt.Sscanf(key, "depth %d", &n); err != nil {
		return 0
	}
	return n
}

// depthRanks computes each issue's dependency depth over the given set (already
// visibility-filtered by the caller): rank 0 = no open gate within the set,
// rank N = 1 + the deepest rank among its open in-set GATES. A gate is either an
// open blocker OR an open child — bd itself treats parent-child as blocking (a
// parent with any open child is never "ready"), so a leaf keeps pure dependency
// semantics while a parent is gated by its subtree exactly as it is by its
// blockers. Closed and out-of-set gates do not count — openBlockers drops both
// for blockers, openChildren drops both for children — so a rank is always
// relative to what is currently visible (scope/filter/closed-toggle), and an
// apex epic (the whole open subtree beneath it) lands one past its deepest
// descendant. bd forbids dependency cycles and the hierarchy is a tree, but the
// combined edge set is guarded regardless: an edge that revisits a node still
// mid-walk is cut (contributes rank 0), so a member of a cycle gets a stable
// finite rank instead of unbounded recursion.
func depthRanks(issues []bd.Issue) map[string]int {
	byID := index(issues)
	kids := openChildren(issues, byID)
	rank := make(map[string]int, len(issues))
	done := make(map[string]bool, len(issues))
	inProgress := make(map[string]bool, len(issues))
	var visit func(id string) int
	visit = func(id string) int {
		if done[id] {
			return rank[id]
		}
		if inProgress[id] {
			return 0 // cycle edge: cut it rather than recurse forever
		}
		inProgress[id] = true
		best := -1
		for _, b := range openBlockers(byID[id], byID) {
			if r := visit(b); r > best {
				best = r
			}
		}
		for _, c := range kids[id] {
			if r := visit(c); r > best {
				best = r
			}
		}
		delete(inProgress, id)
		r := best + 1
		rank[id] = r
		done[id] = true
		return r
	}
	for _, is := range issues {
		visit(is.ID)
	}
	return rank
}

// openChildren maps each parent id to its open, in-set children (by the Parent
// hierarchy link). It is to children what openBlockers is to blockers: a closed
// child or an out-of-set child is dropped, so a done or out-of-scope subtree
// never gates its ancestor. A self-parent link is skipped so it can't gate
// itself.
func openChildren(issues []bd.Issue, byID map[string]bd.Issue) map[string][]string {
	kids := map[string][]string{}
	for _, is := range issues {
		p := is.Parent
		if p == "" || p == is.ID {
			continue
		}
		if _, ok := byID[p]; !ok {
			continue
		}
		if is.Status == bd.StatusClosed {
			continue
		}
		kids[p] = append(kids[p], is.ID)
	}
	return kids
}

// FacetForMode maps a legacy kanban Mode to its equivalent single-valued facet.
func FacetForMode(m Mode) Facet {
	switch m {
	case ModeType:
		return FacetType
	case ModeRoot:
		return FacetRoot
	case ModeBlockers:
		return FacetBlockers
	default:
		return FacetStatus
	}
}

// blockersKey classifies an issue into the blockers facet's four buckets. It is
// the exact rule Rollup uses inline for ModeBlockers.
func blockersKey(issue bd.Issue, byID map[string]bd.Issue) string {
	switch {
	case issue.Status == bd.StatusClosed:
		return "done"
	case len(openBlockers(issue, byID)) > 0:
		return "blocked"
	case issue.DependentCount > 0:
		return "blocking others"
	default:
		return "free"
	}
}

// keysFor returns every column key an issue belongs to under this facet. The
// status/type/root/blockers facets return exactly one key (matching Rollup's
// push() keys); label returns one per label; priority returns one.
func (f Facet) keysFor(issue bd.Issue, byID map[string]bd.Issue) []string {
	switch f {
	case FacetType:
		t := issue.IssueType
		if t == "" {
			t = "untyped"
		}
		return []string{t}
	case FacetRoot:
		return []string{RootOf(issue, byID).ID}
	case FacetBlockers:
		return []string{blockersKey(issue, byID)}
	case FacetLabel:
		if len(issue.Labels) == 0 {
			return []string{NoLabelKey}
		}
		return append([]string(nil), issue.Labels...)
	case FacetPriority:
		return []string{fmt.Sprintf("P%d", issue.Priority)}
	default: // FacetStatus
		return []string{issue.Status}
	}
}

// canonicalKeys is the opinionated section order for a facet whose value domain
// is FIXED (status lifecycle, type container-to-meta, priority rank, blockers
// state). Declaring it here — once per facet, not once per view — makes every
// grouped surface (list sections, board columns, break-out lane bands, tree
// segments) order sections by the facet's semantic rank rather than by card
// count. A free-form facet (label, ancestor root, or any future assignee)
// returns nil and orders its sections case-insensitively. The empty bucket is
// never listed here; noneKey sorts it last regardless of facet.
func (f Facet) canonicalKeys() []string {
	switch f {
	case FacetStatus:
		return statusOrder
	case FacetType:
		return typeOrder
	case FacetPriority:
		return priorityOrder
	case FacetBlockers:
		return blockersOrder
	}
	return nil
}

// noneKey reports whether key is this facet's empty bucket — the unlabeled or
// untyped section — which always sorts last, after every populated section.
func (f Facet) noneKey(key string) bool {
	switch f {
	case FacetLabel:
		return key == NoLabelKey
	case FacetType:
		return key == "untyped"
	}
	return false
}

// sectionLess is the ONE section comparator every facet uses. It orders sections
// by the facet's canonical key list when it has one, otherwise
// case-insensitively; the empty bucket always sorts last, and a key outside a
// canonical list falls after the listed keys, alphabetically.
func sectionLess(f Facet, a, b string) bool {
	if f == FacetDepth {
		return depthKeyRank(a) < depthKeyRank(b) // ascending: ready (rank 0) leftmost
	}
	if na, nb := f.noneKey(a), f.noneKey(b); na != nb {
		return nb // a is none ⇒ sorts after b; b is none ⇒ a first
	}
	if canon := f.canonicalKeys(); canon != nil {
		ia, ib := indexOf(canon, a), indexOf(canon, b)
		if (ia >= 0) != (ib >= 0) {
			return ia >= 0 // a listed, b not ⇒ a first
		}
		if ia >= 0 {
			return ia < ib
		}
	}
	if la, lb := strings.ToLower(a), strings.ToLower(b); la != lb {
		return la < lb
	}
	return a < b
}

// titleFor is the column header for a key. Only the root facet decorates the
// key (with the ancestor's title); every other facet titles a column by its
// key. The standalone bucket's title is set later by rollSingletons.
func (f Facet) titleFor(key string, byID map[string]bd.Issue) string {
	if f == FacetRoot {
		if root, ok := byID[key]; ok {
			return key + " " + root.Title
		}
	}
	return key
}

// Group fans each issue into every key its facet yields and stamps a per-issue
// DupCount (how many OTHER columns the issue also lands in) on each copy, so a
// renderer can flag a multi-membership card. Columns come out in first-seen key
// order; SortGrouped puts them in display order. For the single-valued facets
// this reduces to Rollup's push() with DupCount == 0 everywhere.
func Group(issues []bd.Issue, facet Facet) []Column {
	byID := index(issues)
	groups := map[string][]bd.Issue{}
	var order []string

	// Depth is a GLOBAL property of the whole set (a rank over the dependency
	// graph), not a per-issue lookup keysFor can express, so it is computed once
	// here and each issue's single key read from the result.
	var depth map[string]int
	if facet == FacetDepth {
		depth = depthRanks(issues)
	}

	keysByIssue := make([][]string, len(issues))
	for i, issue := range issues {
		if facet == FacetDepth {
			keysByIssue[i] = []string{depthKey(depth[issue.ID])}
			continue
		}
		keysByIssue[i] = facet.keysFor(issue, byID)
	}
	for i, issue := range issues {
		dup := len(keysByIssue[i]) - 1
		for _, key := range keysByIssue[i] {
			if _, ok := groups[key]; !ok {
				order = append(order, key)
			}
			cp := issue
			cp.DupCount = dup
			groups[key] = append(groups[key], cp)
		}
	}

	cols := make([]Column, 0, len(order))
	for _, key := range order {
		cols = append(cols, Column{Key: key, Title: facet.titleFor(key, byID), Issues: groups[key]})
	}
	return cols
}

// SectionSort orders the columns (sections). It carries the facet — which fixes
// the base ordering (statusOrder / blockersOrder / count-desc-then-key /
// rollSingletons) — plus the hierarchical Tree sort the root facet re-applies
// over each section's members.
type SectionSort struct {
	Facet Facet
	Tree  Sort
}

// ItemSort orders the issues within a section. Flat is the flat sort key; Less,
// if set, overrides it. The zero value sorts by priority-then-id.
type ItemSort struct {
	Flat Sort
	Less func(a, b bd.Issue) bool
}

// SortGrouped is the two-level sorter: it orders the sections by the facet's
// comparator (and rolls the root facet's singletons into a standalone bucket),
// then orders each section's issues. For the four legacy facets it reproduces
// Rollup's key order + SortColumns exactly. It returns the (possibly
// re-sliced) columns.
func SortGrouped(cols []Column, section SectionSort, item ItemSort) []Column {
	sortSectionsByFacet(cols, section.Facet)
	if section.Facet == FacetRoot {
		cols = rollSingletons(cols)
	}

	less := item.Less
	if less == nil {
		less = func(a, b bd.Issue) bool { return issueLess(a, b, item.Flat.Key, item.Flat.Desc) }
	}
	for i := range cols {
		items := cols[i].Issues
		sort.SliceStable(items, func(a, b int) bool { return less(items[a], items[b]) })
	}

	if section.Facet == FacetRoot {
		sortRootSections(cols, section.Tree)
	}
	return cols
}

// sortSectionsByFacet applies the facet's opinionated canonical section order
// (sectionLess): a fixed-domain facet by its declared key list, a free-form
// facet case-insensitively, the empty bucket always last. Card count never
// decides section order. The root facet's whole-section metric reorder is
// applied afterward by sortRootSections.
func sortSectionsByFacet(cols []Column, f Facet) {
	sort.SliceStable(cols, func(a, b int) bool {
		return sectionLess(f, cols[a].Key, cols[b].Key)
	})
}

// sortRootSections re-orders the ancestor sections by a whole-section metric
// (subtree-size / aggregate-priority / open-count / recent-activity), keeping
// the synthetic standalone bucket last. It is the ModeRoot branch of
// SortColumns, lifted verbatim.
func sortRootSections(cols []Column, tree Sort) {
	metric := func(c Column) (float64, string) {
		switch tree.Key {
		case SortAggPriority:
			best := 5
			for _, is := range c.Issues {
				if is.Priority < best {
					best = is.Priority
				}
			}
			return float64(-best), ""
		case SortOpenCount:
			n := 0
			for _, is := range c.Issues {
				if is.Status != bd.StatusClosed {
					n++
				}
			}
			return float64(n), ""
		case SortRecent:
			latest := ""
			for _, is := range c.Issues {
				if is.UpdatedAt > latest {
					latest = is.UpdatedAt
				}
			}
			return 0, latest
		default:
			return float64(len(c.Issues)), ""
		}
	}
	sort.SliceStable(cols, func(a, b int) bool {
		if cols[a].Key == StandaloneKey {
			return false
		}
		if cols[b].Key == StandaloneKey {
			return true
		}
		na, sa := metric(cols[a])
		nb, sb := metric(cols[b])
		if sa != "" || sb != "" {
			if sa != sb {
				if tree.Desc {
					return sa > sb
				}
				return sa < sb
			}
		} else if na != nb {
			if tree.Desc {
				return na > nb
			}
			return na < nb
		}
		return cols[a].Key < cols[b].Key
	})
}
