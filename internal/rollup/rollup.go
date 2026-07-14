// Package rollup groups issues into kanban columns four ways: by status,
// type, top-level ancestor, or blocker relationship. Port of the TS engine.
package rollup

import (
	"fmt"
	"sort"
	"strings"

	"github.com/awhitty/bb/internal/bd"
)

// Mode selects the grouping.
type Mode string

const (
	ModeStatus   Mode = "status"
	ModeType     Mode = "type"
	ModeRoot     Mode = "root"
	ModeBlockers Mode = "blockers"
)

// Modes in tab-cycle order (1-4 select directly).
var Modes = []Mode{ModeStatus, ModeType, ModeRoot, ModeBlockers}

// SortKey names an ordering. Flat keys order issues within a section (and the
// flat list/board); hierarchical keys order tree siblings and ancestor
// sections by whole-subtree metrics (memoized per load).
type SortKey string

const (
	// Flat sort keys.
	SortPriority SortKey = "priority"
	SortUpdated  SortKey = "updated"
	SortCreated  SortKey = "created"
	SortTitle    SortKey = "title"
	SortID       SortKey = "id"
	// Hierarchical sort keys (tree / octopus / ancestor).
	SortSubtreeSize SortKey = "subtree-size"
	SortAggPriority SortKey = "aggregate-priority"
	SortOpenCount   SortKey = "open-count"
	SortMaxDepth    SortKey = "max-depth"
	SortRecent      SortKey = "recent-activity"
)

// FlatSortKeys / TreeSortKeys are the S-cycle orders; the first of each is the
// default. defaultDesc reports whether a key sorts high-to-low by default.
var (
	FlatSortKeys = []SortKey{SortPriority, SortUpdated, SortCreated, SortTitle, SortID}
	TreeSortKeys = []SortKey{SortSubtreeSize, SortAggPriority, SortOpenCount, SortMaxDepth, SortRecent}
)

// DefaultDesc is the natural direction for a key: bigger/newer/more-urgent
// first for the metric keys; ascending for title/id; priority ascending (P0
// first, since 0 is highest).
func DefaultDesc(k SortKey) bool {
	switch k {
	case SortPriority, SortTitle, SortID:
		return false
	default:
		return true
	}
}

// singletonThreshold: ancestor mode forms a section only for a root with ≥2
// members; roots with a single member (no descendants on the board) roll into
// one "standalone" bucket rather than a hundred one-item sections.
const singletonThreshold = 2

// StandaloneKey is the synthetic column key for the rolled-up singletons.
const StandaloneKey = "\x00standalone"

// Labels are the header names for each mode.
var Labels = map[Mode]string{
	ModeStatus:   "Status (kanban)",
	ModeType:     "Issue type",
	ModeRoot:     "Ancestor (epic/root)",
	ModeBlockers: "Blockers",
}

// Column is one kanban column.
type Column struct {
	Key    string
	Title  string
	Issues []bd.Issue
}

// statusOrder / typeOrder / priorityOrder / blockersOrder are the OPINIONATED
// CANONICAL section orders — one per facet with a fixed value domain. They are
// the default order for every grouped surface (list sections, board columns,
// break-out lane bands, tree segments) so a section's position always reads as
// its semantic rank, never its card count. A facet reaches them through
// Facet.canonicalKeys; the empty bucket ("(none)"/unlabeled/untyped) is never
// listed here — it always sorts last (see Facet.noneKey).
//
// status: lifecycle order — whatever subset is present keeps this relative order.
var statusOrder = []string{
	bd.StatusOpen, bd.StatusInProgress, bd.StatusBlocked, bd.StatusDeferred, "pinned", bd.StatusClosed,
}

// type: containers/product first, then work items, then meta.
var typeOrder = []string{
	"epic", "feature", "story", "task", "bug", "chore", "spike", "decision", "milestone",
}

// priority: rank ascending — most urgent (P0) first. Keys are "P0".."P4" (see
// Facet.keysFor).
var priorityOrder = []string{"P0", "P1", "P2", "P3", "P4"}

var blockersOrder = []string{"blocked", "blocking others", "free", "done"}

func indexOf(list []string, s string) int {
	for i, v := range list {
		if v == s {
			return i
		}
	}
	return -1
}

func lessByPriorityThenID(a, b bd.Issue) bool {
	if a.Priority != b.Priority {
		return a.Priority < b.Priority
	}
	return a.ID < b.ID
}

func index(issues []bd.Issue) map[string]bd.Issue {
	byID := make(map[string]bd.Issue, len(issues))
	for _, i := range issues {
		byID[i.ID] = i
	}
	return byID
}

// RootOf walks parent links to the top-level ancestor. An issue with no
// resolvable parent falls back to its dotted-id root (demo-pqr.5 → demo-pqr)
// when that root is on the board.
func RootOf(issue bd.Issue, byID map[string]bd.Issue) bd.Issue {
	cur := issue
	seen := map[string]bool{}
	for cur.Parent != "" && !seen[cur.Parent] {
		parent, ok := byID[cur.Parent]
		if !ok {
			break
		}
		seen[cur.ID] = true
		cur = parent
	}
	if cur.ID == issue.ID && strings.Contains(issue.ID, ".") {
		if dotRoot, ok := byID[strings.SplitN(issue.ID, ".", 2)[0]]; ok {
			return dotRoot
		}
	}
	return cur
}

// openBlockers returns the real (non-hierarchy) blocker deps whose target is
// still open.
func openBlockers(issue bd.Issue, byID map[string]bd.Issue) []string {
	var out []string
	for _, d := range issue.Dependencies {
		if d.Type == "parent-child" {
			continue
		}
		if dep, ok := byID[d.DependsOnID]; ok && dep.Status != bd.StatusClosed {
			out = append(out, d.DependsOnID)
		}
	}
	return out
}

// Rollup groups issues into ordered columns for the given mode. Within a
// column, issues sort by priority then id.
func Rollup(issues []bd.Issue, mode Mode) []Column {
	byID := index(issues)
	groups := map[string][]bd.Issue{}
	var order []string
	push := func(key string, issue bd.Issue) {
		if _, ok := groups[key]; !ok {
			order = append(order, key)
		}
		groups[key] = append(groups[key], issue)
	}

	for _, issue := range issues {
		switch mode {
		case ModeStatus:
			push(issue.Status, issue)
		case ModeType:
			t := issue.IssueType
			if t == "" {
				t = "untyped"
			}
			push(t, issue)
		case ModeRoot:
			root := RootOf(issue, byID)
			push(root.ID, issue)
		case ModeBlockers:
			switch {
			case issue.Status == bd.StatusClosed:
				push("done", issue)
			case len(openBlockers(issue, byID)) > 0:
				push("blocked", issue)
			case issue.DependentCount > 0:
				push("blocking others", issue)
			default:
				push("free", issue)
			}
		}
	}

	keys := order
	facet := FacetForMode(mode)
	sort.SliceStable(keys, func(a, b int) bool {
		return sectionLess(facet, keys[a], keys[b])
	})

	cols := make([]Column, 0, len(keys))
	for _, key := range keys {
		items := groups[key]
		sort.SliceStable(items, func(a, b int) bool { return lessByPriorityThenID(items[a], items[b]) })
		title := key
		if mode == ModeRoot {
			if root, ok := byID[key]; ok {
				title = key + " " + root.Title
			}
		}
		cols = append(cols, Column{Key: key, Title: title, Issues: items})
	}
	if mode == ModeRoot {
		cols = rollSingletons(cols)
	}
	return cols
}

// rollSingletons collapses ancestor sections with a single member (a root with
// no descendants on the board) into ONE "standalone" bucket at the bottom, so
// the ancestor view shows real groupings instead of a hundred one-item
// sections. Multi-member sections (≥2) keep their own header.
func rollSingletons(cols []Column) []Column {
	var kept []Column
	var standalone []bd.Issue
	for _, c := range cols {
		if len(c.Issues) < singletonThreshold {
			standalone = append(standalone, c.Issues...)
		} else {
			kept = append(kept, c)
		}
	}
	if len(standalone) > 0 {
		sort.SliceStable(standalone, func(a, b int) bool { return lessByPriorityThenID(standalone[a], standalone[b]) })
		kept = append(kept, Column{Key: StandaloneKey, Title: fmt.Sprintf("standalone (%d)", len(standalone)), Issues: standalone})
	}
	return kept
}

// TreeNode is one node of the parent-child forest.
type TreeNode struct {
	Issue    bd.Issue
	Children []*TreeNode
	// Stub marks a synthesized ancestor: a hidden (closed, or filtered-out)
	// parent/blocker materialized only so its still-visible descendants stay
	// rooted under their real lineage instead of surfacing as fake top-level
	// orphans. A stub renders dimmed and is excluded from subtree metrics and the
	// +N fold/deps counts.
	Stub bool
}

// BuildTree assembles the parent-child forest, sorted by priority then id at
// every level.
func BuildTree(issues []bd.Issue) []*TreeNode {
	nodes := make(map[string]*TreeNode, len(issues))
	ids := make([]string, 0, len(issues))
	for _, i := range issues {
		nodes[i.ID] = &TreeNode{Issue: i}
		ids = append(ids, i.ID)
	}
	var roots []*TreeNode
	for _, id := range ids {
		node := nodes[id]
		if parent, ok := nodes[node.Issue.Parent]; ok && parent != node {
			parent.Children = append(parent.Children, node)
		} else {
			roots = append(roots, node)
		}
	}
	sortForest(roots)
	return roots
}

// BuildTreeStubbed is BuildTree with hidden-ancestor stubs: when a visible
// issue's parent is not in the visible set but IS on the board, the chain of
// hidden ancestors up to the nearest visible one is materialized as dimmed stub
// nodes — so a child never surfaces as a fake root just because its parent (e.g.
// a closed epic excluded by the closed toggle) is hidden. board is the whole-
// board index (visible ∪ hidden). With every parent visible it is byte-for-byte
// BuildTree.
func BuildTreeStubbed(issues []bd.Issue, board map[string]bd.Issue) []*TreeNode {
	nest := func(is bd.Issue) string {
		if is.Parent == "" || is.Parent == is.ID {
			return ""
		}
		if _, ok := board[is.Parent]; ok {
			return is.Parent
		}
		return ""
	}
	return buildStubbedForest(issues, board, nest)
}

// BuildDepsForestStubbed is BuildDepsForest with hidden-blocker stubs: a visible
// issue whose nesting blocker is hidden (e.g. closed) nests under a dimmed stub
// of that blocker (and its hidden blocker chain) rather than being promoted to a
// root. The nesting blocker is the first dependency present on the board (in
// declaration order); "+n deps" counts the remaining VISIBLE blockers, so a stub
// is never double-counted. board is the whole-board index.
func BuildDepsForestStubbed(issues []bd.Issue, board map[string]bd.Issue) ([]*TreeNode, map[string]int) {
	visible := index(issues)
	boardBlockers := func(is bd.Issue) []string {
		var out []string
		for _, d := range is.Dependencies {
			if d.Type == "parent-child" || d.DependsOnID == "" || d.DependsOnID == is.ID {
				continue
			}
			if _, ok := board[d.DependsOnID]; ok {
				out = append(out, d.DependsOnID)
			}
		}
		return out
	}
	nest := func(is bd.Issue) string {
		b := boardBlockers(is)
		if len(b) == 0 {
			return ""
		}
		return b[0]
	}
	extra := map[string]int{}
	for _, is := range issues {
		b := boardBlockers(is)
		if len(b) <= 1 {
			continue
		}
		n := 0
		for _, id := range b[1:] {
			if _, ok := visible[id]; ok {
				n++
			}
		}
		if n > 0 {
			extra[is.ID] = n
		}
	}
	return buildStubbedForest(issues, board, nest), extra
}

// buildStubbedForest is the shared core for the forward forests (hierarchy and
// deps) with hidden-ancestor stubs. nest(is) returns the single id under which
// `is` nests (its parent, or its first blocker), resolved against board (the
// whole board, visible ∪ hidden) — "" when `is` is a root. When that id is not
// in the visible `issues` set but IS on the board, it is materialized as a
// dimmed stub node and the walk continues up the hidden chain, so a visible node
// whose ancestor is hidden stays rooted under its lineage rather than promoted to
// a fake root. Cycle-safe by the same deterministic chain break as
// assembleForest; every sibling list sorts by priority then id.
func buildStubbedForest(issues []bd.Issue, board map[string]bd.Issue, nest func(bd.Issue) string) []*TreeNode {
	real := make(map[string]*TreeNode, len(issues))
	order := make([]string, 0, len(issues))
	for i := range issues {
		real[issues[i].ID] = &TreeNode{Issue: issues[i]}
		order = append(order, issues[i].ID)
	}
	stub := map[string]*TreeNode{}
	parentOf := map[string]string{}

	// Resolve each visible node's chain up to its nearest visible ancestor (or a
	// root), materializing a stub for every hidden link along the way.
	for _, is := range issues {
		child := is.ID
		pid := nest(is)
		seen := map[string]bool{child: true}
		for pid != "" && !seen[pid] {
			if _, vis := real[pid]; vis {
				parentOf[child] = pid
				break
			}
			pis, ok := board[pid]
			if !ok {
				break // ancestor not on the board at all → child stays a root
			}
			if _, made := stub[pid]; !made {
				stub[pid] = &TreeNode{Issue: pis, Stub: true}
			}
			parentOf[child] = pid
			seen[pid] = true
			child = pid
			pid = nest(pis)
		}
	}

	nodes := make(map[string]*TreeNode, len(real)+len(stub))
	for id, n := range real {
		nodes[id] = n
	}
	for id, n := range stub {
		nodes[id] = n
	}

	// Break any cycle deterministically (mirrors assembleForest): the edge that
	// first revisits a node on a chain walk is cut, making that node a root.
	ids := make([]string, 0, len(nodes))
	for id := range nodes {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		seen := map[string]bool{id: true}
		cur := id
		for {
			next, ok := parentOf[cur]
			if !ok {
				break
			}
			if seen[next] {
				delete(parentOf, cur)
				break
			}
			seen[next] = true
			cur = next
		}
	}

	var roots []*TreeNode
	attach := func(id string) {
		n := nodes[id]
		if p, ok := parentOf[id]; ok {
			nodes[p].Children = append(nodes[p].Children, n)
		} else {
			roots = append(roots, n)
		}
	}
	for _, id := range order { // visible nodes, in issue order
		attach(id)
	}
	stubIDs := make([]string, 0, len(stub))
	for id := range stub {
		stubIDs = append(stubIDs, id)
	}
	sort.Strings(stubIDs)
	for _, id := range stubIDs {
		attach(id)
	}
	sortForest(roots)
	return roots
}

// BuildDepsForest nests issues under what BLOCKS them (non-parent-child
// dependency edges). An issue with several blockers hangs under its first;
// the extra count is reported per issue id (rendered as "+n deps").
// Cycle-safe: the edge that would close a cycle is dropped, making that
// issue a root.
func BuildDepsForest(issues []bd.Issue) ([]*TreeNode, map[string]int) {
	byID := index(issues)
	parentOf := map[string]string{}
	extra := map[string]int{}
	ids := make([]string, 0, len(issues))
	for _, is := range issues {
		ids = append(ids, is.ID)
		var blockers []string
		for _, d := range is.Dependencies {
			if d.Type == "parent-child" || d.DependsOnID == "" || d.DependsOnID == is.ID {
				continue
			}
			if _, ok := byID[d.DependsOnID]; ok {
				blockers = append(blockers, d.DependsOnID)
			}
		}
		if len(blockers) == 0 {
			continue
		}
		parentOf[is.ID] = blockers[0]
		if len(blockers) > 1 {
			extra[is.ID] = len(blockers) - 1
		}
	}

	// Break cycles deterministically: walk each chain; the edge that first
	// revisits a node is cut.
	sort.Strings(ids)
	for _, id := range ids {
		seen := map[string]bool{id: true}
		cur := id
		for {
			next, ok := parentOf[cur]
			if !ok {
				break
			}
			if seen[next] {
				delete(parentOf, cur)
				break
			}
			seen[next] = true
			cur = next
		}
	}

	nodes := make(map[string]*TreeNode, len(issues))
	for _, is := range issues {
		nodes[is.ID] = &TreeNode{Issue: is}
	}
	var roots []*TreeNode
	for _, is := range issues {
		if p, ok := parentOf[is.ID]; ok {
			nodes[p].Children = append(nodes[p].Children, nodes[is.ID])
		} else {
			roots = append(roots, nodes[is.ID])
		}
	}
	sortForest(roots)
	return roots, extra
}

// BuildDepsForestReverse is the mirror of BuildDepsForest: it nests each issue
// under what it BLOCKS rather than under what blocks it. The nesting edge is the
// reverse-dependency (an issue X hangs under the first issue Y that X blocks —
// Y is a dependent of X); an issue that blocks several hangs under its first,
// the extra count reported per id (rendered "+n deps"). Cycle-safe by the same
// deterministic chain walk as BuildDepsForest.
func BuildDepsForestReverse(issues []bd.Issue) ([]*TreeNode, map[string]int) {
	byID := index(issues)
	// blocks[x] = the issues x blocks (its dependents), in issue order — the
	// reverse of BuildDepsForest's per-issue blocker list.
	blocks := map[string][]string{}
	for _, is := range issues {
		for _, d := range is.Dependencies {
			if d.Type == "parent-child" || d.DependsOnID == "" || d.DependsOnID == is.ID {
				continue
			}
			if _, ok := byID[d.DependsOnID]; ok {
				blocks[d.DependsOnID] = append(blocks[d.DependsOnID], is.ID)
			}
		}
	}

	parentOf := map[string]string{}
	extra := map[string]int{}
	for _, is := range issues {
		blocked := blocks[is.ID]
		if len(blocked) == 0 {
			continue
		}
		parentOf[is.ID] = blocked[0]
		if len(blocked) > 1 {
			extra[is.ID] = len(blocked) - 1
		}
	}

	return assembleForest(issues, parentOf), extra
}

// BuildTreeReverse inverts the parent-child edge: it nests each ancestor under a
// descendant instead of the reverse. Leaves become roots and the original roots
// sink to the deepest nesting. A parent with several children hangs under its
// first child, the extra count reported per id (like the deps forest). Cycle-
// safe by the same chain walk.
func BuildTreeReverse(issues []bd.Issue) ([]*TreeNode, map[string]int) {
	byID := index(issues)
	// childrenOf[p] = the in-board children of p, in issue order.
	childrenOf := map[string][]string{}
	for _, is := range issues {
		if is.Parent == "" || is.Parent == is.ID {
			continue
		}
		if _, ok := byID[is.Parent]; ok {
			childrenOf[is.Parent] = append(childrenOf[is.Parent], is.ID)
		}
	}

	parentOf := map[string]string{}
	extra := map[string]int{}
	for _, is := range issues {
		kids := childrenOf[is.ID]
		if len(kids) == 0 {
			continue
		}
		parentOf[is.ID] = kids[0]
		if len(kids) > 1 {
			extra[is.ID] = len(kids) - 1
		}
	}

	return assembleForest(issues, parentOf), extra
}

// assembleForest builds the node forest from a parentOf edge map, breaking any
// cycle deterministically (the edge that first revisits a node on a chain walk
// is cut, making that node a root) and sorting every sibling list by priority
// then id. Shared by the two reversed builders and mirrors BuildDepsForest's
// tail.
func assembleForest(issues []bd.Issue, parentOf map[string]string) []*TreeNode {
	ids := make([]string, 0, len(issues))
	for _, is := range issues {
		ids = append(ids, is.ID)
	}
	sort.Strings(ids)
	for _, id := range ids {
		seen := map[string]bool{id: true}
		cur := id
		for {
			next, ok := parentOf[cur]
			if !ok {
				break
			}
			if seen[next] {
				delete(parentOf, cur)
				break
			}
			seen[next] = true
			cur = next
		}
	}

	nodes := make(map[string]*TreeNode, len(issues))
	for _, is := range issues {
		nodes[is.ID] = &TreeNode{Issue: is}
	}
	var roots []*TreeNode
	for _, is := range issues {
		if p, ok := parentOf[is.ID]; ok {
			nodes[p].Children = append(nodes[p].Children, nodes[is.ID])
		} else {
			roots = append(roots, nodes[is.ID])
		}
	}
	sortForest(roots)
	return roots
}

func sortForest(list []*TreeNode) {
	sort.SliceStable(list, func(a, b int) bool {
		return lessByPriorityThenID(list[a].Issue, list[b].Issue)
	})
	for _, n := range list {
		sortForest(n.Children)
	}
}

// --- sorting (view-spec) ---

// Sort is one ordering: a key and a direction.
type Sort struct {
	Key  SortKey
	Desc bool
}

// issueLess compares two issues by a FLAT sort key (priority/updated/created/
// title/id), respecting desc. Ties break by id ascending for stability.
func issueLess(a, b bd.Issue, key SortKey, desc bool) bool {
	cmp := 0
	switch key {
	case SortUpdated:
		cmp = strings.Compare(a.UpdatedAt, b.UpdatedAt)
	case SortCreated:
		cmp = strings.Compare(a.CreatedAt, b.CreatedAt)
	case SortTitle:
		cmp = strings.Compare(strings.ToLower(a.Title), strings.ToLower(b.Title))
	case SortID:
		cmp = strings.Compare(a.ID, b.ID)
	default: // SortPriority
		cmp = a.Priority - b.Priority
	}
	if cmp == 0 {
		return a.ID < b.ID // stable tiebreak
	}
	if desc {
		return cmp > 0
	}
	return cmp < 0
}

// SortColumns orders each section's issues by the flat key, and (ancestor mode
// only) reorders the sections by the hierarchical key over each section's
// members — subtree-size = member count, aggregate-priority = highest priority
// present, open-count = unfinished members, recent-activity = latest update.
// The synthetic "standalone" bucket always stays last. Status/blockers/type
// column order is fixed by Rollup and left untouched.
func SortColumns(cols []Column, mode Mode, flat, tree Sort) {
	for i := range cols {
		items := cols[i].Issues
		sort.SliceStable(items, func(a, b int) bool { return issueLess(items[a], items[b], flat.Key, flat.Desc) })
	}
	if mode != ModeRoot {
		return
	}
	metric := func(c Column) (float64, string) {
		switch tree.Key {
		case SortAggPriority:
			best := 5
			for _, is := range c.Issues {
				if is.Priority < best {
					best = is.Priority
				}
			}
			return float64(-best), "" // higher priority (lower number) ⇒ larger metric
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
		default: // SortSubtreeSize / SortMaxDepth (no depth in a flat rollup ⇒ size)
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

// TreeMetrics are the memoized whole-subtree measurements used to order tree
// siblings hierarchically (computed once per forest build).
type treeMetric struct {
	size   int    // descendants including self
	aggPri int    // highest priority (lowest number) anywhere in the subtree
	open   int    // unfinished issues in the subtree
	depth  int    // deepest nesting below this node
	recent string // latest UpdatedAt in the subtree
}

// computeMetrics fills a per-node metric map for the whole forest (post-order).
func computeMetrics(roots []*TreeNode) map[string]treeMetric {
	m := map[string]treeMetric{}
	var visit func(n *TreeNode) treeMetric
	visit = func(n *TreeNode) treeMetric {
		var me treeMetric
		if n.Stub {
			// A stub is a hidden-ancestor placeholder: it contributes nothing of
			// its own (a large aggPri so it never wins the priority min); only its
			// real descendants roll up through it.
			me = treeMetric{aggPri: 1 << 30}
		} else {
			me = treeMetric{size: 1, aggPri: n.Issue.Priority, depth: 0, recent: n.Issue.UpdatedAt}
			if n.Issue.Status != bd.StatusClosed {
				me.open = 1
			}
		}
		for _, c := range n.Children {
			cm := visit(c)
			me.size += cm.size
			me.open += cm.open
			if cm.aggPri < me.aggPri {
				me.aggPri = cm.aggPri
			}
			if cm.depth+1 > me.depth {
				me.depth = cm.depth + 1
			}
			if cm.recent > me.recent {
				me.recent = cm.recent
			}
		}
		m[n.Issue.ID] = me
		return me
	}
	for _, r := range roots {
		visit(r)
	}
	return m
}

// SortForestBy orders every sibling list in the forest by a hierarchical key
// over precomputed subtree metrics. Cycle-safe (operates on the already-built
// spanning forest); ties break by priority-then-id for stability.
func SortForestBy(roots []*TreeNode, s Sort) {
	metrics := computeMetrics(roots)
	var sortList func(list []*TreeNode)
	sortList = func(list []*TreeNode) {
		sort.SliceStable(list, func(a, b int) bool {
			ma, mb := metrics[list[a].Issue.ID], metrics[list[b].Issue.ID]
			var va, vb int
			switch s.Key {
			case SortAggPriority:
				va, vb = -ma.aggPri, -mb.aggPri // higher priority ⇒ larger
			case SortOpenCount:
				va, vb = ma.open, mb.open
			case SortMaxDepth:
				va, vb = ma.depth, mb.depth
			case SortRecent:
				if ma.recent != mb.recent {
					if s.Desc {
						return ma.recent > mb.recent
					}
					return ma.recent < mb.recent
				}
				return lessByPriorityThenID(list[a].Issue, list[b].Issue)
			default: // SortSubtreeSize
				va, vb = ma.size, mb.size
			}
			if va != vb {
				if s.Desc {
					return va > vb
				}
				return va < vb
			}
			return lessByPriorityThenID(list[a].Issue, list[b].Issue)
		})
		for _, n := range list {
			sortList(n.Children)
		}
	}
	sortList(roots)
}
