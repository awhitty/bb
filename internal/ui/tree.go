package ui

import (
	"fmt"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/awhitty/bb/internal/bd"
	"github.com/awhitty/bb/internal/rollup"
)

// The tree view (key 5) renders a nested outline: box-drawing connectors,
// compact [type][P#][id] badges, title, status glyph, right-aligned age.
// Two nesting relations, toggled with d: hierarchy (parent links) and deps
// (issues nest under what blocks them).

type treeRow struct {
	issue     bd.Issue
	prefix    string // box-drawing connector run
	depth     int    // nesting depth (0 = root)
	extra     int    // additional blockers beyond the one it nests under
	collapsed bool   // its subtree is folded away
	hidden    int    // descendants hidden under it while collapsed
	section   string // facet-segmentation: the section this row LEADS ("" = not a lead)
	stub      bool   // a dimmed hidden-ancestor placeholder, not a real board row
}

// subtreeSize counts a node and all descendants, EXCLUDING stub nodes (hidden-
// ancestor placeholders never count toward the fold/filter sizes or the +N
// badges).
func subtreeSize(n *rollup.TreeNode) int {
	total := 0
	if !n.Stub {
		total = 1
	}
	for _, c := range n.Children {
		total += subtreeSize(c)
	}
	return total
}

// treeForest builds the nesting forest for the current traverse: the relation
// axis (depsTree — hierarchy vs dependency edges) crossed with the direction
// axis (treeDir — forward vs invert). Invert reverses each edge: ancestors nest
// under descendants, issues nest under what THEY block. extra reports per-id the
// edges beyond the one a node nests under (multi-blocker / multi-child), "" when
// none. Shared by buildTreeRows and the collapse gestures so folding matches the
// displayed forest.
func (m Model) treeForest(issues []bd.Issue) ([]*rollup.TreeNode, map[string]int) {
	switch {
	case m.depsTree && m.treeDir:
		return rollup.BuildDepsForestReverse(issues)
	case m.depsTree:
		return rollup.BuildDepsForestStubbed(issues, m.byID)
	case m.treeDir:
		return rollup.BuildTreeReverse(issues)
	default:
		return rollup.BuildTreeStubbed(issues, m.byID), nil
	}
}

// buildTreeRows flattens the forest into rows, applying the active tree sort,
// the collapse set, and the min-subtree filter. A collapsed node renders with
// a "+N" fold marker and its descendants are skipped; a top-level tree with
// fewer than minSubtree descendants is dropped entirely.
func (m Model) buildTreeRows(issues []bd.Issue) []treeRow {
	roots, extra := m.treeForest(issues)
	rollup.SortForestBy(roots, m.treeSort)

	// Filter: keep only top-level trees with >= minSubtree (real) descendants.
	// The whole subtree is kept or dropped together — a stub ancestor never
	// promotes its children past the filter. subtreeSize already excludes stubs,
	// so a stub root is measured by its real descendants (no self to subtract).
	if m.minSubtree > 0 {
		var kept []*rollup.TreeNode
		for _, r := range roots {
			size := subtreeSize(r)
			if !r.Stub {
				size-- // count descendants, excluding the real root itself
			}
			if size >= m.minSubtree {
				kept = append(kept, r)
			}
		}
		roots = kept
	}

	// Index the sorted, filtered forest by id so the pure traversal can walk it
	// through an edge function and each emitted row can recover its node.
	nodeByID := map[string]*rollup.TreeNode{}
	var indexNode func(n *rollup.TreeNode)
	indexNode = func(n *rollup.TreeNode) {
		nodeByID[n.Issue.ID] = n
		for _, c := range n.Children {
			indexNode(c)
		}
	}
	for _, r := range roots {
		indexNode(r)
	}

	// segment orders one level's nodes. With no facet it keeps forest order (so the
	// everyday tree is byte-unchanged); with a facet it groups the nodes into facet
	// sections and applies the two-level sort (rollup.SegmentChildren) — invoked at
	// the roots and, through edgeFn, at every child list, so the two-level sort runs
	// recursively. Section leads accumulate into segLead for the separator render.
	section := rollup.SectionSort{Facet: m.treeFacet, Tree: m.treeSort}
	item := rollup.ItemSort{Flat: m.flatSort}
	segLead := map[string]string{}
	segment := func(nodes []*rollup.TreeNode) []string {
		if m.treeFacet == "" {
			ids := make([]string, len(nodes))
			for i, n := range nodes {
				ids[i] = n.Issue.ID
			}
			return ids
		}
		issues := make([]bd.Issue, len(nodes))
		for i, n := range nodes {
			issues[i] = n.Issue
		}
		order, leads := rollup.SegmentChildren(issues, m.treeFacet, section, item)
		for id, title := range leads {
			segLead[id] = title
		}
		return order
	}

	rootIDs := segment(roots)

	// edgeFn yields a node's children in segment order; a collapsed parent yields
	// none, folding its subtree away (the traversal then leaves it a leaf).
	edgeFn := func(id string) []string {
		n := nodeByID[id]
		if n == nil {
			return nil
		}
		if m.collapsed[id] && len(n.Children) > 0 {
			return nil
		}
		return segment(n.Children)
	}

	forest := rollup.Traverse(rootIDs, edgeFn, rollup.TraverseOpts{})
	rows := make([]treeRow, 0, len(forest))
	for _, fr := range forest {
		n := nodeByID[fr.ID]
		folded := m.collapsed[fr.ID] && len(n.Children) > 0
		row := treeRow{issue: n.Issue, prefix: fr.Prefix, depth: fr.Depth, extra: extra[fr.ID], collapsed: folded, section: segLead[fr.ID], stub: n.Stub}
		if folded {
			// The +N count is real descendants only (stubs never counted).
			for _, c := range n.Children {
				row.hidden += subtreeSize(c)
			}
		}
		rows = append(rows, row)
	}
	return rows
}

// treeHasVisibleChildren reports whether the row at i is an expanded parent —
// the next row in the pre-order flatten is its child (deeper).
func (m Model) treeHasVisibleChildren(i int) bool {
	return i >= 0 && i+1 < len(m.treeRows) && m.treeRows[i+1].depth > m.treeRows[i].depth
}

// treeCollapseFocused is the LEFT-arrow / h action in the tree. It prefers the
// structural move and falls through to a plain up-row move when there's nothing
// structural left to do, so HOLDING ← folds each branch all the way to its root
// and then carries on UP through the forest instead of stalling (the "roll a
// set of trees up to the root" gesture — a terminal can't report ← and ↑ held
// at once, so ← subsumes the ↑ fallback):
//  1. an expanded parent → fold it;
//  2. else walk UP to the parent (standard outline convention);
//  3. else (a top-level node with nothing to fold) → move up one row.
func (m *Model) treeCollapseFocused() {
	if m.treeIdx < 0 || m.treeIdx >= len(m.treeRows) {
		return
	}
	cur := m.treeRows[m.treeIdx]
	if m.treeHasVisibleChildren(m.treeIdx) {
		m.collapsed[cur.issue.ID] = true
		m.rebuild()
		m.jumpTo(cur.issue.ID)
		return
	}
	for i := m.treeIdx - 1; i >= 0; i-- {
		if m.treeRows[i].depth < cur.depth {
			m.treeIdx = i
			return
		}
	}
	if m.treeIdx > 0 {
		m.treeIdx-- // nothing to fold and no parent above — keep going up
	}
}

// treeExpandFocused is the RIGHT-arrow / l action in the tree — the mirror of
// treeCollapseFocused. HOLDING → unfurls each branch and then carries on DOWN:
//  1. a folded node → unfold it;
//  2. else step INTO the first child;
//  3. else (a leaf with nothing to unfold) → move down one row.
func (m *Model) treeExpandFocused() {
	if m.treeIdx < 0 || m.treeIdx >= len(m.treeRows) {
		return
	}
	cur := m.treeRows[m.treeIdx]
	if cur.collapsed {
		delete(m.collapsed, cur.issue.ID)
		m.rebuild()
		m.jumpTo(cur.issue.ID)
		return
	}
	if m.treeHasVisibleChildren(m.treeIdx) {
		m.treeIdx++ // into the first child
		return
	}
	if m.treeIdx < len(m.treeRows)-1 {
		m.treeIdx++ // a leaf with nothing to unfold — keep going down
	}
}

// collapseToRoots folds every top-level tree that has children — the
// "roll up to parents" action; individual roots re-expand with the collapse
// toggle. deps selects which forest defines "top-level".
func (m *Model) collapseToRoots() {
	roots, _ := m.treeForest(m.visibleIssues())
	for _, r := range roots {
		if len(r.Children) > 0 {
			m.collapsed[r.Issue.ID] = true
		}
	}
}

// collapseAllBranches folds every node with children in the current tree
// relation. Descendant folds are stored even when their ancestor is hidden, so
// expanding a root still leaves nested branches folded.
func (m *Model) collapseAllBranches() {
	roots, _ := m.treeForest(m.visibleIssues())
	var walk func(*rollup.TreeNode)
	walk = func(n *rollup.TreeNode) {
		if len(n.Children) > 0 {
			m.collapsed[n.Issue.ID] = true
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	for _, r := range roots {
		walk(r)
	}
}

var typeLetters = map[string]string{
	"epic": "E", "feature": "F", "task": "T", "bug": "B", "chore": "C", "decision": "D",
}

var typeColors = map[string]lipgloss.Color{
	"epic": "5", "feature": "2", "task": "7", "bug": "1", "chore": "8", "decision": "6",
}

func typeBadge(t string, focused bool) string {
	letter, ok := typeLetters[t]
	if !ok {
		letter = "·"
	}
	color, ok := typeColors[t]
	if !ok {
		color = "8"
	}
	sty := lipgloss.NewStyle().Foreground(color)
	if focused {
		sty = sty.Reverse(true)
	}
	return sty.Render("[" + letter + "]")
}

// relAge humanizes updated_at ("3h ago"). Empty/unparseable → "".
func relAge(ts string, now time.Time) string {
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return ""
	}
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 14*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	case d < 8*7*24*time.Hour:
		return fmt.Sprintf("%dw ago", int(d.Hours()/(24*7)))
	default:
		return fmt.Sprintf("%dmo ago", int(d.Hours()/(24*30)))
	}
}

// treeLine renders one outline row at width w: box-drawing connectors and the
// title flex on the LEFT, and the metadata (priority · status · age) pinned to
// fixed columns on the RIGHT — so those columns line up regardless of tree
// depth. Shares the row.go cell formatters. No brackets, no glyphs, no id.
func treeLine(r treeRow, focused bool, w int, age ageFn, flags relFlags, emph rowEmph) string {
	if w <= 0 {
		return ""
	}
	if r.stub {
		// A hidden-ancestor placeholder: dimmed lineage row, no priority/status/age
		// columns. "· Title (closed)" (or "(hidden)" when filtered for another
		// reason), keeping the chain visibly rooted.
		reason := "hidden"
		if r.issue.Status == bd.StatusClosed {
			reason = "closed"
		}
		label := "· " + r.issue.Title + " (" + reason + ")"
		if r.collapsed {
			label += fmt.Sprintf("  ▸ +%d", r.hidden) // folded subtree
		}
		return gutterEmph(focused, emph) + styDim.Render(ansi.Truncate(r.prefix+label, max(1, w-focusGutter), "…"))
	}
	prefixW := ansi.StringWidth(r.prefix)
	title := r.issue.Title
	if r.extra > 0 {
		title += fmt.Sprintf("  +%d deps", r.extra)
	}
	if r.collapsed {
		title += fmt.Sprintf("  ▸ +%d", r.hidden) // folded subtree
	}
	// gutter · connectors · title(flex) · [indicator pri status age], 1-space gutter.
	rightW := relSlot + 1 + rightBlockW
	titleW := w - focusGutter - prefixW - rightW - 1
	if titleW < 4 {
		return gutterEmph(focused, emph) + ansi.Truncate(r.prefix+title, max(1, w-focusGutter), "…")
	}
	if emph.muted { // spotlight: dim non-targets, connectors + title only
		return gutterEmph(focused, emph) + styDim.Render(r.prefix+ansi.Truncate(title, titleW, "…"))
	}
	right := lipgloss.JoinHorizontal(lipgloss.Top,
		relIndicator(flags), " ",
		priorityCell(r.issue.Priority), " ",
		statusCell(r.issue.Status), " ",
		age(r.issue))
	return gutterEmph(focused, emph) +
		styDim.Render(r.prefix) +
		titleWithChips(r.issue, title, titleW, focused) +
		" " +
		right
}

// treeSectionGuide is the indent a section separator sits at: a row's connector
// prefix minus its own trailing ├─/└─ connector (3 cells), i.e. the ancestor
// guide its siblings share. A root row (empty prefix) yields "".
func treeSectionGuide(prefix string) string {
	r := []rune(prefix)
	if len(r) < 3 {
		return ""
	}
	return string(r[:len(r)-3])
}

// renderTreeSeparator draws the dim, connector-aligned divider between two
// sibling facet-sections under a parent: the shared ancestor guide, then a dim
// "─ label ─" rule, clipped to width.
func renderTreeSeparator(guide, label string, w int) string {
	if w <= 0 {
		return ""
	}
	line := styDim.Render(guide + "─ " + label + " ─")
	return ansi.Truncate(line, w, "")
}

// viewTree renders the outline at exactly bodyH lines × boardWidth, with the
// same always-reserved ↑/↓ indicator lines and fixed page-jump window as the
// kanban columns. When a facet is active the rows are segmented into sibling
// groups; a separator line precedes each row that leads a new group. The
// navigation cursor (m.treeIdx) still indexes only the issue rows — separators
// are never focusable — so the movement code is untouched; only the focus is
// mapped onto the interleaved display sequence here.
func (m Model) viewTree(bodyH int) string {
	w := m.boardWidth()
	rows := m.treeRows
	if len(rows) == 0 {
		empty := styDim.Render("no issues — r refresh · c include closed")
		return lipgloss.Place(w, bodyH, lipgloss.Center, lipgloss.Center, empty)
	}
	age := m.ageCellFn(time.Now())

	// disp interleaves separator lines (row < 0) with issue rows. With no facet
	// no row leads a section, so disp == rows 1:1 and focus == m.treeIdx — the
	// everyday tree renders byte-for-byte as before.
	type dispItem struct {
		row   int    // >= 0: index into rows; < 0: a separator
		guide string // separator indent
		label string // separator section label
	}
	disp := make([]dispItem, 0, len(rows))
	focus := 0
	for i, r := range rows {
		if r.section != "" {
			disp = append(disp, dispItem{row: -1, guide: treeSectionGuide(r.prefix), label: r.section})
		}
		if i == m.treeIdx {
			focus = len(disp)
		}
		disp = append(disp, dispItem{row: i})
	}

	return m.renderNavList(bodyH, len(disp), focus, "\x00tree",
		func(di int, focused bool) string {
			it := disp[di]
			if it.row < 0 {
				return renderTreeSeparator(it.guide, it.label, w)
			}
			r := rows[it.row]
			return treeLine(r, focused, w, age, m.relFlagsFor(r.issue), m.issueEmph(r.issue.ID))
		},
		moreTop, moreBottom)
}
