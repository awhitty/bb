package ui

import (
	"strings"
	"testing"

	"github.com/awhitty/bb/internal/agentapi"
	"github.com/awhitty/bb/internal/bd"
)

func agentDo(t *testing.T, m Model, a agentapi.Action) (Model, agentapi.Response) {
	t.Helper()
	reply := make(chan agentapi.Response, 1)
	next, _ := m.Update(agentapi.Request{Action: a, Reply: reply})
	select {
	case resp := <-reply:
		return next.(Model), resp
	default:
		t.Fatal("agent request got no reply")
		return m, agentapi.Response{}
	}
}

// The XML must claim exactly what is visible — window ranges, hidden counts,
// and the user focus mark.
func TestAgentViewXMLTruthfulness(t *testing.T) {
	const w, h = 120, 30                    // borderless board: cardWindow = bodyH-3 = 25
	m := testModel(t, deepColumn(73), w, h) // the multi-column board is the default layout
	// Walk deep so the window pages, RENDERING each frame like the live TUI
	// (page-jump state advances during View): focus row 41 → window 26-50.
	for i := 0; i < 40; i++ {
		m = press(t, m, "j")
		frameLines(t, m, w, h)
	}
	m, resp := agentDo(t, m, agentapi.ViewAction{})
	x := resp.Text
	for _, must := range []string{
		`<screen focus="board">`,
		`mode="status · board" mode-by="user"`,
		`<column key="open" count="73" visible="26-50" hidden-above="25" hidden-below="23">`,
		`row="41" focused="true"`, // the walk landed on row 41 (1-based)
		`<panel open="false"/>`,
	} {
		if !strings.Contains(x, must) {
			t.Fatalf("XML missing %q:\n%s", must, x)
		}
	}
	// Visible-only hard rule: rows 1-25 exist but are NOT visible.
	if strings.Contains(x, `row="1"`) || strings.Contains(x, `row="25"`) {
		t.Fatalf("XML claims a hidden card is visible:\n%s", x)
	}
	if got := strings.Count(x, `focused="true"`); got != 1 {
		t.Fatalf("exactly one user focus, got %d", got)
	}
	// Exactly the window's cards serialize.
	if got := strings.Count(x, "<card "); got != 25 {
		t.Fatalf("serialized %d cards, window is 25", got)
	}
	// Structured mirror agrees.
	v := resp.Data.(*agentapi.View)
	if v.Board.Columns[0].FirstRow != 26 || v.Board.Columns[0].LastRow != 50 {
		t.Fatalf("structured rows = %d-%d", v.Board.Columns[0].FirstRow, v.Board.Columns[0].LastRow)
	}
}

func TestAgentShowSelectResetLifecycle(t *testing.T) {
	const w, h = 120, 30
	m := testModel(t, graphFixture(), w, h)
	attachDefault(&m) // attached to the agent's channel, so its show/set_view apply live

	// show(ids, title): arrangement applies, provenance = agent, footer notice.
	m, resp := agentDo(t, m, agentapi.ShowAction{IDs: []string{"g-2", "g-3"}, Title: "blocked pair"})
	if resp.Err != "" {
		t.Fatal(resp.Err)
	}
	for _, must := range []string{
		`filter-by="agent"`, `filter-title="blocked pair"`,
		`id="g-2"`, `id="g-3"`,
		"agent: showing blocked pair",
	} {
		if !strings.Contains(resp.Text, must) {
			t.Fatalf("show XML missing %q:\n%s", must, resp.Text)
		}
	}
	if strings.Contains(resp.Text, `id="g-1"`) {
		t.Fatal("show(ids) must display exactly the requested list")
	}

	// select: in-view id focuses (panel opens on request)...
	panel := true
	m, resp = agentDo(t, m, agentapi.SelectAction{ID: "g-3", Panel: &panel})
	if resp.Err != "" {
		t.Fatal(resp.Err)
	}
	if !strings.Contains(resp.Text, `id="g-3" row="1" focused="true"`) ||
		!strings.Contains(resp.Text, `<panel open="true" issue="g-3"`) {
		t.Fatalf("select XML wrong:\n%s", resp.Text)
	}
	// ...an out-of-view id errors honestly with a hint, without rearranging.
	m, resp = agentDo(t, m, agentapi.SelectAction{ID: "g-1"})
	if resp.Err == "" || !strings.Contains(resp.Err, "not in the current view") {
		t.Fatalf("select must refuse out-of-view ids, got %+v", resp)
	}

	// A second show REPLACES the slot (single slot semantics).
	m, resp = agentDo(t, m, agentapi.ShowAction{IDs: []string{"g-1"}, Title: "epic only"})
	if !strings.Contains(resp.Text, `filter-title="epic only"`) {
		t.Fatalf("second show did not replace the slot:\n%s", resp.Text)
	}

	// reset(): the user's original unfiltered board comes back.
	m, resp = agentDo(t, m, agentapi.ResetAction{})
	if resp.Err != "" {
		t.Fatal(resp.Err)
	}
	if !strings.Contains(resp.Text, `id="g-1"`) || !strings.Contains(resp.Text, `id="g-2"`) {
		t.Fatalf("reset did not restore the full board:\n%s", resp.Text)
	}
	if strings.Contains(resp.Text, `filter-by="agent"`) {
		t.Fatal("reset must drop agent provenance")
	}
	if m.attach.active {
		t.Fatal("agent slot must clear on reset")
	}
}

// The human outranks the agent: esc detaches the arrangement.
func TestEscDismissesAgentArrangement(t *testing.T) {
	m := testModel(t, graphFixture(), 120, 30)
	attachDefault(&m)
	m, _ = agentDo(t, m, agentapi.ShowAction{IDs: []string{"g-2"}, Title: "just one"})
	if !m.attach.active {
		t.Fatal("arrangement should be active")
	}
	m = press(t, m, "esc")
	if m.attach.active {
		t.Fatal("esc must detach the agent arrangement")
	}
	if len(m.columns) == 0 || len(m.columns[0].Issues) < 3 {
		t.Fatalf("user view not restored: %+v", m.columns)
	}
}

func TestAgentShowQueryAppliesPrefetchedIssues(t *testing.T) {
	m := testModel(t, graphFixture(), 120, 30)
	attachDefault(&m)
	fetched := []bd.Issue{
		{ID: "g-9", Title: "Fresh from bd", Status: "open", Priority: 1, IssueType: "bug"},
	}
	m, resp := agentDo(t, m, agentapi.ShowAction{Query: "type=bug", Title: "bugs", Issues: fetched})
	if !strings.Contains(resp.Text, `filter="query &quot;type=bug&quot;" filter-by="agent"`) &&
		!strings.Contains(resp.Text, `type=bug`) {
		t.Fatalf("query provenance missing:\n%s", resp.Text)
	}
	if !strings.Contains(resp.Text, `id="g-9"`) {
		t.Fatalf("pre-fetched issues not displayed:\n%s", resp.Text)
	}
	// reset() restores the user's (empty) query → needs a reload note.
	_, resp = agentDo(t, m, agentapi.ResetAction{})
	if !strings.Contains(resp.Text, "reloading") {
		t.Fatalf("query reset must note the reload:\n%s", resp.Text)
	}
}

// detach restores ONLY the attach spec scope. The sort + collapse the agent
// drove revert to the human's baseline; the panel/detail surface state the human
// already had is preserved (restoreAttach writes back only the spec knobs). A
// union snapshot()->restore() would revert those too — this locks that out.
func TestDetachRevertsSpecKeepsPanelDetail(t *testing.T) {
	m := testModel(t, graphFixture(), 120, 30)
	// The human's own surface state before attaching: the panel and a detail open.
	m = press(t, m, " ")
	if !m.panelOpen {
		t.Fatal("space should open the panel")
	}
	focused := m.focusedIssue().ID
	next, _ := m.Update(detailMsg{issue: bd.Issue{ID: focused, Title: "d", Status: "open"}})
	m = next.(Model)
	if m.detail == nil {
		t.Fatal("detail should open")
	}

	attachDefault(&m)
	userSort := m.flatSort
	// The agent (attached channel) arranges with a different sort and a collapsed node.
	m, resp := agentDo(t, m, agentapi.SpecAction{
		SortKey:  "title", // a flat key ≠ the default priority sort
		Collapse: &agentapi.CollapseSpec{NodeIDs: []string{"g-1"}},
		Title:    "agent arrangement",
	})
	if resp.Err != "" {
		t.Fatal(resp.Err)
	}
	if m.flatSort == userSort {
		t.Fatal("the agent's sort should differ from the user's baseline")
	}
	if !m.collapsed["g-1"] {
		t.Fatal("the agent's collapse did not apply")
	}
	// reset() detaches: the agent's spec scope reverts, the human's panel/detail stay.
	m, _ = agentDo(t, m, agentapi.ResetAction{})
	if m.flatSort != userSort {
		t.Fatalf("detach must revert the agent's sort, got %+v", m.flatSort)
	}
	if len(m.collapsed) != 0 {
		t.Fatalf("detach must revert the agent's collapse, got %v", m.collapsed)
	}
	if !m.panelOpen {
		t.Fatal("detach reverted the human's panel — union restore regression")
	}
	if m.detail == nil {
		t.Fatal("detach reverted the human's detail — union restore regression")
	}
}

func TestAgentTreeSerializationNests(t *testing.T) {
	issues := []bd.Issue{
		{ID: "g-1", Title: "Epic", Status: "open", Priority: 1, IssueType: "epic"},
		{ID: "g-1.1", Title: "Child", Status: "open", Priority: 0, IssueType: "task", Parent: "g-1"},
		{ID: "g-1.1.1", Title: "Grandchild", Status: "open", Priority: 2, IssueType: "task", Parent: "g-1.1"},
		{ID: "g-2", Title: "Loner", Status: "open", Priority: 3, IssueType: "chore"},
	}
	m := testModel(t, issues, 120, 30)
	m = press(t, m, "5")
	_, resp := agentDo(t, m, agentapi.ViewAction{})
	x := resp.Text
	if !strings.Contains(x, `<tree relation="hierarchy"`) {
		t.Fatalf("tree element missing:\n%s", x)
	}
	// Nesting mirrors indentation: grandchild inside child inside epic.
	iEpic := strings.Index(x, `id="g-1"`)
	iChild := strings.Index(x, `id="g-1.1"`)
	iGrand := strings.Index(x, `id="g-1.1.1"`)
	iClose := strings.Index(x, "</node>")
	if !(iEpic < iChild && iChild < iGrand && iGrand < iClose) {
		t.Fatalf("tree nesting order wrong (epic=%d child=%d grand=%d close=%d):\n%s", iEpic, iChild, iGrand, iClose, x)
	}
	if got := strings.Count(x, "<node "); got != 4 {
		t.Fatalf("nodes = %d, want 4", got)
	}
	if strings.Count(x, "</node>") != 4 {
		t.Fatalf("unbalanced node closes:\n%s", x)
	}
}
