package ui

// control.go is the facet-control widget: a small anchored dropdown that drives
// ONE viewState facet with live preview. It replaced the command palette — where
// the palette was one full-body list over every axis, a facetControl is a
// per-facet menu anchored under its strip segment in the header, composed OVER
// the board so the view never disappears behind a modal. The routing helpers the
// controls call (setTraverse/applyGroup/facetToMode/setActiveSort) live here too;
// set_view and the share stream drive the same helpers.
//
// The live-preview loop: on open, the whole viewState is cloned into snap; while
// the menu is open, highlighting an option runs its apply func (mutating the
// target field + rebuild), so the board re-renders BEHIND the menu immediately;
// enter commits (drops snap); esc writes snap back (the exact pre-open state,
// cursor included) and rebuilds. This mirrors nav.go's snapshot/restore, but the
// whole viewState is restored (not nav's selective subset) because a cancel must
// undo everything the preview touched.

import (
	"fmt"
	"io"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/awhitty/bb/internal/rollup"
)

// controlItem is one option in a facet control. label is shown; apply mutates
// the target viewState field and rebuilds, so highlighting the item previews
// the change live on the board behind the menu.
type controlItem struct {
	label string
	apply func(m *Model)
}

func (it controlItem) FilterValue() string { return it.label }

// controlDelegate renders one compact line per option — a focus bar + bold label
// on the selected row, two-space gutter otherwise — matching the palette's tight
// look (theme.go styles, no bubbles chrome).
type controlDelegate struct{}

func (d controlDelegate) Height() int                         { return 1 }
func (d controlDelegate) Spacing() int                        { return 0 }
func (d controlDelegate) Update(tea.Msg, *list.Model) tea.Cmd { return nil }
func (d controlDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	it, ok := item.(controlItem)
	if !ok {
		return
	}
	var line string
	if index == m.Index() {
		line = styFocusBar.Render("▌") + " " + styBold.Render(it.label)
	} else {
		line = "  " + it.label
	}
	fmt.Fprint(w, ansi.Truncate(line, max(1, m.Width()), "…"))
}

var styControlBox = lipgloss.NewStyle().
	Border(lipgloss.RoundedBorder()).
	BorderForeground(accent).
	Padding(0, 1)

// facetControl is a small anchored dropdown driving ONE facet with live preview.
// snap is the pre-open viewState (esc restores it); anchorX is the column its
// menu opens at (under its strip segment).
type facetControl struct {
	open    bool
	list    list.Model
	snap    viewState
	anchorX int
}

// newFacetControl builds a control's list (chrome suppressed, quit keys off, no
// fuzzy filter — it is a short menu, not a palette), snapshots the viewState for
// esc-revert, and anchors it at anchorX. selIdx pre-selects the current value so
// the menu opens on it and j/k step away.
func (m *Model) newFacetControl(items []list.Item, selIdx, anchorX int) facetControl {
	l := list.New(items, controlDelegate{}, 0, 0)
	l.SetShowTitle(false)
	l.SetShowStatusBar(false)
	l.SetShowPagination(false)
	l.SetShowHelp(false)
	l.SetFilteringEnabled(false)
	l.DisableQuitKeybindings()
	l.Select(clamp(selIdx, 0, max(0, len(items)-1)))

	w := 2 // the "▌ " / "  " gutter
	for _, it := range items {
		if ci, ok := it.(controlItem); ok {
			w = max(w, 2+lipgloss.Width(ci.label))
		}
	}
	w = clamp(w, 10, 40)
	bodyH := m.layoutScreen().Body.H
	h := clamp(len(items), 1, max(1, bodyH-2))
	l.SetSize(w, h)

	m.pushLayer(layerControl) // esc reverts the preview via cancelControl (layers.go)
	return facetControl{open: true, list: l, snap: m.viewState.clone(), anchorX: anchorX}
}

// setTraverse flips the nesting relation for the tree (depsTree) and the columns
// navigator (millerDeps) together — one "traverse by hierarchy/dependencies"
// choice across the relation views. It leaves the nesting direction (treeDir)
// untouched, so the set_view / share paths that never carried direction keep it;
// the traverse control uses setTraverseDir to move both axes at once.
func (m *Model) setTraverse(deps bool) {
	m.depsTree = deps
	m.millerDeps = deps
	m.millerSel = 0
	m.rebuild()
}

// applyGroup sets the group facet for the CURRENT view: the tree's segmentation,
// the swimlane's column axis, or the board/list grouping. An empty facet clears
// the override (no tree segmentation / status swim columns / mode-driven board).
func (m *Model) applyGroup(f rollup.Facet) {
	switch m.view {
	case ViewTree:
		m.treeFacet = f
	case ViewSwim:
		m.swimFacet = f
	default: // ViewList / ViewKanban
		m.boardFacet = f
		if mode, ok := facetToMode(f); ok {
			m.mode = mode // keep m.mode coherent for the four legacy facets (header/keys)
		}
	}
	m.rebuild()
}

// facetToMode maps the four legacy facets back to their kanban Mode; label and
// priority have no Mode (they group only through boardFacet).
func facetToMode(f rollup.Facet) (rollup.Mode, bool) {
	switch f {
	case rollup.FacetStatus:
		return rollup.ModeStatus, true
	case rollup.FacetType:
		return rollup.ModeType, true
	case rollup.FacetRoot:
		return rollup.ModeRoot, true
	case rollup.FacetBlockers:
		return rollup.ModeBlockers, true
	}
	return "", false
}

// setActiveSort sets the sort key for the current view — treeSort for the
// hierarchical views, flatSort otherwise — at its default direction.
func (m *Model) setActiveSort(k rollup.SortKey) {
	s := rollup.Sort{Key: k, Desc: rollup.DefaultDesc(k)}
	if m.hierarchicalView() {
		m.treeSort = s
	} else {
		m.flatSort = s
	}
	m.rebuild()
}

// openSortControl opens the sort control: an anchored dropdown over the sort keys
// for the current view (tree keys for a hierarchical view, flat keys otherwise),
// pre-selected on the active key. It anchors under the header's sort segment and
// drives setActiveSort with live preview.
func (m *Model) openSortControl() {
	keys := rollup.FlatSortKeys
	cur := m.flatSort.Key
	if m.hierarchicalView() {
		keys = rollup.TreeSortKeys
		cur = m.treeSort.Key
	}
	items := make([]list.Item, len(keys))
	selIdx := 0
	for i, k := range keys {
		k := k
		items[i] = controlItem{label: string(k), apply: func(m *Model) { m.setActiveSort(k) }}
		if k == cur {
			selIdx = i
		}
	}
	m.control = m.newFacetControl(items, selIdx, m.sortSegmentX())
}

// groupOptions is the grouper's full facet set: rollup.FacetBindings, the ONE
// canonical facet registry (none first, single-valued facets, depth last) shared
// with set_view resolution and the MCP validation. A groupOption carries its
// strip label (Name, in the modeLabels/facetLabels vocabulary) and the facet it
// routes into applyGroup; the empty facet is "none" — no tree segmentation,
// status swim columns, the mode-driven board.
type groupOption = rollup.FacetBinding

var groupOptions = rollup.FacetBindings

// OfferedFacets is the facet set the grouper and break-out-lane controls offer,
// in dropdown order. It exists so the MCP's anti-drift test can assert set_view
// accepts exactly the facets the UI presents (both derive from
// rollup.FacetBindings, so neither can drift from the other).
func OfferedFacets() []rollup.Facet {
	facets := make([]rollup.Facet, len(groupOptions))
	for i, opt := range groupOptions {
		facets[i] = opt.Facet
	}
	return facets
}

// activeFacet is the grouping facet the current view is displaying — the value
// the grouper control opens pre-selected on. The board derives it from its mode
// when no boardFacet override is set; the tree reads its segmentation facet (""
// = none), the swim its column-axis facet (default status).
func (m Model) activeFacet() rollup.Facet {
	switch m.view {
	case ViewTree:
		return m.treeFacet
	case ViewSwim:
		if m.swimFacet == "" {
			return rollup.FacetStatus
		}
		return m.swimFacet
	default: // list / kanban
		if m.boardFacet == "" {
			return rollup.FacetForMode(m.mode)
		}
		return m.boardFacet
	}
}

// openGroupControl opens the grouper: an anchored dropdown over the group
// facets, pre-selected on the active view's current facet and anchored under the
// header's grouper segment. It drives applyGroup with live preview, so the board
// re-segments (boardFacet), the tree re-segments (treeFacet), or the swim column
// axis changes (swimFacet) behind the still-open menu.
func (m *Model) openGroupControl() {
	cur := m.activeFacet()
	items := make([]list.Item, len(groupOptions))
	selIdx := 0
	for i, opt := range groupOptions {
		opt := opt
		items[i] = controlItem{label: opt.Name, apply: func(m *Model) { m.applyGroup(opt.Facet) }}
		if opt.Facet == cur {
			selIdx = i
		}
	}
	m.control = m.newFacetControl(items, selIdx, m.groupSegmentX())
}

// applyLane sets the kanban's break-out lane facet — the SECOND axis that splits
// each board column into stacked lane bands. An empty facet clears it back to the
// flat 1D board. Distinct from applyGroup (the column axis): lanes and grouper are
// two independent facets on the same board.
func (m *Model) applyLane(f rollup.Facet) {
	m.boardLane = f
	m.rebuild()
}

// openLaneControl opens the break-out lane control: an anchored dropdown over the
// same seven facet choices as the grouper, pre-selected on the current lane facet
// ("none" when the board is flat) and anchored under the header's lanes segment.
// It drives applyLane with live preview, so the board splits into (or collapses
// out of) lane bands behind the still-open menu; esc reverts, "none" clears it.
func (m *Model) openLaneControl() {
	cur := m.boardLane
	items := make([]list.Item, len(groupOptions))
	selIdx := 0
	for i, opt := range groupOptions {
		opt := opt
		items[i] = controlItem{label: opt.Name, apply: func(m *Model) { m.applyLane(opt.Facet) }}
		if opt.Facet == cur {
			selIdx = i
		}
	}
	m.control = m.newFacetControl(items, selIdx, m.laneSegmentX())
}

// setTraverseDir sets BOTH tree-traverse axes at once: the relation (depsTree,
// mirrored to the columns navigator's millerDeps so the two views share it) and
// the nesting direction (treeDir). One rebuild reflects the pair. The relation-
// only setTraverse leaves direction untouched for the set_view / share paths that
// never carried it.
func (m *Model) setTraverseDir(deps, dir bool) {
	m.depsTree = deps
	m.millerDeps = deps
	m.millerSel = 0
	m.treeDir = dir
	m.rebuild()
}

// openTraverseControl opens the tree-traverse control: one anchored menu over
// BOTH axes — the relation (hierarchy ↔ dependencies) and the direction (forward
// ↓ ↔ invert ↑). In the tree it offers the four relation×direction combinations,
// pre-selected on the active pair, driving setTraverseDir with live preview
// (inverting reverses the nesting behind the still-open menu). In the columns
// navigator only the relation applies, so it offers the two relations alone.
func (m *Model) openTraverseControl() {
	var items []list.Item
	selIdx := 0
	if m.view == ViewColumns {
		opts := []struct {
			label string
			deps  bool
		}{{"hierarchy", false}, {"dependencies", true}}
		for i, o := range opts {
			o := o
			items = append(items, controlItem{label: o.label, apply: func(m *Model) { m.setTraverseDir(o.deps, m.treeDir) }})
			if o.deps == m.millerDeps {
				selIdx = i
			}
		}
	} else {
		opts := []struct {
			label     string
			deps, dir bool
		}{
			{"hierarchy ↓", false, false},
			{"hierarchy ↑", false, true},
			{"dependencies ↓", true, false},
			{"dependencies ↑", true, true},
		}
		for i, o := range opts {
			o := o
			items = append(items, controlItem{label: o.label, apply: func(m *Model) { m.setTraverseDir(o.deps, o.dir) }})
			if o.deps == m.depsTree && o.dir == m.treeDir {
				selIdx = i
			}
		}
	}
	m.control = m.newFacetControl(items, selIdx, m.traverseSegmentX())
}

// previewControl runs the highlighted option's apply func — mutating the target
// field and rebuilding, so the board behind the open menu re-renders live.
func (m *Model) previewControl() {
	if it, ok := m.control.list.SelectedItem().(controlItem); ok {
		it.apply(m)
	}
}

// commitControl closes the control keeping the previewed change (the field is
// already mutated by the last preview); the snapshot is dropped, and so is the
// control's esc-layer entry — commit keeps the change, so there is nothing left
// for a later esc to revert.
func (m *Model) commitControl() {
	m.control.open = false
	if n := len(m.layers); n > 0 && m.layers[n-1] == layerControl {
		m.layers = m.layers[:n-1]
	}
}

// cancelControl closes the control and restores the exact pre-open viewState
// (sort and cursor included), then rebuilds so the board matches.
func (m *Model) cancelControl() {
	m.viewState = m.control.snap
	m.control.open = false
	m.rebuild()
}

// handleControlKey drives the open facet control: esc reverts, enter commits,
// j/k (and arrows) move the selection and re-preview. Every other key is
// swallowed so the board underneath never sees it while the menu is open.
func (m Model) handleControlKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		// The control is the top esc layer (it is modal — it swallows every key
		// while open), so peel it through the one stack: dismissLayer runs
		// cancelControl, reverting the preview to the pre-open viewState.
		m.popLayer()
		return m, nil
	case "enter":
		m.commitControl()
		return m, nil
	case "j", "down", "k", "up":
		var cmd tea.Cmd
		m.control.list, cmd = m.control.list.Update(msg)
		m.previewControl()
		return m, cmd
	}
	return m, nil
}

// overlayControl composites the open control's menu onto the body, anchored at
// its strip segment along the top edge — the board renders normally and the menu
// sits over its top-left, never replacing the whole body.
func (m Model) overlayControl(body string) string {
	box := styControlBox.Render(m.control.list.View())
	boxW := lipgloss.Width(box)
	col := m.control.anchorX
	if col+boxW > m.width {
		col = max(0, m.width-boxW)
	}
	return overlayBox(body, box, 0, col, m.width)
}

// overlayBox splices box onto base starting at (row, col): each box line replaces
// the underlying cells of the base line at that column, leaving the base (the
// board) visible on either side and above/below. ANSI-aware, so styled board
// lines are cut on cell boundaries; the composed line is clamped to w columns.
func overlayBox(base, box string, row, col, w int) string {
	const reset = "\x1b[0m"
	baseLines := strings.Split(base, "\n")
	boxLines := strings.Split(box, "\n")
	for i, bl := range boxLines {
		y := row + i
		if y < 0 || y >= len(baseLines) {
			continue
		}
		orig := baseLines[y]
		left := padVisual(ansi.Truncate(orig, col, ""), col)
		boxW := ansi.StringWidth(bl)
		right := ""
		if rightStart := col + boxW; ansi.StringWidth(orig) > rightStart {
			right = ansi.TruncateLeft(orig, rightStart, "")
		}
		baseLines[y] = ansi.Truncate(left+reset+bl+right, w, "")
	}
	return strings.Join(baseLines, "\n")
}

// padVisual pads s with spaces to visual width n (ANSI-aware, unlike padRight's
// byte-length pad — the board lines carry styling). s is assumed no wider than n.
func padVisual(s string, n int) string {
	if d := n - ansi.StringWidth(s); d > 0 {
		return s + strings.Repeat(" ", d)
	}
	return s
}
