package ui

import (
	"strings"
	"testing"

	"github.com/awhitty/bb/internal/agentapi"
)

// nameSession registers a conversation name for a session the way the Stop hook
// does — a name-drop carrying ConvoName lands as the channel's Title.
func nameSession(t *testing.T, m *Model, session, convo string) {
	t.Helper()
	m.handleAgent(agentapi.NameDropAction{
		SessionID: session, ConvoName: convo,
		TS: "2026-07-10T10:00:00Z", IDs: []string{"a"},
	})
	if ch := findChannel(t, m.sessions, session); ch.Title != convo {
		t.Fatalf("session %q title = %q, want %q", session, ch.Title, convo)
	}
}

// Surface 1 — the attach badge names the SESSION being driven (Alex's
// conversation name), not the view title, and stays visible while attached.
func TestAttachBadgeNamesSession(t *testing.T) {
	m := testModel(t, sharesBoard(), 120, 30)
	nameSession(t, &m, "s1", "beads-shop")
	m.handleAgent(agentapi.SpecAction{Mode: "tree", Title: "s1 tree", Session: "s1"})

	m = attachSession(t, m, "s1")
	if !m.attach.active || m.sharesBrowse {
		t.Fatalf("precondition: attached with the browser closed (active=%v browse=%v)", m.attach.active, m.sharesBrowse)
	}
	hdr := m.viewHeader()
	if !strings.Contains(hdr, "attached: beads-shop") {
		t.Fatalf("attach badge does not name the session:\n%s", hdr)
	}
	// It names the session, not the view title ("s1 tree").
	if strings.Contains(hdr, "s1 tree") {
		t.Fatalf("attach badge shows the view title instead of the session name:\n%s", hdr)
	}
}

// Surface 2 — the arriving-share footer names the originating session and the
// view it shared, replacing the old generic "agent shared".
func TestFooterNamesSessionOnPush(t *testing.T) {
	m := testModel(t, sharesBoard(), 120, 30)
	nameSession(t, &m, "s1", "beads-shop")
	// A view push on that session, not attached — it announces in the footer.
	m.handleAgent(agentapi.SpecAction{Mode: "tree", Title: "report blockers", Session: "s1"})

	if !strings.Contains(m.message, "beads-shop shared “report blockers”") {
		t.Fatalf("footer does not name the session + view: %q", m.message)
	}
	if strings.Contains(m.message, "agent shared") {
		t.Fatalf("footer still uses the generic agent label: %q", m.message)
	}
	if !strings.Contains(m.message, "@ to browse") {
		t.Fatalf("footer dropped the browse affordance: %q", m.message)
	}
}

// Surface 3 — a browse section for a named session heads its rows with the
// session name (with freshness), and the view it pushed nests under that name
// rather than repeating an anonymous row.
func TestBrowseHeaderNamesSession(t *testing.T) {
	m := testModel(t, sharesBoard(), 120, 30)
	nameSession(t, &m, "s1", "beads-shop")
	m.handleAgent(agentapi.SpecAction{Mode: "relationship", Root: "a", Title: "the a graph", Session: "s1"})

	m = press(t, m, "@")
	var sec *shareSection
	for i := range m.shareSections {
		if m.shareSections[i].ch.SessionID == "s1" {
			sec = &m.shareSections[i]
		}
	}
	if sec == nil {
		t.Fatal("no browse section for s1")
	}
	hdr := renderShareSectionHeader(*sec, m.boardWidth(), m.sessions.Channels[0].Freshness)
	if !strings.Contains(hdr, "beads-shop") {
		t.Fatalf("browse header does not name the session: %q", hdr)
	}
	// The pushed view nests under the named session as a note, not a header.
	if !strings.Contains(sec.note, "relationship") {
		t.Fatalf("pushed view did not nest as a note under the session: %q", sec.note)
	}
}

// Surface 4 — an un-pushed session's ambient recently-touched face is headed by
// the session name.
func TestAmbientFaceHeadedBySessionName(t *testing.T) {
	m := testModel(t, sharesBoard(), 120, 30)
	m.handleAgent(agentapi.NameDropAction{
		SessionID: "s2", ConvoName: "demo-shop", TS: "2026-07-10T10:00:00Z",
		IDs: []string{"a", "b"},
	})
	m = press(t, m, "@")
	if len(m.shareSections) != 1 {
		t.Fatalf("want 1 section, got %d", len(m.shareSections))
	}
	sec := m.shareSections[0]
	if !sec.ambient {
		t.Fatal("an un-pushed session should present its ambient recently-touched face")
	}
	if got := shareHeaderTitle(sec); got != "demo-shop" {
		t.Fatalf("ambient face header = %q, want the session name", got)
	}
}

// Fallback — a session that never named itself reads as a short session-id stub
// in both the footer and the attach badge (never the generic "agent shared").
func TestUnnamedSessionFallsBackToStub(t *testing.T) {
	m := testModel(t, sharesBoard(), 120, 30)
	// A push with no prior name-drop: the channel has no Title.
	m.handleAgent(agentapi.SpecAction{Mode: "tree", Title: "x", Session: "sess-abcdefghijklmnop"})

	if !strings.Contains(m.message, "session sess-abcdefg shared “x”") {
		t.Fatalf("footer stub wrong: %q", m.message)
	}

	m = attachSession(t, m, "sess-abcdefghijklmnop")
	hdr := m.viewHeader()
	if !strings.Contains(hdr, "attached: session sess-abcdefg") {
		t.Fatalf("attach badge stub wrong:\n%s", hdr)
	}
}
