package ui

import (
	"testing"

	"github.com/awhitty/bb/internal/agentapi"
)

// attachSession opens the sessions browser, focuses the section for the given
// session, and presses A to attach to it — the human's A/enter in the browser.
func attachSession(t *testing.T, m Model, session string) Model {
	t.Helper()
	m = press(t, m, "@")
	if !m.sharesBrowse {
		t.Fatal("@ should open the sessions browser")
	}
	found := false
	for i := range m.shareSections {
		if m.shareSections[i].ch.SessionID == session {
			m.shareSecIdx, m.shareBeadIdx = i, 0
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("no section for session %q", session)
	}
	return press(t, m, "A")
}

// Attaching a live session shows the agent's current view, and each new push on
// THAT channel applies live; a push on a different channel does not disturb it.
func TestAttachShowsCurrentViewAndLiveUpdates(t *testing.T) {
	m := testModel(t, sharesBoard(), 120, 30)
	baseView := m.view // the default board

	// The agent on s1 publishes a tree view — not attached yet, so publish-only.
	m.handleAgent(agentapi.SpecAction{Mode: "tree", Title: "s1 tree", Session: "s1"})
	if m.view != baseView {
		t.Fatalf("a publish must not seize before attach, view=%v", m.view)
	}

	// Attaching shows the agent's current (tree) view.
	m = attachSession(t, m, "s1")
	if !m.attach.active || m.attach.session != "s1" {
		t.Fatalf("attach state wrong: %+v", m.attach)
	}
	if m.view != ViewTree {
		t.Fatalf("attaching should show the agent's tree view, got %v", m.view)
	}

	// A new push on the ATTACHED channel applies live (latest-wins).
	m.handleAgent(agentapi.SpecAction{Mode: "kanban", Title: "s1 board", Session: "s1"})
	if m.view != ViewKanban {
		t.Fatalf("a live push on the attached channel should apply, got %v", m.view)
	}

	// A push on a DIFFERENT channel does not touch the attached view.
	m.handleAgent(agentapi.SpecAction{Mode: "list", Title: "s2 list", Session: "s2"})
	if m.view != ViewKanban {
		t.Fatalf("a push on an unattached channel must not apply, got %v", m.view)
	}
}

// Sticky attach: a nav key moves the cursor within the agent's view and STAYS
// attached — it does not detach. The human can arrow around what the agent pushed.
func TestNavKeysStayAttached(t *testing.T) {
	m := testModel(t, sharesBoard(), 120, 30)
	m.handleAgent(agentapi.SpecAction{Mode: "tree", Title: "s1 tree", Session: "s1"})
	m = attachSession(t, m, "s1")
	if m.view != ViewTree {
		t.Fatalf("precondition: attached tree, got %v", m.view)
	}
	if m.treeIdx != 0 {
		t.Fatalf("precondition: cursor at row 0, got %d", m.treeIdx)
	}

	m = press(t, m, "j") // a human navigation key: moves the cursor, stays attached
	if !m.attach.active {
		t.Fatal("a nav key must NOT detach — the attach is sticky")
	}
	if m.view != ViewTree {
		t.Fatalf("a nav key must keep the agent's view, got %v", m.view)
	}
	if m.treeIdx != 1 {
		t.Fatalf("j should move the cursor within the attached view, treeIdx=%d", m.treeIdx)
	}

	// A LOCAL view change (v cycles the view) also stays attached.
	m = press(t, m, "v")
	if !m.attach.active {
		t.Fatal("a local view change must keep the attach")
	}
}

// After the human fiddles locally (moves the cursor, changes the view), the agent's
// next push RE-ASSERTS the channel's view — latest push wins, applied live.
func TestPushReassertsOverLocalChanges(t *testing.T) {
	m := testModel(t, sharesBoard(), 120, 30)
	m.handleAgent(agentapi.SpecAction{Mode: "tree", Title: "s1 tree", Session: "s1"})
	m = attachSession(t, m, "s1")
	if m.view != ViewTree {
		t.Fatalf("precondition: attached tree, got %v", m.view)
	}

	// The human fiddles: move the cursor and change the view locally.
	m = press(t, m, "j", "v")
	if !m.attach.active {
		t.Fatal("local fiddling must keep the attach")
	}

	// The agent pushes a fresh view on the attached channel — it re-asserts live.
	m.handleAgent(agentapi.SpecAction{Mode: "kanban", Title: "s1 board", Session: "s1"})
	if !m.attach.active {
		t.Fatal("a re-asserting push must keep the attach")
	}
	if m.view != ViewKanban {
		t.Fatalf("the latest push should re-assert its view over local changes, got %v", m.view)
	}
}

// history-clean: local moves while attached are ephemeral within the attach layer.
// esc restores the EXACT pre-attach view, and NO phantom history checkpoint leaks
// onto the [ / ] timeline from the fiddling.
func TestAttachHistoryCleanAfterDetach(t *testing.T) {
	m := testModel(t, sharesBoard(), 120, 30)
	baseView := m.view
	baseTree := m.treeIdx
	backBefore := len(m.navBack)
	m.handleAgent(agentapi.SpecAction{Mode: "tree", Title: "s1 tree", Session: "s1"})
	m = attachSession(t, m, "s1")

	// Fiddle: move the cursor and cycle the view locally.
	m = press(t, m, "j", "v", "j")
	if len(m.navBack) != backBefore {
		t.Fatalf("local moves while attached polluted history (%d -> %d)", backBefore, len(m.navBack))
	}

	// esc detaches and restores the exact pre-attach view + cursor.
	m = press(t, m, "esc")
	if m.attach.active {
		t.Fatal("esc should detach")
	}
	if m.view != baseView {
		t.Fatalf("esc-detach should restore the pre-attach view %v, got %v", baseView, m.view)
	}
	if m.treeIdx != baseTree {
		t.Fatalf("esc-detach should restore the pre-attach cursor %d, got %d", baseTree, m.treeIdx)
	}

	// No phantom history: with no earlier position the board must not move on [.
	backView := m.view
	m = press(t, m, "[")
	if m.view != backView {
		t.Fatalf("[ after detach moved the view %v -> %v — a phantom checkpoint from the fiddling", backView, m.view)
	}
}

// esc detaches back to the human's prior view via the layer stack.
func TestEscDetachesToPriorView(t *testing.T) {
	m := testModel(t, sharesBoard(), 120, 30)
	baseView := m.view
	m.handleAgent(agentapi.SpecAction{Mode: "tree", Title: "s1 tree", Session: "s1"})
	m = attachSession(t, m, "s1")
	if m.view != ViewTree {
		t.Fatalf("precondition: attached tree, got %v", m.view)
	}

	m = press(t, m, "esc")
	if m.attach.active {
		t.Fatal("esc should detach")
	}
	if m.view != baseView {
		t.Fatalf("esc-detach should restore the prior view %v, got %v", baseView, m.view)
	}
}

// Switching attach between two sessions swaps cleanly: the second channel's view
// shows with no residual arrangement (scope/facets) from the first, and detaching
// after a switch returns to the human's ORIGINAL base, not the first channel's view.
func TestSwitchAttachSwapsCleanly(t *testing.T) {
	m := testModel(t, graphFixture(), 120, 30)
	baseView := m.view

	// s1 pushes a scoped tree; s2 pushes a plain board.
	m.handleAgent(agentapi.SpecAction{Mode: "tree", Scope: "g-1", Title: "s1 scoped tree", Session: "s1"})
	m.handleAgent(agentapi.SpecAction{Mode: "kanban", Title: "s2 board", Session: "s2"})

	m = attachSession(t, m, "s1")
	if m.view != ViewTree || m.scopeRoot != "g-1" {
		t.Fatalf("attach s1: view=%v scope=%q", m.view, m.scopeRoot)
	}

	// Switch to s2: the s1 scope must not linger.
	m = attachSession(t, m, "s2")
	if m.attach.session != "s2" {
		t.Fatalf("attach session = %q, want s2", m.attach.session)
	}
	if m.view != ViewKanban {
		t.Fatalf("switch should show s2's board, got %v", m.view)
	}
	if m.scopeRoot != "" {
		t.Fatalf("switching left residual scope %q from s1", m.scopeRoot)
	}

	// Detaching after a switch restores the human's ORIGINAL base, not s1's view.
	m = press(t, m, "esc")
	if m.attach.active {
		t.Fatal("esc should detach")
	}
	if m.view != baseView {
		t.Fatalf("detach after a switch should restore the human base %v, got %v", baseView, m.view)
	}
	if m.scopeRoot != "" {
		t.Fatalf("detach left residual scope %q", m.scopeRoot)
	}
}
