package ui

import (
	"testing"

	"github.com/awhitty/bb/internal/agentapi"
)

// TestEscPeelsLayersLIFO proves esc walks the LAYER stack most-recently-raised
// first: with a relationship scope raised and then an emphasis on top, the first
// esc peels ONLY the emphasis (the scope beneath is untouched) and the second
// esc exits the scope. One esc = one layer.
func TestEscPeelsLayersLIFO(t *testing.T) {
	const w, h = 140, 40
	m := testModel(t, swimFixture(), w, h)
	attachDefault(&m) // attached: the agent's pushes apply and raise their layers

	// Raise the SCOPE layer: the attached agent scopes to g-R's neighborhood.
	m.handleAgent(agentapi.SpecAction{Scope: "g-R"})
	if m.scopeRoot != "g-R" {
		t.Fatalf("scope=g-R should set scopeRoot, got %q", m.scopeRoot)
	}

	// Raise the EMPHASIS layer on top of the scope.
	m.handleAgent(agentapi.EmphasizeAction{Targets: []agentapi.Emphasis{
		{Kind: "issue", Ref: "g-c1", Style: "highlight"},
	}})
	if len(m.emphasis) == 0 {
		t.Fatal("emphasis should be active after the emphasize action")
	}

	// First esc peels the most-recent layer: emphasis clears, scope stays.
	m = press(t, m, "esc")
	if len(m.emphasis) != 0 {
		t.Fatalf("first esc should clear the emphasis layer, %d left", len(m.emphasis))
	}
	if m.scopeRoot != "g-R" {
		t.Fatalf("first esc must not touch the scope beneath it, scopeRoot=%q", m.scopeRoot)
	}

	// Second esc peels the scope: back to the pre-scope kanban (the attach layer,
	// raised first, is still beneath — a third esc would detach).
	m = press(t, m, "esc")
	if m.scopeRoot != "" {
		t.Fatalf("second esc should exit the scope, scopeRoot=%q", m.scopeRoot)
	}
	if m.view != ViewKanban {
		t.Fatalf("exiting the scope should restore the pre-scope kanban, view=%v", m.view)
	}
}

// TestEscAtBaseDoesNothing proves esc stops cleanly at base: with no layer and
// no earlier position it never reshapes the board (the deleted last-resort floor
// that used to fake history by dropping the filter / leaving swim).
func TestEscAtBaseDoesNothing(t *testing.T) {
	const w, h = 120, 30
	m := testModel(t, graphFixture(), w, h) // fresh board: no layers, no history

	beforeView := m.view
	beforeIDs := boardIDs(m)
	m = press(t, m, "esc")
	if m.view != beforeView {
		t.Fatalf("esc at base changed the view %v -> %v", beforeView, m.view)
	}
	if got := boardIDs(m); !eqIDs(got, beforeIDs) {
		t.Fatalf("esc at base reshaped the board: %v -> %v", beforeIDs, got)
	}
}

// TestScopeIsLayerNotHistory proves the "no interaction is on both stacks"
// invariant for a relationship scope: pressing R raises the scope LAYER (esc
// territory) and records NO history checkpoint, so the scope lives on the layer
// stack alone. esc peels it via the layer stack; because entry left no phantom
// checkpoint, a following `[` finds no earlier position (it never double-counts
// the same interaction). A plain mode switch, by contrast, DOES checkpoint.
func TestScopeIsLayerNotHistory(t *testing.T) {
	m := testModel(t, graphFixture(), 120, 30)
	if len(m.navBack) != 0 {
		t.Fatalf("precondition: navBack = %d, want 0", len(m.navBack))
	}

	// R enters the relationship scope + swimlane: a LAYER is raised.
	m = press(t, m, "R")
	if m.scopeRoot == "" {
		t.Fatal("R should enter a relationship scope")
	}
	// The scope must NOT have checkpointed the history timeline — it is a layer.
	if len(m.navBack) != 0 {
		t.Fatalf("entering a scope recorded a history checkpoint (%d) — scope is on BOTH stacks", len(m.navBack))
	}

	// esc peels the scope through the LAYER stack.
	m = press(t, m, "esc")
	if m.scopeRoot != "" {
		t.Fatalf("esc should peel the scope layer, scopeRoot=%q", m.scopeRoot)
	}
	// No phantom checkpoint from the entry: `[` reports no earlier position
	// (the scope did not also live on the history stack).
	backView := m.view
	m = press(t, m, "[")
	if m.view != backView {
		t.Fatalf("[ after esc-detaching the scope moved the view %v -> %v — a phantom history entry from the scope", backView, m.view)
	}
}

// TestAttachIsLayerNotHistory proves the same invariant for an attach: attaching
// to a channel (A in the browser) raises the attach LAYER and records NO history
// checkpoint, so esc detaches via the layer stack and no phantom `[` back remains.
func TestAttachIsLayerNotHistory(t *testing.T) {
	m := testModel(t, sharesBoard(), 120, 30)
	m.handleAgent(agentapi.SpecAction{Mode: "tree", Title: "s1 tree", Session: "s1"})
	// Note the pre-attach history depth (attachSession opens the browser + presses A).
	backBefore := len(m.navBack)
	m = attachSession(t, m, "s1")
	if !m.attach.active {
		t.Fatal("A should attach to the channel")
	}
	if len(m.navBack) != backBefore {
		t.Fatalf("attaching recorded a history checkpoint (%d -> %d) — attach is on BOTH stacks", backBefore, len(m.navBack))
	}
	m = press(t, m, "esc")
	if m.attach.active {
		t.Fatal("esc should detach")
	}
	// No phantom: with no earlier position the board must not move on `[`.
	backView := m.view
	m = press(t, m, "[")
	if m.view != backView {
		t.Fatalf("[ after esc-detaching moved the view %v -> %v — a phantom history entry from the attach", backView, m.view)
	}
}

// TestModeSwitchIsHistoryNotLayer is the complementary half of the invariant: a
// plain mode switch (which raises NO layer) DOES checkpoint the history timeline,
// so esc/[ walk back across it — the interaction is on the history stack alone.
func TestModeSwitchIsHistoryNotLayer(t *testing.T) {
	m := testModel(t, graphFixture(), 120, 30)
	if len(m.layers) != 0 {
		t.Fatalf("precondition: layers = %v, want none", m.layers)
	}
	m = press(t, m, "5") // enter the tree — a mode switch, not a layer
	if m.view != ViewTree {
		t.Fatal("5 should enter the tree")
	}
	if len(m.layers) != 0 {
		t.Fatalf("a mode switch raised a layer %v — it belongs to the history stack, not the layer stack", m.layers)
	}
	if len(m.navBack) != 1 {
		t.Fatalf("a mode switch should checkpoint history, navBack=%d", len(m.navBack))
	}
}

// TestEscSkipsStaleLayer proves a layer another path already tore down is
// discarded, not re-dismissed: emphasis is raised and then cleared by the agent
// (a non-esc path), so the following esc finds nothing live and is a clean no-op.
func TestEscSkipsStaleLayer(t *testing.T) {
	const w, h = 120, 30
	m := testModel(t, graphFixture(), w, h)
	attachDefault(&m) // attach layer at the base, so emphasis applies over it

	// The agent raises emphasis (over the attach layer) and then clears it by a
	// NON-esc path, leaving a stale emphasis entry on the stack above the live attach.
	m.handleAgent(agentapi.EmphasizeAction{Targets: []agentapi.Emphasis{
		{Kind: "issue", Ref: "g-2", Style: "highlight"},
	}})
	m.handleAgent(agentapi.ClearEmphasisAction{})
	if len(m.emphasis) != 0 {
		t.Fatal("precondition: emphasis cleared by the agent")
	}

	beforeView := m.view
	// One esc discards the stale emphasis entry and peels the next LIVE layer (the
	// attach) — never re-dismissing the dead one, and never reshaping the board.
	m = press(t, m, "esc")
	if m.attach.active {
		t.Fatal("esc should skip the stale emphasis and detach the live attach beneath")
	}
	if m.view != beforeView {
		t.Fatalf("esc reshaped the view: %v -> %v", beforeView, m.view)
	}
}
