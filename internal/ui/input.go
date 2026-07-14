package ui

// input.go is the key-handling layer: handleKey routes every keypress by the
// current focus (prompt / panel / detail / board) and layout.

import (
	"fmt"
	"strings"

	keybind "github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/awhitty/bb/internal/nlq"
	"github.com/awhitty/bb/internal/rollup"
)

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	if key == "ctrl+c" {
		return m, tea.Quit
	}

	// ---- facet control swallows input ----
	// A small anchored dropdown (S/M/L/d and the per-facet controls): esc reverts
	// to the pre-open state, enter commits the previewed change, j/k move + re-
	// preview. Everything else is swallowed so the board never sees it while the
	// menu is open.
	if m.control.open {
		return m.handleControlKey(msg)
	}

	// ---- full help overlay swallows input ----
	if m.showHelp {
		switch key {
		case "?", "esc", "q":
			m.showHelp = false
			return m, nil
		case "g":
			m.helpVP.GotoTop()
			return m, nil
		case "G":
			m.helpVP.GotoBottom()
			return m, nil
		}
		var cmd tea.Cmd
		m.helpVP, cmd = m.helpVP.Update(msg) // j/k/arrows/pgup/pgdn scroll
		return m, cmd
	}

	// ---- text prompts swallow input ----
	switch m.prompt {
	case promptQuery, promptNLEdit, promptAnalyst, promptJump, promptComment:
		switch key {
		case "esc":
			if m.compiling {
				m.compiling = false
				m.compileSeq++ // drop the in-flight result
				m.setMessage("compile cancelled", false)
				return m, nil
			}
			// Editing during an NL preview: esc returns to the review of the
			// pre-edit query (the preview stays live), not a full cancel.
			if m.prompt == promptNLEdit && m.previewing {
				m.prompt = promptNLReview
				m.input.Blur()
				return m, nil
			}
			if m.prompt == promptNLEdit {
				m.recordFeedback(nlq.Rejected, "")
			}
			m.closePrompt()
			return m, nil
		case "enter":
			text := strings.TrimSpace(m.input.Value())
			switch m.prompt {
			case promptNLEdit:
				if text == "" {
					m.prompt = promptNLReview
					m.input.Blur()
					return m, nil
				}
				// During a preview, editing RE-PREVIEWS and returns to the review
				// (accept/cancel still to come); otherwise it commits as before.
				if m.previewing {
					m.review.Query = text
					m.previewEdited = true
					m.prompt = promptNLReview
					m.input.Blur()
					return m, tea.Batch(m.previewCmd(text), m.spin.Tick)
				}
				m.recordFeedback(nlq.Edited, text)
				m.activeQuery = text
				m.closePrompt()
				return m, tea.Batch(m.loadCmd(), m.spin.Tick)
			case promptAnalyst:
				m.closePrompt()
				if text == "" {
					return m, nil
				}
				return m, m.startAnalyst(text)
			case promptJump:
				m.closePrompt()
				if text == "" {
					return m, nil
				}
				m.jumpTo(text) // move selection if it's on the board
				m.loading = true
				return m, tea.Batch(m.showCmd(text), m.spin.Tick) // open its detail
			case promptComment:
				m.closePrompt()
				if text == "" || m.detail == nil {
					return m, nil
				}
				m.setMessage("adding comment…", false)
				return m, tea.Batch(m.addCommentCmd(m.detail.ID, text), m.spin.Tick)
			default: // promptQuery — the smart `/` prompt: filter or ask
				// Empty clears the filter; a query-shaped string runs as a raw
				// bd filter; anything else is natural language → the compile flow
				// (live-preview + re-roll + edit + feedback logging, all preserved).
				if text == "" || nlq.IsQueryShaped(text) {
					m.activeQuery = text
					m.filterBy = "user"
					m.closePrompt()
					return m, tea.Batch(m.loadCmd(), m.spin.Tick)
				}
				if cmd, ok := m.requireModels(); !ok {
					m.closePrompt()
					return m, cmd
				}
				m.rolls, m.priorRolls = 0, nil // fresh ask, fresh re-roll budget
				m.compiling = true
				m.setMessage(fmt.Sprintf("compiling via %s…", m.provider.Label), false)
				return m, tea.Batch(m.compileCmd(text), m.spin.Tick)
			}
		}
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd

	case promptNLReview:
		if m.compiling {
			if key == "esc" {
				m.compiling = false
				m.compileSeq++ // drop the in-flight re-roll; keep the current query
				m.setMessage("", false)
			}
			return m, nil // a re-roll is in flight; other verdicts wait for it
		}
		switch key {
		case "enter", "y":
			// The previewed query is already the live filter — commit it in place.
			verdict := nlq.Accepted
			if m.previewEdited {
				verdict = nlq.Edited
			}
			m.recordFeedback(verdict, m.review.Query)
			m.activeQuery = m.review.Query
			m.filterBy = "user"
			m.endPreview(false) // keep the previewed filter
			m.closePrompt()
			return m, nil
		case "e":
			return m, m.openPrompt(promptNLEdit, m.review.Query, "edit> ")
		case "r":
			// Re-roll: same NL, different interpretation. Temperature stays 0;
			// the prompt carries the rolled-away attempts so decoding differs.
			if m.rolls >= maxRerolls {
				m.setMessage(fmt.Sprintf("%d re-rolls — press e to edit it instead", maxRerolls), false)
				return m, nil
			}
			m.recordFeedback(nlq.Rerolled, "")
			m.priorRolls = append(m.priorRolls, m.review.Query)
			m.rolls++
			m.compiling = true
			m.setMessage(fmt.Sprintf("re-rolling via %s…", m.provider.Label), false)
			return m, tea.Batch(m.compileCmd(m.review.NL), m.spin.Tick)
		case "esc", "n":
			m.recordFeedback(nlq.Rejected, "")
			revert := m.endPreview(true) // restore the exact pre-ask view
			m.closePrompt()
			msg := "rejected — logged for training"
			if m.compileElapsed > slowCompile {
				msg += " · slow model for compiles — consider a smaller BB_NLQ_MODEL"
			}
			m.setMessage(msg, false)
			if revert != nil {
				return m, tea.Batch(revert, m.spin.Tick)
			}
			return m, nil
		}
		return m, nil
	}

	// ---- preview panel focus ----
	if m.panelFocus && m.detail == nil {
		is := m.previewIssue()
		var flat []string
		if is != nil {
			_, flat = m.octopusBody(is.ID, max(8, m.panelWidth()-4), -1)
		}
		switch key {
		case "esc", "tab":
			m.panelFocus = false
			return m, nil
		case " ":
			m.panelOpen, m.panelFocus = false, false
			return m, nil
		case "j", "down":
			m.panelLinkIdx = clamp(m.panelLinkIdx+1, 0, max(0, len(flat)-1))
			return m, nil
		case "k", "up":
			m.panelLinkIdx = clamp(m.panelLinkIdx-1, 0, max(0, len(flat)-1))
			return m, nil
		case "g":
			m.panelLinkIdx = 0
			return m, nil
		case "G":
			m.panelLinkIdx = max(0, len(flat)-1)
			return m, nil
		case "enter":
			if len(flat) == 0 {
				return m, nil
			}
			target := flat[clamp(m.panelLinkIdx, 0, len(flat)-1)]
			if m.jumpTo(target) {
				m.panelLinkIdx = 0 // panel follows; travel on from the new card
			} else {
				m.setMessage(target+" is not on the board (filtered or closed?)", false)
			}
			return m, nil
		}
		// Everything else (s, r, c, /, ?, m, 1-4, q) still drives the board.
	}

	// ---- detail view (tabbed) ----
	if m.detail != nil {
		switch key {
		case "esc", "q":
			// Closing the detail steps BACK on the coarse timeline: opening it
			// pushed the pre-detail position (the board + the bead you opened
			// from), so back restores it and ] reopens. Fall back to a modal close
			// only if there's no checkpoint to return to (defensive).
			if len(m.navBack) > 0 {
				return m, m.navBackCmd()
			}
			m.detail = nil
			m.navigatingHistory = true
			return m, nil
		case "tab":
			m.detailTab = m.detailTab.next()
			m.setDetailContent()
			m.detailVP.GotoTop()
			return m, nil
		case "shift+tab":
			m.detailTab = m.detailTab.prev()
			m.setDetailContent()
			m.detailVP.GotoTop()
			return m, nil
		case "+", "=":
			return m, tea.Batch(m.changePriority(-1), m.spin.Tick)
		case "-", "_":
			return m, tea.Batch(m.changePriority(+1), m.spin.Tick)
		case "c":
			// Add a comment — the human↔agent annotation channel.
			return m, m.openPrompt(promptComment, "", "comment> ")
		case "R":
			// Enter this bead's relationship focus (the swimlane presentation of the
			// scoped root). enterSwimRooted captures the pre-scope return WITH this
			// detail open — so esc reopens it — then drops the detail for the board.
			m.modeBy = "user"
			m.enterSwimRooted(m.detail.ID)
			return m, nil
		case "T":
			// Theme toggle also here: the glamour body is most visible in the
			// detail view, so recovery must work without backing out first.
			// toggleTheme rebuilds the renderer and re-renders the open detail.
			m.toggleTheme()
			m.setMessage("theme: "+themeName()+" (T to switch)", false)
			return m, nil
		}
		switch key {
		case "g":
			m.detailVP.GotoTop()
			return m, nil
		case "G":
			m.detailVP.GotoBottom()
			return m, nil
		}
		var cmd tea.Cmd
		m.detailVP, cmd = m.detailVP.Update(msg)
		return m, cmd
	}

	// ---- shares browser (master-detail) swallows input ----
	// A self-contained mode: it owns j/k across sections, A/enter (attach to the
	// channel), and esc/[ (back out), so board keys never leak through underneath it.
	if m.sharesBrowse {
		return m.handleSharesBrowseKey(msg)
	}

	// ---- attached: STICKY (only esc detaches) ----
	// While an attach layer is live the agent drives the board, but the attach is
	// STICKY: the human may move the cursor, page, open the panel, and change the
	// view locally (mode/grouper/…) and STAY attached — the agent's next push simply
	// re-asserts the channel's face over that fiddling (latest push wins). ONLY esc
	// detaches, through the layer stack below (popLayer). Every local change while
	// attached is EPHEMERAL within the layer: history is suppressed (Update's
	// recordNav gate) and detach restores the exact pre-attach view (detach()), so no
	// fiddling leaks past the detach or onto the [ / ] timeline. Nav keys therefore
	// fall through to the board handling below unchanged.

	// ---- board ----
	// Discrete actions dispatch through the keybinding registry (keys.go), so
	// the keys can never drift from the footer/overlay. Contextual movement
	// (h/l/j/k, g/G) and directional pairs ([ ], < >, +/-) stay on raw keys
	// below because the same binding means different things per view/direction.
	k := m.keys
	switch {
	case keybind.Matches(msg, k.Quit):
		return m, tea.Quit
	case keybind.Matches(msg, k.Refresh):
		return m, tea.Batch(m.loadCmd(), m.spin.Tick)
	case keybind.Matches(msg, k.Help):
		m.openHelp()
		return m, nil
	case keybind.Matches(msg, k.Theme):
		// Recover from a wrong auto-detect or a mid-session terminal theme change:
		// flip the palette + rebuild the glamour body from the new explicit theme,
		// no termenv probe (no deadlock).
		m.toggleTheme()
		m.setMessage("theme: "+themeName()+" (T to switch)", false)
		return m, nil
	case keybind.Matches(msg, k.Jump):
		return m, m.openPrompt(promptJump, "", "id> ")
	case keybind.Matches(msg, k.Query):
		// The smart `/` prompt: a raw bd filter or a natural-language ask.
		return m, m.openPrompt(promptQuery, m.activeQuery, "filter or ask> ")
	case keybind.Matches(msg, k.Analyst):
		if cmd, ok := m.requireModels(); !ok {
			return m, cmd
		}
		return m, m.openPrompt(promptAnalyst, "", "analyst> ")
	case keybind.Matches(msg, k.Closed):
		m.showClosed = !m.showClosed
		return m, tea.Batch(m.loadCmd(), m.spin.Tick)
	case keybind.Matches(msg, k.Age):
		// Cycle the age column between last-activity (updated_at) and
		// time-in-status; the latter ramps amber→red as open work sits cold.
		m.toggleAgeMode()
		return m, nil
	case keybind.Matches(msg, k.Swimlane):
		// Relationship swimlane board rooted at the focused bead: a 2D overview
		// of its sub-issues / blockers / dependents / siblings by status.
		if is := m.focusedIssue(); is != nil {
			m.modeBy = "user"
			m.enterSwim()
		} else {
			m.setMessage("focus a bead first, then R for its relationship board", false)
		}
		return m, nil
	case keybind.Matches(msg, k.Board):
		// Toggle between the multi-column board (the default layout) and the
		// full-width single-column sectioned list. No effect in the tree,
		// swimlane, or columns navigator.
		switch m.view {
		case ViewList:
			m.setView(ViewKanban)
		case ViewKanban:
			m.setView(ViewList)
		}
		return m, nil
	case keybind.Matches(msg, k.Columns):
		// Miller-columns relationship navigator, rooted at the top-level beads.
		m.modeBy = "user"
		m.enterColumns("", m.millerDeps) // keep the current drill relation
		return m, nil
	case keybind.Matches(msg, k.View):
		// Direct view cycle: list → board → tree, no menu.
		m.cycleView()
		return m, nil
	case keybind.Matches(msg, k.Sort):
		m.openSortControl() // anchored dropdown over the current view's sort keys
		return m, nil
	case keybind.Matches(msg, k.Group):
		// Grouper dropdown over the active view's facet slot (board/tree/swim);
		// the columns navigator and the overlays have no grouping axis.
		switch m.view {
		case ViewList, ViewKanban, ViewTree, ViewSwim:
			m.openGroupControl()
		default:
			m.setMessage("grouping applies to the board, tree, and relationship board", false)
		}
		return m, nil
	case keybind.Matches(msg, k.Lanes):
		// Break-out lane control: adds a chosen facet as a second (horizontal) axis
		// on the kanban, splitting each column into lane bands. Kanban-only — the
		// 2D cross-tab is a board rendering.
		if m.view == ViewKanban {
			m.openLaneControl()
		} else {
			m.setMessage("break-out lanes apply to the kanban board (v to reach it)", false)
		}
		return m, nil
	case keybind.Matches(msg, k.AgentShare):
		// @ opens the master-detail shares browser (sectioned: one view per
		// section, its beads inline, the preview panel following focus). The
		// browser swallows @ itself, so this only fires from the board.
		m.openSharesBrowser()
		return m, nil
	case keybind.Matches(msg, k.Activity):
		// The activity feed is an OVERLAY: it composes over the current nav view
		// without clearing it, so esc drops back to what was beneath (tree/swim/…).
		m.modeBy = "user"
		return m, tea.Batch(m.enterActivity(), m.spin.Tick)
	case keybind.Matches(msg, k.EdgeNest):
		// Traverse control: one anchored menu over BOTH tree-traverse axes — the
		// relation (hierarchy ↔ dependencies) and the direction (forward ↔ invert).
		// In the columns navigator only the relation applies, so the menu offers the
		// two relations alone (invert is a tree concept).
		if m.view == ViewTree || m.view == ViewColumns {
			m.openTraverseControl()
		}
		return m, nil
	case keybind.Matches(msg, k.Fold):
		// Fold/unfold the focused node's subtree (tree).
		if m.view == ViewTree {
			m.toggleCollapse()
		}
		return m, nil
	case keybind.Matches(msg, k.FoldAll):
		// Roll every subtree up to the top-level roots (or expand all).
		if m.view == ViewTree {
			m.toggleCollapseAll()
		}
		return m, nil
	case keybind.Matches(msg, k.SubtreeFilter):
		// Cycle the min-subtree-size filter (tree only).
		if m.view == ViewTree {
			m.cycleSubtreeFilter()
		}
		return m, nil
	}

	// ---- board: contextual movement + directional keys (raw) ----
	switch key {
	case "h", "left":
		switch m.view {
		case ViewColumns:
			m.millerBack() // pop a level: focus returns to the left column
		case ViewSwim:
			m.swimNav(-1, 0)
		case ViewTree:
			m.treeCollapseFocused() // no column axis in the tree — left folds / walks up
		case ViewKanban:
			m.nav(-1, 0)
		default: // ViewList
			m.listSection(-1) // jump to the previous section
		}
	case "l", "right":
		switch m.view {
		case ViewColumns:
			m.millerDrill() // drill into the selection: APPENDS a column
		case ViewSwim:
			m.swimNav(1, 0)
		case ViewTree:
			m.treeExpandFocused() // right unfolds / walks into the first child
		case ViewKanban:
			m.nav(1, 0)
		default: // ViewList
			m.listSection(1) // jump to the next section
		}
	case "j", "down":
		switch {
		case m.activityView:
			m.activityIdx = clamp(m.activityIdx+1, 0, max(0, len(m.activityEvents)-1))
		default:
			switch m.view {
			case ViewColumns:
				m.millerMove(1)
			case ViewSwim:
				m.swimNav(0, 1)
			case ViewTree:
				m.treeIdx = clamp(m.treeIdx+1, 0, max(0, len(m.treeRows)-1))
			case ViewKanban:
				m.nav(0, 1)
			default: // ViewList
				m.listDown()
			}
		}
	case "k", "up":
		switch {
		case m.activityView:
			m.activityIdx = clamp(m.activityIdx-1, 0, max(0, len(m.activityEvents)-1))
		default:
			switch m.view {
			case ViewColumns:
				m.millerMove(-1)
			case ViewSwim:
				m.swimNav(0, -1)
			case ViewTree:
				m.treeIdx = clamp(m.treeIdx-1, 0, max(0, len(m.treeRows)-1))
			case ViewKanban:
				m.nav(0, -1)
			default: // ViewList
				m.listUp()
			}
		}
	case "g":
		switch {
		case m.activityView:
			m.activityIdx = 0
		default:
			switch m.view {
			case ViewColumns:
				m.millerTop()
			case ViewSwim:
				m.swimTop()
			case ViewTree:
				m.treeIdx = 0
			case ViewKanban:
				m.rowIdx = 0
			default: // ViewList
				m.listTop()
			}
		}
	case "G":
		switch {
		case m.activityView:
			m.activityIdx = max(0, len(m.activityEvents)-1)
		default:
			switch m.view {
			case ViewColumns:
				m.millerBottom()
			case ViewSwim:
				m.swimBottom()
			case ViewTree:
				m.treeIdx = max(0, len(m.treeRows)-1)
			case ViewKanban:
				if m.colIdx < len(m.columns) {
					m.rowIdx = max(0, len(m.columns[m.colIdx].Issues)-1)
				}
			default: // ViewList
				m.listBottom()
			}
		}
	case " ":
		m.panelOpen = !m.panelOpen
		if !m.panelOpen {
			m.panelFocus = false
		}
	case "tab":
		// tab's old job (mode cycling) moved to m and 1-4; tab now moves
		// focus into the panel when it is open.
		if m.panelOpen {
			m.panelFocus = true
			m.panelLinkIdx = 0
		}
	case "m":
		m.modeBy = "user"
		// Cycle: status → type → root → blockers → tree → activity → status.
		// (The octopus relationship view is retired from the rotation — the
		// Miller-columns navigator (C) replaces its drill, the swimlane its
		// snapshot; see octopus.go.)
		switch {
		case m.activityView:
			m.activityView = false
			// The feed may overlay any nav view now; the cycle lands on a board
			// grouping, so drop back to the board family if we were elsewhere.
			if m.view != ViewList && m.view != ViewKanban {
				m.setView(ViewKanban)
			}
			m.setMode(rollup.ModeStatus)
		case m.view == ViewTree:
			// Leaving the tree stop for the activity stop: the feed overlays the
			// board underneath, so drop to a board-family view first.
			m.setView(ViewKanban)
			return m, tea.Batch(m.enterActivity(), m.spin.Tick)
		case m.mode == rollup.ModeBlockers:
			m.setView(ViewTree)
		default:
			// From the swimlane/columns the cycle steps back onto the board family.
			if m.view != ViewList && m.view != ViewKanban {
				m.setView(ViewKanban)
			}
			for i, mode := range rollup.Modes {
				if mode == m.mode {
					m.setMode(rollup.Modes[(i+1)%len(rollup.Modes)])
					break
				}
			}
		}
	case "1", "2", "3", "4":
		// A mode key stays in the current board family (list vs kanban) and only
		// drops back from a non-board view (tree/swim/columns). The activity
		// overlay is dismissed.
		m.activityView = false
		if m.view != ViewList && m.view != ViewKanban {
			m.setView(ViewKanban)
		}
		m.modeBy = "user"
		m.setMode(rollup.Modes[int(key[0]-'1')])
	case "5":
		m.activityView = false
		m.setView(ViewTree)
		m.modeBy = "user"
	case "+", "=":
		// Raise priority (toward P0). "=" is the unshifted "+" key.
		return m, tea.Batch(m.changePriority(-1), m.spin.Tick)
	case "-", "_":
		// Lower priority (toward P4).
		return m, tea.Batch(m.changePriority(+1), m.spin.Tick)
	case "[":
		return m, m.navBackCmd() // navigation history: cursor back
	case "]":
		return m, m.navFwdCmd() // navigation history: cursor forward
	case "esc":
		// esc walks the LAYER stack (layers.go): the things composed OVER the
		// active view — an emphasis decoration, an agent arrangement, an analyst
		// filter, the activity feed, a relationship scope, the facet control. It
		// peels ONE, most-recently-raised first (agent-imposed layers pop like any
		// other — the human outranks the agent); each dismiss sets navigatingHistory
		// so the esc is not itself recorded as a return point.
		//
		// (The shares BROWSER handles its own esc in handleSharesBrowseKey, which
		// intercepts before this point — so esc here is never seen while browsing.)
		if cmd, ok := m.popLayer(); ok {
			return m, cmd
		}
		// No layer left: esc defers to the HISTORY timeline (navBack), stepping
		// back exactly like `[` (and `]` redoes) — so escaping a filter or a view
		// switch returns to the position `[` would resurrect. At true base (no
		// layer AND no earlier position) esc does nothing: it never reshapes the
		// board to fake a history it doesn't have.
		if len(m.navBack) > 0 {
			return m, m.navBackCmd()
		}
		return m, nil
	case "enter":
		if m.activityView {
			// Jump to the event's bead: select it on the board, or open its
			// detail if it's off the current view (closed/filtered).
			id := m.activityFocusID()
			if id == "" {
				return m, nil
			}
			m.activityView = false
			if !m.jumpTo(id) {
				m.loading = true
				return m, tea.Batch(m.showCmd(id), m.spin.Tick)
			}
			return m, nil
		}
		if m.view == ViewColumns {
			m.millerDrill() // enter drills, like l/right (append a column)
			return m, nil
		}
		if is := m.focusedIssue(); is != nil {
			// bd show is async (a Cmd); spin while it fetches.
			m.loading = true
			return m, tea.Batch(m.showCmd(is.ID), m.spin.Tick)
		}
	}
	return m, nil
}
