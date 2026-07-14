package ui

import tea "github.com/charmbracelet/bubbletea"

// layers.go is the esc dismissal stack — one of the TWO explicit stacks that
// drive "going back", each carrying a disjoint set of interactions:
//
//   HISTORY (navBack/navFwd, walked by `[` and `]`) is the view-state timeline:
//   every real view / filter / drill / detail transition, checkpointed by
//   recordNav. See nav.go.
//
//   LAYER (m.layers, popped by esc) is the ordered set of things composed OVER
//   the active view: an emphasis decoration, an attached agent's arrangement, an
//   analyst filter, the activity feed, a relationship scope, and the facet control.
//
// esc peels exactly ONE layer per press, most-recently-raised first; with the
// stack empty it defers to the HISTORY timeline (navBackCmd), and at true base
// (no layer, no earlier position) it does nothing — it never reshapes the board.
//
// Every layer maps onto state that already lives on the Model; the stack only
// records the ORDER those layers were raised, so esc undoes them last-in-first-
// out rather than by a fixed priority. Each kind has a liveness predicate — a
// layer another path already tore down (a detach over MCP, emphasis
// cleared by the agent, a committed control) is skipped — and a dismiss action
// that is its existing revert. The two full-viewState reverts (the facet
// control's cancelControl and the scope's exitScope) flow through here too.

type layerKind int

const (
	layerEmphasis layerKind = iota
	layerAttach
	layerAnalyst
	layerActivity
	layerScope
	layerControl
)

// pushLayer raises a layer to the top of the esc stack. Re-raising a kind moves
// its existing entry to the top instead of duplicating it, so the stack holds at
// most one entry per kind, in most-recently-raised order.
func (m *Model) pushLayer(k layerKind) {
	out := m.layers[:0]
	for _, l := range m.layers {
		if l != k {
			out = append(out, l)
		}
	}
	m.layers = append(out, k)
}

// layerLive reports whether a layer's underlying state is still active. A layer
// can be dismissed by a path other than esc (a detach over MCP, emphasis
// cleared by the agent, a control commit), so popLayer skips entries that no
// longer describe live state instead of tracking every teardown.
func (m Model) layerLive(k layerKind) bool {
	switch k {
	case layerEmphasis:
		return len(m.emphasis) > 0
	case layerAttach:
		return m.attach.active
	case layerAnalyst:
		return len(m.analystIDs) > 0 || m.analystText != ""
	case layerActivity:
		return m.activityView
	case layerScope:
		return m.scopeRoot != ""
	case layerControl:
		return m.control.open
	}
	return false
}

// popLayer dismisses the topmost LIVE layer, returning its command and whether
// one was found. Stale entries (already torn down elsewhere) are discarded on
// the way down, so one esc press peels one real layer.
func (m *Model) popLayer() (tea.Cmd, bool) {
	for len(m.layers) > 0 {
		top := m.layers[len(m.layers)-1]
		m.layers = m.layers[:len(m.layers)-1]
		if !m.layerLive(top) {
			continue // already dismissed by another path
		}
		return m.dismissLayer(top), true
	}
	return nil, false
}

// dismissLayer runs one layer's revert. navigatingHistory is set first so the
// esc that triggered the dismissal is never itself recorded as a new history
// checkpoint (recordNav consumes the flag) — the anti-self-record seam the
// scattered esc branches used to set inline.
func (m *Model) dismissLayer(k layerKind) tea.Cmd {
	m.navigatingHistory = true
	switch k {
	case layerEmphasis:
		m.clearEmphasis()
		m.setDetailContent()
		m.setMessage("emphasis cleared", false)
		return nil
	case layerAttach:
		cmd := m.detach()
		m.setMessage("detached — back to your view", false)
		return cmd
	case layerAnalyst:
		m.analystIDs = nil
		m.analystText = ""
		m.rebuild()
		m.setMessage("", false)
		return nil
	case layerActivity:
		m.activityView = false
		return nil
	case layerScope:
		return m.exitScope()
	case layerControl:
		m.cancelControl()
		return nil
	}
	return nil
}
