package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/awhitty/bb/internal/agentapi"
	"github.com/awhitty/bb/internal/bd"
	"github.com/awhitty/bb/internal/nlq"
	"github.com/awhitty/bb/internal/rollup"
)

// This file is the TUI half of the MCP contract: agents drive the VISIBLE
// interface. Every arranging verb answers with a physical description of the
// layout — what is literally on screen — and the human outranks the agent:
// esc dismisses agent arrangements, and every agent action leaves a footer
// notice.

// attachSlot is the ATTACH layer's state: the ONE session channel the human is
// attached to (session), the agent-driven arrangement it applies, and prev — the
// human's viewState from the moment they attached, restored exactly when they
// detach (esc). The attach is sticky — local moves under it are ephemeral within
// the layer. Only one channel is attached at a time; attaching another replaces it.
type attachSlot struct {
	active  bool
	session string // the attached channel's Claude Code SessionID ("" = the unattributed default)
	title   string
	ids     []string
	query   string
	prev    viewState
}

// attachedTo reports whether the human is attached to the given session's channel
// — the gate for applying that channel's pushes to the board live.
func (m Model) attachedTo(session string) bool {
	return m.attach.active && m.attach.session == session
}

// attachTo attaches the human to a channel and applies its face live — the A/enter
// action in the sessions browser. The first attach snapshots the human's view (the
// detach restore point) and raises the attach layer; switching from another channel
// restores the human's base first, so the new channel applies over it with no
// residual arrangement (latest-wins). prev always stays the human's own pre-attach
// view, so esc detaches back to exactly there.
func (m *Model) attachTo(session string, face share) tea.Cmd {
	if m.attach.active && m.attach.session != session {
		m.restoreAttach(m.attach.prev) // undo the prior channel back to the human's base
		m.emphasis = nil
		m.remarks = nil // the prior channel's remarks clear; the new share restores its own
		m.attach.title, m.attach.ids, m.attach.query = "", nil, ""
	}
	if !m.attach.active {
		m.attach.prev = m.snapshotAttach()
		m.attach.active = true
		m.pushLayer(layerAttach) // esc detaches (sticky attach; layers.go, input.go)
	}
	m.attach.session = session
	return m.applyShare(face)
}

// snapshotAttach captures the human's view for the attach slot to restore. It is a
// full viewState clone; restoreAttach writes back only the attach scope.
func (m Model) snapshotAttach() viewState {
	return m.viewState.clone()
}

// restoreAttach writes back every ARRANGEMENT knob an attached channel can drive —
// the filter, view + grouping/mode, the relationship roots/drill, the traverse
// (deps/direction), the group/lane facets, the relationship-focus scope, and the
// sort/collapse/min-subtree spec — so detaching (or switching channels) leaves NO
// residual agent state. It deliberately leaves the human's focus cursor, panel, and
// open detail alone, so detaching keeps whatever surface they had.
func (m *Model) restoreAttach(vs viewState) {
	m.activeQuery = vs.activeQuery
	m.mode = vs.mode
	m.setView(vs.view)
	m.swimRoot = vs.swimRoot
	m.swimFacet = vs.swimFacet
	m.millerPath = append([]string(nil), vs.millerPath...)
	m.millerSel = vs.millerSel
	m.millerDeps = vs.millerDeps
	m.depsTree = vs.depsTree
	m.treeDir = vs.treeDir
	m.boardFacet = vs.boardFacet
	m.treeFacet = vs.treeFacet
	m.boardLane = vs.boardLane
	m.scopeRoot = vs.scopeRoot
	m.flatSort = vs.flatSort
	m.treeSort = vs.treeSort
	m.minSubtree = vs.minSubtree
	m.collapsed = vs.collapsed
	if m.collapsed == nil {
		m.collapsed = map[string]bool{}
	}
}

// handleAgent executes one agent action on the UI goroutine.
func (m *Model) handleAgent(action agentapi.Action) (agentapi.Response, tea.Cmd) {
	switch a := action.(type) {
	case agentapi.ViewAction:
		return m.agentView(), nil

	case agentapi.ShowAction:
		// Publish into the agent-shares stream; the human pulls with @ / attaches
		// with A. Apply live only when they are attached to this session's channel.
		s := m.publishShare(share{Kind: shareViewKind, Name: a.Title, SessionID: a.Session, Spec: specFromShow(a)})
		if !m.attachedTo(a.Session) {
			m.sharesFooterNotice(s)
			return m.agentView(), nil
		}
		m.attach.title = a.Title
		m.attach.ids = a.IDs
		m.attach.query = a.Query
		m.setRemarks(a.Remarks) // ephemeral issue-keyed notes on this view
		if a.Query != "" {
			m.activeQuery = a.Query
			m.issues = a.Issues
			m.rebuildIndexes()
		}
		switch a.Mode {
		case "tree":
			m.setView(ViewTree)
			m.modeBy = "agent"
		case string(rollup.ModeStatus), string(rollup.ModeType), string(rollup.ModeRoot), string(rollup.ModeBlockers):
			if m.view == ViewTree {
				m.setView(ViewKanban) // leaving the tree for a mode-grouped board
			}
			m.mode = rollup.Mode(a.Mode)
			m.modeBy = "agent"
		}
		m.filterBy = "agent"
		m.rebuild()
		m.colIdx, m.rowIdx, m.treeIdx = 0, 0, 0
		what := a.Title
		if what == "" {
			what = describeArrangement(a)
		}
		m.setMessage("agent: showing "+what+" — esc returns to your view", false)
		missing := m.missingFromBoard(a.IDs)
		resp := m.agentView()
		if len(missing) > 0 {
			resp.Text = fmt.Sprintf("note: %d requested id(s) not on the loaded board (%s)\n\n",
				len(missing), strings.Join(missing, ", ")) + resp.Text
		}
		return resp, nil

	case agentapi.SelectAction:
		if !m.jumpTo(a.ID) {
			return agentapi.Response{
				Err: fmt.Sprintf("%s is not in the current view — call show() to arrange it on screen, or reset() to return to the user's view", a.ID),
			}, nil
		}
		if a.Panel != nil {
			m.panelOpen = *a.Panel
			if !m.panelOpen {
				m.panelFocus = false
			}
		}
		m.setMessage("agent: selected "+a.ID, false)
		return m.agentView(), nil

	case agentapi.ResetAction:
		cmd := m.detach()
		m.setMessage("agent: returned to your view", false)
		resp := m.agentView()
		if cmd != nil {
			resp.Text = "note: the user's query filter is reloading; call view() again for the settled board\n\n" + resp.Text
		}
		return resp, cmd

	case agentapi.RefreshAction:
		m.issues = a.Issues
		m.graph = a.Issues // refresh carries the whole board (List(true))
		m.rebuildIndexes()
		m.panelMD.id = ""
		m.rebuild()
		if m.activeQuery == "" {
			m.vocab = nlq.DeriveVocab(a.Issues)
			m.boardCtx = nlq.BoardContext(a.Issues)
			m.analystWarmed = false
		}
		m.setMessage("agent: refreshed the board", false)
		return m.agentView(), nil

	case agentapi.ReprioritizeAction:
		is, ok := m.byID[a.ID]
		if !ok {
			return agentapi.Response{Err: a.ID + " is not on the loaded board"}, nil
		}
		next := clamp(a.Priority, priMin, priMax)
		if next == is.Priority {
			m.setMessage(fmt.Sprintf("agent: %s already P%d", a.ID, next), false)
			return m.agentView(), nil
		}
		// Optimistic, exactly like the human's +/- : paint now, sync via bd,
		// reload to confirm. Fold the edit into the signature so the watcher's
		// reconciling refresh stays silent.
		for i := range m.issues {
			if m.issues[i].ID == a.ID {
				m.issues[i].Priority = next
			}
		}
		for i := range m.graph {
			if m.graph[i].ID == a.ID {
				m.graph[i].Priority = next
			}
		}
		m.rebuild()
		m.boardSig = boardSignature(m.issues)
		m.setMessage(fmt.Sprintf("agent: %s → P%d", a.ID, next), false)
		client, logger, id := m.client, m.logger, a.ID
		cmd := func() tea.Msg {
			err := client.SetPriority(id, next)
			if err != nil {
				logger.Error("agent priority", "id", id, "err", err)
			}
			return prioritySyncedMsg{id: id, err: err}
		}
		return m.agentView(), cmd

	case agentapi.EmphasizeAction:
		s := m.publishShare(share{Kind: shareViewKind, SessionID: a.Session, Spec: shareSpec{Emphasis: a.Targets}})
		if !m.attachedTo(a.Session) {
			m.sharesFooterNotice(s)
			return m.agentView(), nil
		}
		m.setEmphasis(a.Targets)
		m.setDetailContent() // the detail viewport is cached; re-render for section marks
		what := describeEmphasis(a.Targets)
		m.setMessage("agent: emphasizing "+what+" — esc clears", false)
		return m.agentView(), nil

	case agentapi.ClearEmphasisAction:
		m.clearEmphasis()
		m.setDetailContent()
		m.setMessage("agent: emphasis cleared", false)
		return m.agentView(), nil

	case agentapi.SpecAction:
		s := m.publishShare(share{Kind: shareViewKind, Name: a.Title, SessionID: a.Session, Spec: specFromSpecAction(a)})
		if !m.attachedTo(a.Session) {
			m.sharesFooterNotice(s)
			return m.agentView(), nil
		}
		return m.applySpec(a)

	case agentapi.NameDropAction:
		m.ingestNameDrop(a)
		return m.agentView(), nil

	case agentapi.SessionEndAction:
		m.archiveSession(a.SessionID)
		return m.agentView(), nil
	}
	return agentapi.Response{Err: "unknown action"}, nil
}

// applySpec sets the view-spec knobs an agent drives (mode/filter/sort/
// collapse/threshold). Only the provided fields change; the rest are left
// alone. Snapshots the user's state so esc/reset restores it.
func (m *Model) applySpec(a agentapi.SpecAction) (agentapi.Response, tea.Cmd) {
	deps, hasTrav := traverseDeps(a.Traverse)
	switch a.Mode {
	case "list":
		m.setView(ViewList)
		m.modeBy = "agent"
	case "kanban":
		m.setView(ViewKanban)
		m.modeBy = "agent"
	case "relationship":
		// Root at the requested bead, else the current focus. If it isn't on the
		// loaded board, say so rather than opening an empty board.
		root := a.Root
		if root == "" {
			if is := m.focusedIssue(); is != nil {
				root = is.ID
			}
		}
		if root != "" {
			if _, ok := m.byID[root]; !ok {
				return agentapi.Response{Err: root + " is not on the loaded board — refresh() or pass a root that exists"}, nil
			}
		}
		m.enterSwimRooted(root)
		m.modeBy = "agent"
	case "columns":
		// Miller-columns navigator; an optional root pre-drills to that bead. The
		// traverse (if given) picks the drill relation; else keep the current one.
		if a.Root != "" {
			if _, ok := m.byID[a.Root]; !ok {
				return agentapi.Response{Err: a.Root + " is not on the loaded board — refresh() or pass a root that exists"}, nil
			}
		}
		d := m.millerDeps
		if hasTrav {
			d = deps
		}
		m.enterColumns(a.Root, d)
		m.modeBy = "agent"
	case "tree":
		m.setView(ViewTree)
		m.modeBy = "agent"
	case string(rollup.ModeStatus), string(rollup.ModeType), string(rollup.ModeRoot), string(rollup.ModeBlockers):
		if m.view != ViewList && m.view != ViewKanban {
			m.setView(ViewKanban)
		}
		m.mode, m.modeBy = rollup.Mode(a.Mode), "agent"
	}
	// The traverse (tree deps↔hierarchy) applies after the view is set. columns
	// already threaded it through enterColumns, so skip the redundant re-apply
	// (which would reset the pre-drilled selection).
	if hasTrav && a.Mode != "columns" {
		m.setTraverse(deps)
	}
	// The group facet applies to whatever view is now active (tree segmentation,
	// swim column axis, or board/list grouping).
	if a.Group != "" {
		if f, ok := facetFromName(a.Group); ok {
			m.applyGroup(f)
		}
	}
	// The break-out lane is the kanban's SECOND facet axis (boardLane); "none"
	// clears it back to the flat 1D board. It is a board rendering, so it lands
	// regardless of the current view and takes effect once the board is shown.
	if a.Lane != "" {
		if f, ok := facetFromName(a.Lane); ok {
			m.applyLane(f)
		}
	}
	// The relationship-focus scope narrows every mode to a bead's neighborhood.
	// Validate the root is on the loaded board before scoping, else say so rather
	// than scoping to an empty neighborhood.
	if a.Scope != "" {
		if _, ok := m.byID[a.Scope]; !ok {
			return agentapi.Response{Err: a.Scope + " is not on the loaded board — refresh() or pass a scope that exists"}, nil
		}
		m.enterScope(a.Scope)
	}
	if a.Query != nil {
		m.activeQuery = *a.Query
		if *a.Query != "" {
			m.issues = a.Issues
			m.graph = a.Issues
			m.rebuildIndexes()
		}
		m.attach.query = *a.Query
		m.filterBy = "agent"
	}
	if a.IDs != nil {
		m.attach.ids = a.IDs
		m.filterBy = "agent"
	}
	if a.Title != "" {
		m.attach.title = a.Title
	}
	if a.SortKey != "" {
		desc := rollup.DefaultDesc(rollup.SortKey(a.SortKey))
		switch a.SortDir {
		case "asc":
			desc = false
		case "desc":
			desc = true
		}
		s := rollup.Sort{Key: rollup.SortKey(a.SortKey), Desc: desc}
		if isFlatKey(a.SortKey) {
			m.flatSort = s
		} else {
			m.treeSort = s
		}
	}
	if a.Collapse != nil {
		switch {
		case a.Collapse.ExpandAll:
			m.collapsed = map[string]bool{}
		case a.Collapse.Level != nil && *a.Collapse.Level == 0:
			m.collapseToRoots()
		}
		for _, id := range a.Collapse.NodeIDs {
			m.collapsed[id] = true
		}
	}
	if a.MinSubtree != nil {
		m.minSubtree = *a.MinSubtree
	}
	m.setRemarks(a.Remarks) // ephemeral issue-keyed notes on this view
	m.rebuild()
	m.setDetailContent() // the detail viewport is cached; re-render so a remark shows on an open bead
	m.setMessage("agent: adjusted the view — esc restores yours", false)
	return m.agentView(), nil
}

// traverseDeps maps the algebra's traverse token to the deps bool the tree and
// columns share. ok=false for an empty token (leave the current relation).
func traverseDeps(s string) (deps, ok bool) {
	switch s {
	case "deps", "dependencies":
		return true, true
	case "hierarchy":
		return false, true
	}
	return false, false
}

// facetFromName maps a group/lane token to a rollup.Facet via rollup.FacetFromName
// — the ONE canonical facet registry the grouper control, this spec resolution,
// and the MCP validation all share, so the vocabularies cannot drift. "none"
// clears the override (facet ""); ok is false for an unknown token so the caller
// can leave the grouping untouched.
func facetFromName(s string) (rollup.Facet, bool) {
	return rollup.FacetFromName(s)
}

func isFlatKey(k string) bool {
	for _, f := range rollup.FlatSortKeys {
		if string(f) == k {
			return true
		}
	}
	return false
}

func describeEmphasis(t []agentapi.Emphasis) string {
	if len(t) == 0 {
		return "nothing"
	}
	style := t[0].Style
	if t[0].Label != "" {
		return fmt.Sprintf("%d target(s) [%s] — %s", len(t), style, t[0].Label)
	}
	return fmt.Sprintf("%d target(s) [%s]", len(t), style)
}

// detach drops the attach layer and restores the human's EXACT pre-attach view.
// Returns a reload Cmd when the human's query filter must be refetched. esc and the
// agent's reset() route here. The attach is sticky, so any cursor/paging/panel move
// the human made while attached is ephemeral within the layer: detach restores the
// pre-attach cursor and panel too (restoreAttach handles the arrangement knobs),
// leaving the surface exactly as it was before the attach.
func (m *Model) detach() tea.Cmd {
	if !m.attach.active {
		return nil
	}
	prev := m.attach.prev
	needReload := m.attach.query != "" && prev.activeQuery != m.attach.query
	m.restoreAttach(prev)
	m.colIdx, m.rowIdx, m.treeIdx = prev.colIdx, prev.rowIdx, prev.treeIdx
	m.swimCol, m.swimPos = prev.swimCol, prev.swimPos
	m.millerSel = prev.millerSel
	m.panelOpen = prev.panelOpen
	m.emphasis = nil // detaching drops the decoration layer too
	m.remarks = nil  // …and the share's ephemeral remarks (never persisted to Beads)
	m.attach = attachSlot{}
	m.filterBy = "user"
	m.modeBy = "user"
	m.rebuild()
	if needReload {
		return tea.Batch(m.loadCmd(), m.spin.Tick)
	}
	return nil
}

func describeArrangement(a agentapi.ShowAction) string {
	switch {
	case a.Query != "":
		return fmt.Sprintf("query %q", a.Query)
	case len(a.IDs) > 0:
		return fmt.Sprintf("%d selected beads", len(a.IDs))
	default:
		return "an arrangement"
	}
}

func (m *Model) missingFromBoard(ids []string) []string {
	var missing []string
	for _, id := range ids {
		if _, ok := m.byID[id]; !ok {
			missing = append(missing, id)
		}
	}
	return missing
}

// --- the view serialization ---

// agentView captures the screen as both narration and structured data.
func (m *Model) agentView() agentapi.Response {
	v := m.serializeView()
	return agentapi.Response{Text: xmlView(v), Data: v}
}

func (m *Model) serializeView() *agentapi.View {
	v := &agentapi.View{}
	v.Screen = agentapi.ScreenInfo{Width: m.width, Height: m.height, Focus: m.focusName()}

	modeLabel := modeLabels[m.mode]
	switch m.view {
	case ViewSwim:
		modeLabel = "relationship-board"
	case ViewColumns:
		modeLabel = "columns"
	case ViewTree:
		modeLabel = "tree"
	case ViewKanban:
		modeLabel += " · board"
	}
	v.Mode = agentapi.Provenanced{Value: modeLabel, SetBy: m.provenance(m.modeBy)}
	if m.modeBy == "agent" {
		v.Mode.Title = m.attach.title
	}
	v.Filter = m.filterInfo()
	s := m.activeSort()
	dir := "asc"
	if s.Desc {
		dir = "desc"
	}
	v.Sort = agentapi.SortInfo{Key: string(s.Key), Dir: dir}
	v.Emphasis = m.emphasis
	v.Shares = m.serializeShares()
	v.Footer = m.footerText()

	bodyH := max(1, m.height-2)
	switch m.view {
	case ViewSwim:
		v.Swim = m.serializeSwim()
	case ViewColumns:
		v.Columns = m.serializeColumns(bodyH)
	case ViewTree:
		v.Tree = m.serializeTree(bodyH)
	case ViewKanban:
		v.Board = m.serializeBoard(bodyH)
	default:
		v.Board = m.serializeList(bodyH) // the single-column sectioned list (ViewList)
	}
	v.Panel = m.serializePanel(bodyH)
	return v
}

// serializeList mirrors the full-width sectioned list: the one page-jump
// window spans all sections, so a section can be partly visible. Each section
// with a visible row becomes a <column> carrying only its visible rows plus
// hidden-above/below counts — the same visible-only contract as the board.
func (m *Model) serializeList(bodyH int) *agentapi.BoardInfo {
	b := &agentapi.BoardInfo{TotalColumns: len(m.columns)}
	if len(m.columns) == 0 {
		return b
	}
	win := max(1, bodyH-2)
	start := windowStart(m.winStart, "\x00list", len(m.listRows), win, m.listFocusIndex())
	end := start + win
	if end > len(m.listRows) {
		end = len(m.listRows)
	}
	order := []int{}
	byCol := map[int]*agentapi.ColumnInfo{}
	for i := start; i < end; i++ {
		r := m.listRows[i]
		if r.kind != rowIssue {
			continue
		}
		ci, ok := byCol[r.col]
		if !ok {
			col := m.columns[r.col]
			ci = &agentapi.ColumnInfo{Name: col.Title, Total: len(col.Issues), FirstRow: r.row + 1}
			byCol[r.col] = ci
			order = append(order, r.col)
		}
		is := *r.issue
		ci.Rows = append(ci.Rows, agentapi.RowInfo{
			ID: is.ID, Row: r.row + 1, Title: is.Title,
			Priority: is.Priority, Status: is.Status, Blockers: m.openBlockerCount(is),
			Focused: r.col == m.colIdx && r.row == m.rowIdx,
		})
		ci.LastRow = r.row + 1
	}
	for _, ci := range order {
		c := byCol[ci]
		c.HiddenAbove = c.FirstRow - 1
		c.HiddenBelow = c.Total - c.LastRow
		b.Columns = append(b.Columns, *c)
	}
	if len(order) > 0 {
		b.FirstVisible, b.LastVisible = order[0]+1, order[len(order)-1]+1
	}
	return b
}

func (m *Model) focusName() string {
	switch {
	case m.prompt != promptNone:
		return "PROMPT"
	case m.detail != nil:
		return "DETAIL"
	case m.panelFocus:
		return "PANEL"
	default:
		return "BOARD"
	}
}

func (m *Model) provenance(by string) string {
	if by == "" {
		return "user"
	}
	return by
}

func (m *Model) filterInfo() agentapi.Provenanced {
	var parts []string
	if m.activeQuery != "" {
		parts = append(parts, fmt.Sprintf("query %q", m.activeQuery))
	}
	if len(m.attach.ids) > 0 {
		parts = append(parts, fmt.Sprintf("agent list of %d ids", len(m.attach.ids)))
	}
	if len(m.analystIDs) > 0 {
		parts = append(parts, fmt.Sprintf("analyst matches (%d ids)", len(m.analystIDs)))
	}
	p := agentapi.Provenanced{Value: "none", SetBy: "user"}
	if len(parts) > 0 {
		p.Value = strings.Join(parts, " + ")
		p.SetBy = m.provenance(m.filterBy)
		if p.SetBy == "agent" {
			p.Title = m.attach.title
		}
	}
	return p
}

func (m *Model) footerText() string {
	if m.message != "" {
		return m.message
	}
	return "(keybind hints)"
}

func (m *Model) serializeBoard(bodyH int) *agentapi.BoardInfo {
	b := &agentapi.BoardInfo{TotalColumns: len(m.columns)}
	if len(m.columns) == 0 {
		return b
	}
	colStart, nVis := m.columnWindow()
	cardWindow := max(1, bodyH-3) // borderless: title(1) + reserved overflow lines(2)
	b.FirstVisible, b.LastVisible = colStart+1, colStart+nVis
	for i := colStart; i < colStart+nVis && i < len(m.columns); i++ {
		col := m.columns[i]
		active := i == m.colIdx
		focusRow := -1
		if active {
			focusRow = m.rowIdx
		}
		start := windowStart(m.winStart, col.Key, len(col.Issues), cardWindow, focusRow)
		end := start + cardWindow
		if end > len(col.Issues) {
			end = len(col.Issues)
		}
		ci := agentapi.ColumnInfo{
			Name:        col.Title,
			Total:       len(col.Issues),
			HiddenAbove: start,
			HiddenBelow: len(col.Issues) - end,
		}
		if end > start {
			ci.FirstRow, ci.LastRow = start+1, end
		}
		for j := start; j < end; j++ {
			is := col.Issues[j]
			ci.Rows = append(ci.Rows, agentapi.RowInfo{
				ID: is.ID, Row: j + 1, Title: is.Title,
				Priority: is.Priority, Status: is.Status, Blockers: m.openBlockerCount(is),
				Focused: active && j == m.rowIdx,
			})
		}
		b.Columns = append(b.Columns, ci)
	}
	return b
}

func (m *Model) serializeTree(bodyH int) *agentapi.TreeInfo {
	rel := "hierarchy"
	if m.depsTree {
		rel = "deps"
	}
	t := &agentapi.TreeInfo{Relation: rel, TotalRows: len(m.treeRows)}
	if len(m.treeRows) == 0 {
		return t
	}
	win := max(1, bodyH-2)
	start := windowStart(m.winStart, "\x00tree", len(m.treeRows), win, m.treeIdx)
	end := start + win
	if end > len(m.treeRows) {
		end = len(m.treeRows)
	}
	t.FirstRow, t.LastRow = start+1, end
	t.HiddenAbove, t.HiddenBelow = start, len(m.treeRows)-end
	for i := start; i < end; i++ {
		r := m.treeRows[i]
		t.Rows = append(t.Rows, agentapi.RowInfo{
			ID: r.issue.ID, Row: i + 1, Title: r.issue.Title,
			Priority: r.issue.Priority, Status: r.issue.Status, Depth: r.depth,
			Blockers: m.openBlockerCount(r.issue), Focused: i == m.treeIdx,
		})
	}
	return t
}

// serializeSwim mirrors the relationship swimlane board: the root, the shown
// status columns, and each populated lane's cells (only-populated, like the
// screen). The whole neighborhood is small, so every card is reported.
func (m *Model) serializeSwim() *agentapi.SwimInfo {
	rootID := m.swimRootID()
	sm := m.buildGrid(rootID, m.gridFacet())
	si := &agentapi.SwimInfo{Root: rootID, RootTitle: sm.root.Title}
	if fis := m.swimFocusedIssue(); fis != nil {
		si.Focus = fis.ID
	}
	for _, col := range sm.columns {
		si.Statuses = append(si.Statuses, col.key) // facet keys (canonical status names for the status facet)
	}
	ref, _, hasFocus := m.swimFocus(sm)
	for li, lane := range sm.lanes {
		li2 := agentapi.SwimLaneInfo{Relation: lane.kind.label(), Total: lane.total}
		for colIdx, col := range sm.columns {
			cards := sm.cells[li][colIdx]
			if len(cards) == 0 {
				continue
			}
			cell := agentapi.SwimCellInfo{Status: col.key}
			for ci, is := range cards {
				cell.Cards = append(cell.Cards, agentapi.RowInfo{
					ID: is.ID, Title: is.Title, Priority: is.Priority, Status: is.Status,
					Blockers: m.openBlockerCount(is),
					Focused:  hasFocus && ref.lane == li && ref.is.ID == is.ID && ref.cellIdx == ci,
				})
			}
			li2.Cells = append(li2.Cells, cell)
		}
		si.Lanes = append(si.Lanes, li2)
	}
	return si
}

// serializeColumns mirrors the Miller navigator: the relation, the drill path,
// the selected bead, and the VISIBLE columns (the horizontal window the human
// sees) with their cards — the same visible-only contract as the board.
func (m *Model) serializeColumns(bodyH int) *agentapi.ColumnsInfo {
	lay := m.millerBuild()
	ci := &agentapi.ColumnsInfo{
		Relation:     m.millerRelLabel(),
		Path:         lay.path,
		Focus:        m.millerSelectedID(),
		TotalColumns: len(lay.cols),
	}
	// Match viewColumns' window: show the deepest columns that fit.
	w := m.boardWidth()
	previewW := clamp(w/3, 34, 52)
	if maxPrev := w - millerColW - boardColGap; previewW > maxPrev {
		previewW = maxPrev
	}
	colsW := w
	if previewW >= 20 {
		colsW = w - previewW - boardColGap
	}
	nFit := (colsW + boardColGap) / (millerColW + boardColGap)
	if nFit < 1 {
		nFit = 1
	}
	start := 0
	if len(lay.cols) > nFit {
		start = len(lay.cols) - nFit
	}
	ci.FirstVisible = start + 1
	for c := start; c < len(lay.cols); c++ {
		name := "top-level"
		if c > 0 {
			name = m.millerRelLabel() + " of " + m.shortID(lay.path[c-1])
		}
		col := agentapi.ColumnInfo{Name: name, Total: len(lay.cols[c])}
		sel := -1
		if c == lay.focusCol {
			sel = lay.sel
		} else if c < len(lay.path) {
			for i, is := range lay.cols[c] {
				if is.ID == lay.path[c] {
					sel = i
					break
				}
			}
		}
		for i, is := range lay.cols[c] {
			col.Rows = append(col.Rows, agentapi.RowInfo{
				ID: is.ID, Row: i + 1, Title: is.Title, Priority: is.Priority,
				Status: is.Status, Blockers: m.openBlockerCount(is), Focused: i == sel,
			})
		}
		if len(col.Rows) > 0 {
			col.FirstRow, col.LastRow = 1, len(col.Rows)
		}
		ci.Columns = append(ci.Columns, col)
	}
	return ci
}

// serializeShares reports the agent-shares stream + follow state, with the
// newest few entries — so a connected agent knows what it has published and
// whether the human is following or pulling.
func (m *Model) serializeShares() *agentapi.SharesInfo {
	ns := m.sharesNewestFirst()
	si := &agentapi.SharesInfo{Total: len(ns), Unseen: m.shareNew, Following: m.attach.active}
	const cap = 10
	for i, s := range ns {
		if i >= cap {
			break
		}
		si.Entries = append(si.Entries, agentapi.ShareEntry{
			ID: s.ID, Type: string(s.Kind), Name: s.Name, TS: s.TS.Format("2006-01-02T15:04:05Z07:00"),
		})
	}
	return si
}

func (m *Model) serializePanel(bodyH int) agentapi.PanelInfo {
	p := agentapi.PanelInfo{Open: m.panelOpen, Focused: m.panelFocus}
	if !m.panelOpen {
		return p
	}
	is := m.previewIssue()
	if is == nil {
		return p
	}
	p.IssueID = is.ID
	p.Meta = fmt.Sprintf("%s P%d %s(%s)", is.IssueType, is.Priority, is.Status, statusGlyph(is.Status))
	p.Labels = is.Labels
	if strings.TrimSpace(is.Description) != "" {
		p.Description = "shown, clipped to the panel height — full text via issue(id)"
	} else {
		p.Description = "none"
	}
	if strings.TrimSpace(is.Notes) != "" {
		p.Notes = "present — full text via issue(id)"
	} else {
		p.Notes = "none"
	}
	links := m.relatedLinks(*is)
	for i, l := range links {
		li := m.byID[l.id]
		p.Related = append(p.Related, agentapi.LinkInfo{
			Kind: string(l.kind), ID: l.id, Title: li.Title, Status: li.Status,
			Highlighted: m.panelFocus && i == m.panelLinkIdx,
		})
	}
	return p
}

// xmlView renders the structured view as an XML serialization of the UI
// state: the element hierarchy mirrors the view tree, attributes carry each
// view's meaningful state. Visible-only is a hard rule — hidden issues exist
// only as counts in attributes.
func xmlView(v *agentapi.View) string {
	var b strings.Builder
	fmt.Fprintf(&b, "<screen focus=%q>\n", strings.ToLower(v.Screen.Focus))

	provAttrs := func(prefix string, p agentapi.Provenanced) string {
		if p.Value == "" || p.Value == "none" {
			return ""
		}
		out := fmt.Sprintf(" %s=%q %s-by=%q", prefix, p.Value, prefix, p.SetBy)
		if p.Title != "" {
			out += fmt.Sprintf(" %s-title=%q", prefix, p.Title)
		}
		return out
	}

	if v.Board != nil {
		fmt.Fprintf(&b, "  <board mode=%q mode-by=%q%s>\n",
			strings.ToLower(v.Mode.Value), v.Mode.SetBy, provAttrs("filter", v.Filter))
		var offscreen []string
		for i, c := range v.Board.Columns {
			_ = i
			vis := fmt.Sprintf("%d-%d", c.FirstRow, c.LastRow)
			if c.FirstRow == 1 && c.LastRow == c.Total {
				vis = "all"
			}
			if c.Total == 0 {
				vis = "none"
			}
			fmt.Fprintf(&b, "    <column key=%q count=\"%d\" visible=%q hidden-above=\"%d\" hidden-below=\"%d\">\n",
				c.Name, c.Total, vis, c.HiddenAbove, c.HiddenBelow)
			for _, r := range c.Rows {
				b.WriteString("      " + cardXML(r) + "\n")
			}
			b.WriteString("    </column>\n")
		}
		_ = offscreen
		if hidden := v.Board.TotalColumns - len(v.Board.Columns); hidden > 0 {
			fmt.Fprintf(&b, "    <columns-offscreen count=\"%d\" position=\"showing %d-%d of %d\"/>\n",
				hidden, v.Board.FirstVisible, v.Board.LastVisible, v.Board.TotalColumns)
		}
		b.WriteString("  </board>\n")
	}

	if v.Tree != nil {
		t := v.Tree
		fmt.Fprintf(&b, "  <tree relation=%q mode-by=%q rows=\"%d\" visible=\"%d-%d\" hidden-above=\"%d\" hidden-below=\"%d\"%s>\n",
			t.Relation, v.Mode.SetBy, t.TotalRows, t.FirstRow, t.LastRow, t.HiddenAbove, t.HiddenBelow,
			provAttrs("filter", v.Filter))
		b.WriteString(nestNodes(t.Rows, "    "))
		b.WriteString("  </tree>\n")
	}

	if v.Swim != nil {
		s := v.Swim
		fmt.Fprintf(&b, "  <relationship-board root=%q focus=%q statuses=%q%s>%s\n",
			s.Root, s.Focus, strings.Join(s.Statuses, ","), provAttrs("filter", v.Filter), " <!-- lanes × status matrix rooted at the bead -->")
		for _, lane := range s.Lanes {
			fmt.Fprintf(&b, "    <lane rel=%q count=\"%d\">\n", lane.Relation, lane.Total)
			for _, cell := range lane.Cells {
				fmt.Fprintf(&b, "      <cell status=%q>\n", cell.Status)
				for _, r := range cell.Cards {
					b.WriteString("        " + cardXML(r) + "\n")
				}
				b.WriteString("      </cell>\n")
			}
			b.WriteString("    </lane>\n")
		}
		b.WriteString("  </relationship-board>\n")
	}

	if v.Columns != nil {
		c := v.Columns
		fmt.Fprintf(&b, "  <columns relation=%q path=%q focus=%q total=\"%d\" first-visible=\"%d\"%s>\n",
			c.Relation, strings.Join(c.Path, " › "), c.Focus, c.TotalColumns, c.FirstVisible,
			provAttrs("filter", v.Filter))
		if hidden := c.FirstVisible - 1; hidden > 0 {
			fmt.Fprintf(&b, "    <columns-offscreen-left count=\"%d\"/>\n", hidden)
		}
		for i, col := range c.Columns {
			fmt.Fprintf(&b, "    <column depth=\"%d\" name=%q count=\"%d\">\n", c.FirstVisible-1+i, col.Name, col.Total)
			for _, r := range col.Rows {
				b.WriteString("      " + cardXML(r) + "\n")
			}
			b.WriteString("    </column>\n")
		}
		b.WriteString("  </columns>\n")
	}

	if !v.Panel.Open {
		b.WriteString("  <panel open=\"false\"/>\n")
	} else {
		scroll := "clipped-to-fit"
		fmt.Fprintf(&b, "  <panel open=\"true\" issue=%q focused=\"%v\" scroll=%q>\n",
			v.Panel.IssueID, v.Panel.Focused, scroll)
		meta := v.Panel.Meta
		fmt.Fprintf(&b, "    <meta %s labels=%q/>\n", metaAttrs(meta), strings.Join(v.Panel.Labels, ","))
		fmt.Fprintf(&b, "    <description present=\"%v\"/><notes present=\"%v\"/>\n",
			v.Panel.Description != "none", v.Panel.Notes != "none")
		if len(v.Panel.Related) > 0 {
			b.WriteString("    <related>\n")
			for _, l := range v.Panel.Related {
				attrs := fmt.Sprintf("rel=%q id=%q status=%q", relName(l.Kind), l.ID, l.Status)
				if l.Highlighted {
					attrs += " highlighted=\"true\""
				}
				fmt.Fprintf(&b, "      <link %s>%s</link>\n", attrs, xmlEscape(truncTitle(l.Title)))
			}
			b.WriteString("    </related>\n")
		}
		b.WriteString("  </panel>\n")
	}

	fmt.Fprintf(&b, "  <sort key=%q dir=%q/>\n", v.Sort.Key, v.Sort.Dir)
	if len(v.Emphasis) > 0 {
		b.WriteString("  <emphasis>\n")
		for _, e := range v.Emphasis {
			attrs := fmt.Sprintf("kind=%q ref=%q style=%q", e.Kind, e.Ref, e.Style)
			if e.Label != "" {
				attrs += fmt.Sprintf(" label=%q", xmlEscape(e.Label))
			}
			fmt.Fprintf(&b, "    <mark %s/>\n", attrs)
		}
		b.WriteString("  </emphasis>\n")
	}
	if v.Shares != nil {
		s := v.Shares
		fmt.Fprintf(&b, "  <agent-shares total=\"%d\" unseen=\"%d\" following=\"%v\"> <!-- you PUBLISH here; the human browses these views (and each view's beads) with @ -->\n",
			s.Total, s.Unseen, s.Following)
		for _, e := range s.Entries {
			fmt.Fprintf(&b, "    <share id=%q kind=%q name=%q ts=%q/>\n", e.ID, e.Type, xmlEscape(e.Name), e.TS)
		}
		b.WriteString("  </agent-shares>\n")
	}
	fmt.Fprintf(&b, "  <footer>%s</footer>\n", xmlEscape(v.Footer))
	b.WriteString("</screen>")
	return b.String()
}

// openBlockerCount is the decision signal: how many OPEN issues block this one
// (0 = ready to work). Cheap from the in-memory graph.
func (m Model) openBlockerCount(is bd.Issue) int {
	n := 0
	for _, id := range bd.BlockerIDs(is) {
		if b, ok := m.byID[id]; ok && b.Status != bd.StatusClosed {
			n++
		}
	}
	return n
}

func cardAttrs(r agentapi.RowInfo) string {
	attrs := fmt.Sprintf("id=%q row=\"%d\"", r.ID, r.Row)
	if r.Focused {
		attrs += " focused=\"true\""
	}
	attrs += fmt.Sprintf(" p=\"%d\" status=%q", r.Priority, r.Status)
	if r.Blockers > 0 {
		attrs += fmt.Sprintf(" blockers=\"%d\"", r.Blockers) // >0 ⇒ blocked; absent ⇒ ready
	}
	return attrs
}

func cardXML(r agentapi.RowInfo) string {
	return fmt.Sprintf("<card %s>%s</card>", cardAttrs(r), xmlEscape(truncTitle(r.Title)))
}

// nestNodes emits visible tree rows as nested <node> elements mirroring the
// tree's indentation. A visible child whose parent is scrolled off the top
// nests relative to what IS visible.
func nestNodes(rows []agentapi.RowInfo, indent string) string {
	var b strings.Builder
	type open struct{ depth int }
	var stack []open
	closeTo := func(depth int) {
		for len(stack) > 0 && stack[len(stack)-1].depth >= depth {
			stack = stack[:len(stack)-1]
			b.WriteString(indent + strings.Repeat("  ", len(stack)) + "</node>\n")
		}
	}
	base := 0
	if len(rows) > 0 {
		base = rows[0].Depth
	}
	for _, r := range rows {
		d := r.Depth - base
		if d < 0 {
			d = 0
		}
		closeTo(d)
		fmt.Fprintf(&b, "%s%s<node %s>%s\n", indent, strings.Repeat("  ", len(stack)), cardAttrs(r), xmlEscape(truncTitle(r.Title)))
		stack = append(stack, open{depth: d})
	}
	closeTo(0)
	return b.String()
}

func metaAttrs(meta string) string {
	// Meta is "type P# status(glyph)"; re-derive terse attributes.
	fields := strings.Fields(meta)
	typ, p, status := "", "", ""
	if len(fields) >= 3 {
		typ = fields[0]
		p = strings.TrimPrefix(fields[1], "P")
		status = fields[2]
		if i := strings.Index(status, "("); i >= 0 {
			status = status[:i]
		}
	}
	return fmt.Sprintf("type=%q p=%q status=%q", typ, p, status)
}

func relName(kind string) string {
	switch kind {
	case "needs":
		return "depends-on"
	case "blocks":
		return "blocked-by"
	default:
		return kind
	}
}

var xmlEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")

func xmlEscape(s string) string { return xmlEscaper.Replace(s) }

func truncTitle(s string) string {
	return ansi.Truncate(s, 60, "…")
}
