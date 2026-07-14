package ui

import (
	"slices"

	tea "github.com/charmbracelet/bubbletea"
)

// Navigation history: `[` and `]` step back/forward through the user's own
// positions (browser-style) — "jump back to where I was looking". This is
// distinct from board time-travel (T), which scrubs the DATA.
//
// The timeline is COARSE — a page/detail level, not a cursor level. A checkpoint
// is pushed ONLY on a real transition: a view/mode/layout switch, a filter
// applied or cleared, a relationship/columns drill or re-root, or opening a
// bead's detail. Cursor scanning within a page (j/k/h/l/g/G) records NOTHING —
// it just updates the live position that the NEXT checkpoint will capture. So a
// checkpoint remembers the bead you were on WHEN YOU LEFT the page, and `[`
// restores that page with that bead re-selected, without ever stepping through
// every bead you scanned past.

// navPos is a nav-history checkpoint: a full viewState snapshot plus the focused
// bead's id. The id (not the raw cursor) is what restoreNav re-selects, because
// a filter reload reorders rows and a stored index would drift.
type navPos struct {
	vs   viewState
	bead string
}

// snapshotNav captures the current position for the nav timeline: the whole
// viewState (cloned, so a later in-place edit can't corrupt the checkpoint) plus
// the focused bead's id.
func (m Model) snapshotNav() navPos {
	p := navPos{vs: m.viewState.clone()}
	if is := m.focusedIssue(); is != nil {
		p.bead = is.ID
	}
	return p
}

// sameContext is everything that defines a position EXCEPT which bead is
// selected — the view, filter, and open detail. Two positions with the same
// context differ only by a cursor move. It reads only the location-defining
// subset of the snapshot's viewState (not the sort/collapse knobs).
func (a navPos) sameContext(b navPos) bool {
	return a.vs.mode == b.vs.mode && a.vs.view == b.vs.view &&
		a.vs.swimRoot == b.vs.swimRoot && a.vs.scopeRoot == b.vs.scopeRoot &&
		slices.Equal(a.vs.millerPath, b.vs.millerPath) && a.vs.millerDeps == b.vs.millerDeps &&
		a.vs.activeQuery == b.vs.activeQuery && a.vs.detailID() == b.vs.detailID()
}

// sameLocation ignores panel state (not location-defining on its own).
func (a navPos) sameLocation(b navPos) bool {
	return a.bead == b.bead && a.sameContext(b)
}

// recordNav checkpoints `before` when the keypress caused a real page/view/
// filter/drill transition — NOT a cursor move. Skipped while restoring history
// (that flag is consumed here). A pure cursor move (same context, only the bead
// changed) records nothing: it just leaves the live position for the next
// checkpoint to capture. (Opening a detail is async, so it checkpoints from the
// detailMsg handler, not here — see pushNav.)
func (m *Model) recordNav(before navPos) {
	if m.navigatingHistory {
		m.navigatingHistory = false
		return
	}
	after := m.snapshotNav()
	if before.sameContext(after) {
		return // scanning within a page — no checkpoint
	}
	m.pushNav(before)
}

// pushNav appends a checkpoint to the back-stack (deduped against the top) and
// clears the forward stack — standard browser semantics. Used by recordNav for
// synchronous transitions and by the detailMsg handler for the async detail
// open.
func (m *Model) pushNav(pos navPos) {
	if n := len(m.navBack); n == 0 || !m.navBack[n-1].sameLocation(pos) {
		m.navBack = append(m.navBack, pos)
		if len(m.navBack) > 200 {
			m.navBack = m.navBack[len(m.navBack)-200:]
		}
	}
	m.navFwd = nil
}

func (m *Model) navBackCmd() tea.Cmd {
	if len(m.navBack) == 0 {
		m.setMessage("no earlier position", false)
		return nil
	}
	m.navFwd = append(m.navFwd, m.snapshotNav())
	pos := m.navBack[len(m.navBack)-1]
	m.navBack = m.navBack[:len(m.navBack)-1]
	return m.restoreNav(pos, "←")
}

func (m *Model) navFwdCmd() tea.Cmd {
	if len(m.navFwd) == 0 {
		m.setMessage("no later position", false)
		return nil
	}
	m.navBack = append(m.navBack, m.snapshotNav())
	pos := m.navFwd[len(m.navFwd)-1]
	m.navFwd = m.navFwd[:len(m.navFwd)-1]
	return m.restoreNav(pos, "→")
}

// restoreNav re-selects a remembered position: its view, filter, bead, and
// detail. It writes back ONLY nav.go's scope (view/mode, the relationship roots
// and drill, the filter, the panel, the open detail, and the focused bead) —
// deliberately NOT the sort/collapse/min-subtree knobs the saved viewState also
// carries, so a browser-back never reverts a sort or fold the user changed since
// the checkpoint. A filter change reloads (the bead is re-selected by id
// afterward); a detail change opens/closes it.
func (m *Model) restoreNav(pos navPos, arrow string) tea.Cmd {
	m.navigatingHistory = true // suppress the recordNav this move triggers
	m.mode = pos.vs.mode
	m.setView(pos.vs.view)
	m.swimRoot = pos.vs.swimRoot
	m.scopeRoot = pos.vs.scopeRoot // a checkpoint remembers whether it was scoped
	m.swimCol, m.swimPos = 0, 0
	m.millerDeps = pos.vs.millerDeps
	m.millerSel = pos.vs.millerSel
	m.millerPath = append([]string(nil), pos.vs.millerPath...)
	m.panelOpen = pos.vs.panelOpen

	var cmds []tea.Cmd
	if pos.vs.activeQuery != m.activeQuery {
		m.activeQuery = pos.vs.activeQuery
		m.focusIDBefore = pos.bead // the reload re-selects it by id
		cmds = append(cmds, m.loadCmd())
	} else {
		m.rebuild()
		if pos.bead != "" && !m.jumpTo(pos.bead) {
			m.setMessage(arrow+" "+m.shortID(pos.bead)+" — no longer in this view", false)
		}
	}
	did := pos.vs.detailID()
	switch {
	case did != "" && (m.detail == nil || m.detail.ID != did):
		m.detailTab = pos.vs.detailTab
		m.loading = true
		m.suppressDetailCheckpoint = true // a restored detail must not re-checkpoint
		cmds = append(cmds, m.showCmd(did))
	case did == "" && m.detail != nil:
		m.detail = nil
	}
	if m.message == "" {
		m.setMessage(arrow+" "+m.shortID(pos.bead), false)
	}
	if len(cmds) > 0 {
		return tea.Batch(append(cmds, m.spin.Tick)...)
	}
	return nil
}

// exitScope leaves the relationship-focus scope, restoring the EXACT pre-scope
// arrangement captured at entry (view, mode, every facet/sort/collapse knob, and
// the cursor by id) — a full-viewState write-back like cancelControl, not the nav
// timeline's deliberately partial one, so a grouper/sort tuned inside the scope
// never leaks out. A pre-scope open detail is reopened (its viewport is Model-
// level, so re-fetched); otherwise the remembered bead is re-selected on the board.
//
// It stays symmetric with the [ / ] timeline: the scoped position is pushed onto
// the forward stack, so ] redoes back into the scope exactly as it would redo any
// esc-back (restoreNav is scope-aware).
func (m *Model) exitScope() tea.Cmd {
	m.navFwd = append(m.navFwd, m.snapshotNav()) // ] redoes back into the scope
	ret := m.scopeReturn
	did := ret.vs.detailID()
	m.viewState = ret.vs.clone() // full restore, incl. scopeRoot="" — drops the scope
	m.scopeReturn = navPos{}
	m.navigatingHistory = true // this esc is the scope exit, not a new checkpoint
	if did != "" {
		// The restore set m.detail to the snapshot's (stale) pointer; reopen cleanly.
		m.detail = nil
		m.loading = true
		m.suppressDetailCheckpoint = true
		m.setMessage("esc "+m.shortID(did), false)
		return tea.Batch(m.showCmd(did), m.spin.Tick)
	}
	m.detail = nil
	m.rebuild()
	if ret.bead != "" {
		m.jumpTo(ret.bead)
	}
	m.setMessage("esc "+m.shortID(ret.bead), false)
	return nil
}
