package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/awhitty/bb/internal/agentapi"
	"github.com/awhitty/bb/internal/bd"
)

func sharesBoard() []bd.Issue {
	return []bd.Issue{
		{ID: "a", Title: "A", Status: "open"},
		{ID: "b", Title: "B", Status: "open"},
		{ID: "c", Title: "C", Status: "open"},
	}
}

// With follow OFF (the default), an agent arrange PUBLISHES into the stream and
// does NOT seize the board.
func TestSharePublishDoesNotSeize(t *testing.T) {
	m := testModel(t, sharesBoard(), 120, 30)
	resp, _ := m.handleAgent(agentapi.ShowAction{IDs: []string{"b"}, Title: "just b"})

	if got := m.sharesNewestFirst(); len(got) != 1 {
		t.Fatalf("share not published: %d entries", len(got))
	}
	if len(m.attach.ids) != 0 || m.attach.active {
		t.Fatal("the board was seized — follow is off, it should only publish")
	}
	v := resp.Data.(*agentapi.View)
	if v.Shares == nil || v.Shares.Total != 1 || v.Shares.Following {
		t.Fatalf("view() shares state wrong: %+v", v.Shares)
	}
	if v.Shares.Entries[0].Name != "just b" || v.Shares.Entries[0].Type != "view" {
		t.Fatalf("published entry wrong: %+v", v.Shares.Entries[0])
	}
}

// The human browses with @ and applies the focused section's view with A, which
// arranges the board from that spec.
func TestSharePullApplies(t *testing.T) {
	m := testModel(t, sharesBoard(), 120, 30)
	m.handleAgent(agentapi.SpecAction{Mode: "tree", Title: "the tree"})
	if m.view == ViewTree {
		t.Fatal("follow off: set_view must not seize the board")
	}
	m = press(t, m, "@") // open the browser
	if !m.sharesBrowse {
		t.Fatal("@ should open the browser")
	}
	m = press(t, m, "A") // apply the focused section's view to the board
	if !m.attach.active || m.view != ViewTree {
		t.Fatalf("A should apply the focused share (view=%v applied=%v)", m.view, m.attach.active)
	}
	if m.sharesBrowse {
		t.Fatal("applying should close the browser")
	}
}

// While ATTACHED to a channel, a new push on it applies to the board live.
func TestAttachLiveApply(t *testing.T) {
	m := testModel(t, sharesBoard(), 120, 30)
	attachDefault(&m) // attached to the default channel
	m.handleAgent(agentapi.SpecAction{Mode: "tree", Title: "live tree"})
	if m.view != ViewTree {
		t.Fatal("attached: a push on the channel should apply live")
	}
}

// The channels persist across a restart (a fresh store reloads them): a view push
// lands in the unattributed default channel's ring, a name-drop lands as its
// session channel's ambient face, and the flat projection round-trips both.
func TestSharePersistence(t *testing.T) {
	m := testModel(t, sharesBoard(), 120, 30) // sets BB_CONFIG_DIR to a temp dir
	m.handleAgent(agentapi.ShowAction{IDs: []string{"b"}, Title: "report blockers"})
	m.handleAgent(agentapi.NameDropAction{SessionID: "sess-1", TS: "2026-07-08T10:00:00Z", IDs: []string{"a", "c"}})

	store := loadSessions() // reads the same temp config dir
	// The view push (no session) is the default channel; the name-drop is sess-1.
	if got := len(store.Channels); got != 2 {
		t.Fatalf("persisted %d channels, want 2", got)
	}
	def := findChannel(t, store, "")
	if len(def.PushRing) != 1 || def.PushRing[0].Name != "report blockers" {
		t.Fatalf("default channel push ring = %+v", def.PushRing)
	}
	sess := findChannel(t, store, "sess-1")
	if len(sess.AmbientBeads.IDs) != 2 {
		t.Fatalf("sess-1 ambient face = %+v", sess.AmbientBeads)
	}
	// The flat projection reconstitutes both entries with their kinds.
	flat := store.flat()
	if len(flat) != 2 {
		t.Fatalf("flat projection = %d entries, want 2", len(flat))
	}
	var views, mentions int
	for _, s := range flat {
		switch s.Kind {
		case shareViewKind:
			views++
		case shareMentioned:
			mentions++
		}
	}
	if views != 1 || mentions != 1 {
		t.Fatalf("flat kinds = %d view / %d mentioned, want 1/1", views, mentions)
	}
}

// findChannel returns the persisted channel for a session id (fails if absent).
func findChannel(t *testing.T, s sessionStore, id string) sessionChannel {
	t.Helper()
	for _, ch := range s.Channels {
		if ch.SessionID == id {
			return ch
		}
	}
	t.Fatalf("no channel for session %q in %+v", id, s.Channels)
	return sessionChannel{}
}

// view() reports the stream + follow so a connected agent knows what it shared.
func TestSharesMCPRoundTrip(t *testing.T) {
	m := testModel(t, sharesBoard(), 120, 30)
	m.handleAgent(agentapi.ShowAction{IDs: []string{"b"}, Title: "what to do next"})
	resp := m.agentView()
	if !strings.Contains(resp.Text, "<agent-shares ") {
		t.Fatalf("view XML missing <agent-shares>:\n%s", resp.Text)
	}
	if !strings.Contains(resp.Text, `name="what to do next"`) {
		t.Fatalf("view XML missing the published name:\n%s", resp.Text)
	}
	if !strings.Contains(resp.Text, `following="false"`) {
		t.Fatalf("view XML missing follow state:\n%s", resp.Text)
	}
}

// Applying an ids-based share resolves the ids against the WHOLE board, not the
// filtered display set — so a share naming a closed bead (off the default load)
// shows it instead of blanking to an empty "no issues" board. This is the
// regression guard for the intermittent blank Alex hit tabbing in via @.
func TestApplyIdsShareResolvesAgainstFullBoard(t *testing.T) {
	m := testModel(t, nil, 120, 30)
	// The display set excludes the closed bead 'c'; the full graph includes it —
	// exactly what a default `bd list` (open only) + `bd list --all` (graph) loads.
	display := []bd.Issue{{ID: "a", Title: "A", Status: "open"}, {ID: "b", Title: "B", Status: "open"}}
	full := []bd.Issue{{ID: "a", Title: "A", Status: "open"}, {ID: "b", Title: "B", Status: "open"}, {ID: "c", Title: "C", Status: "closed"}}
	next, _ := m.Update(issuesMsg{seq: m.seq, issues: display, graph: full})
	m = next.(Model)

	// An agent name-drops the closed bead 'c' (agents talk about beads they just
	// closed) — a "mentioned" share filtering to [c].
	m.handleAgent(agentapi.NameDropAction{SessionID: "s", TS: "2026-07-08T10:00:00Z", IDs: []string{"c"}})
	m = press(t, m, "@") // open the browser

	// The section lists the bead resolved against the FULL board (c is closed,
	// off the display set) — not a blank.
	if len(m.shareSections) != 1 || len(m.shareSections[0].beads) != 1 || m.shareSections[0].beads[0].ID != "c" {
		t.Fatalf("ids section resolved against the filtered set, not the full board: %+v", m.shareSections)
	}
	// Applying it arranges the board to just [c].
	m = press(t, m, "A")
	if !m.attach.active {
		t.Fatal("A did not apply the share")
	}
	vis := m.visibleIssues()
	if len(vis) != 1 || vis[0].ID != "c" {
		t.Fatalf("applied ids share resolved wrong: got %d %v", len(vis), vis)
	}
	if len(m.columns) == 0 {
		t.Fatal("board blanked applying an ids share whose bead is off the display filter")
	}
}

// A section whose spec matches zero beads shows "(no matches)" under its header,
// not a blank — and applying it still reads as the clear applied-share empty.
func TestEmptyShareShowsNoMatches(t *testing.T) {
	m := testModel(t, sharesBoard(), 120, 30)
	m.handleAgent(agentapi.NameDropAction{SessionID: "s", TS: "2026-07-08T10:00:00Z", IDs: []string{"ghost"}})
	m = press(t, m, "@")
	if len(m.shareSections) != 1 || len(m.shareSections[0].beads) != 0 {
		t.Fatalf("a section matching zero beads should have no beads: %+v", m.shareSections)
	}
	// The rows carry an explicit "(no matches)" marker, never a blank gap.
	var hasEmpty bool
	for _, r := range m.shareBrowseRows(m.boardWidth()) {
		if r.kind == sbEmpty {
			hasEmpty = true
		}
	}
	if !hasEmpty {
		t.Fatal("a zero-match section must render a (no matches) row")
	}
	// Applying it still reads as the clear applied-share empty, not broken-board.
	m = press(t, m, "A")
	if got := m.emptyBoardText(); got != "no matches for this share — esc to undo" {
		t.Fatalf("empty applied-share message = %q", got)
	}
}

// A hook name-drop registers (creates/refreshes) the originating session's
// channel: Title = the conversation name, Freshness = the batch timestamp, and
// the per-session ambient face = the ids + excerpts the hook sent.
func TestHookNameDropRegistersChannel(t *testing.T) {
	m := testModel(t, sharesBoard(), 120, 30)
	m.handleAgent(agentapi.NameDropAction{
		SessionID: "sess-1", ConvoName: "reporting cleanup", TS: "2026-07-08T10:00:00Z",
		IDs: []string{"a", "b"},
		Excerpts: []agentapi.Excerpt{
			{Text: "closing a and b", Mentions: []agentapi.Mention{{ID: "a", Start: 8, End: 9}}},
		},
	})
	ch := findChannel(t, m.sessions, "sess-1")
	if ch.Title != "reporting cleanup" {
		t.Fatalf("channel title = %q, want the conversation name", ch.Title)
	}
	if ch.Freshness.Format("2006-01-02T15:04:05Z07:00") != "2026-07-08T10:00:00Z" {
		t.Fatalf("channel freshness = %v, want the batch ts", ch.Freshness)
	}
	if len(ch.AmbientBeads.IDs) != 2 || ch.AmbientBeads.IDs[0] != "a" || ch.AmbientBeads.IDs[1] != "b" {
		t.Fatalf("ambient ids = %+v, want [a b]", ch.AmbientBeads.IDs)
	}
	if len(ch.AmbientBeads.Excerpts) != 1 {
		t.Fatalf("ambient excerpts not retained: %+v", ch.AmbientBeads.Excerpts)
	}

	// A fresher name-drop on the same session refreshes the face latest-wins.
	m.handleAgent(agentapi.NameDropAction{
		SessionID: "sess-1", ConvoName: "reporting cleanup", TS: "2026-07-08T11:00:00Z",
		IDs: []string{"c"},
	})
	ch = findChannel(t, m.sessions, "sess-1")
	if len(ch.AmbientBeads.IDs) != 1 || ch.AmbientBeads.IDs[0] != "c" {
		t.Fatalf("ambient face did not refresh latest-wins: %+v", ch.AmbientBeads.IDs)
	}
	if ch.Freshness.Format("2006-01-02T15:04:05Z07:00") != "2026-07-08T11:00:00Z" {
		t.Fatalf("freshness did not advance: %v", ch.Freshness)
	}
}

// A published view carries its originating session id onto that session's own
// channel ring (never the human's screen, with follow off); a second push on the
// same session keeps the prior one browsable; an unattributed push lands on the
// shared default channel and does not leak into a session's ring.
func TestPushAttributionRoutesToSessionChannel(t *testing.T) {
	m := testModel(t, sharesBoard(), 120, 30)

	// A set_view attributed to session X lands on X's push ring; the board is not
	// seized (follow is off) — the push goes to the channel, not the screen.
	m.handleAgent(agentapi.SpecAction{Mode: "tree", Title: "X first", Session: "sess-X"})
	if m.view == ViewTree || m.attach.active {
		t.Fatal("an attributed push must not seize the screen with follow off")
	}
	chX := findChannel(t, m.sessions, "sess-X")
	if len(chX.PushRing) != 1 || chX.PushRing[0].Name != "X first" {
		t.Fatalf("push did not land on session X's ring: %+v", chX.PushRing)
	}

	// A second push on X makes it the latest (front of the face) while keeping the
	// prior one browsable in the ring.
	m.handleAgent(agentapi.SpecAction{Mode: "kanban", Title: "X second", Session: "sess-X"})
	chX = findChannel(t, m.sessions, "sess-X")
	if len(chX.PushRing) != 2 || chX.PushRing[0].Name != "X first" || chX.PushRing[1].Name != "X second" {
		t.Fatalf("second push should keep the prior browsable: %+v", chX.PushRing)
	}

	// An unattributed push (no session) lands on the shared default channel ("").
	m.handleAgent(agentapi.SpecAction{Mode: "list", Title: "no session"})
	def := findChannel(t, m.sessions, "")
	if len(def.PushRing) != 1 || def.PushRing[0].Name != "no session" {
		t.Fatalf("unattributed push should land on the default channel: %+v", def.PushRing)
	}
	// It did not leak into session X's ring.
	if chX = findChannel(t, m.sessions, "sess-X"); len(chX.PushRing) != 2 {
		t.Fatalf("the unattributed push leaked into session X: %+v", chX.PushRing)
	}
}

// The live-sessions browser shows one section per channel with a PER-SESSION
// face: a channel that pushed a deliberate view previews that view's beads; a
// channel that only name-dropped shows its own recently-touched beads. Two
// different sessions therefore present two different faces.
func TestPerChannelFace(t *testing.T) {
	m := testModel(t, sharesBoard(), 120, 30) // beads a, b, c
	// Session P pushes a deliberate view of [b] — its section previews that view.
	m.handleAgent(agentapi.ShowAction{IDs: []string{"b"}, Title: "P's pick", Session: "sess-P"})
	// Session Q only name-drops [a] (no push) — its section shows recently-touched.
	m.handleAgent(agentapi.NameDropAction{SessionID: "sess-Q", ConvoName: "Q chat", TS: "2026-07-08T10:00:00Z", IDs: []string{"a"}})

	m = press(t, m, "@")
	if len(m.shareSections) != 2 {
		t.Fatalf("want one section per channel (2), got %d", len(m.shareSections))
	}

	secBySession := func(id string) *shareSection {
		for i := range m.shareSections {
			if m.shareSections[i].ch.SessionID == id {
				return &m.shareSections[i]
			}
		}
		t.Fatalf("no section for session %q", id)
		return nil
	}

	// P's channel: a pushed view, previewing its own bead [b].
	p := secBySession("sess-P")
	if p.ambient {
		t.Fatal("a channel that pushed a view must not read as the ambient face")
	}
	if got := ids(p.beads); len(got) != 1 || got[0] != "b" {
		t.Fatalf("pushed channel preview = %v, want [b]", got)
	}

	// Q's channel: no push, so the recently-touched ambient face of [a].
	q := secBySession("sess-Q")
	if !q.ambient {
		t.Fatal("a channel with no push must present its ambient recently-touched face")
	}
	if q.note != "recently touched" {
		t.Fatalf("ambient channel note = %q, want \"recently touched\"", q.note)
	}
	if got := ids(q.beads); len(got) != 1 || got[0] != "a" {
		t.Fatalf("ambient channel face = %v, want [a] (its own name-drop)", got)
	}

	// The two faces genuinely differ (a different session shows a different face).
	if ids(p.beads)[0] == ids(q.beads)[0] {
		t.Fatal("two sessions should present two different faces")
	}

	// The header carries the channel title + its live state/freshness, and an
	// archived channel reads as ended.
	hdr := renderShareSectionHeader(*secBySession("sess-Q"), m.boardWidth(), time.Now())
	if !strings.Contains(hdr, "Q chat") || !strings.Contains(hdr, "live") {
		t.Fatalf("live header missing title/state: %q", hdr)
	}
	m.archiveSession("sess-Q")
	hdr = renderShareSectionHeader(*secBySession("sess-Q"), m.boardWidth(), time.Now())
	if !strings.Contains(hdr, "ended") {
		t.Fatalf("archived channel header should read ended: %q", hdr)
	}
}

// Applying a share (A) arranges the board; esc then undoes it (the agent-slot
// restore), returning the human's prior view.
func TestApplyThenEscUndoesShare(t *testing.T) {
	m := testModel(t, sharesBoard(), 120, 30)
	m.handleAgent(agentapi.ShowAction{IDs: []string{"a"}, Title: "just a"})
	m = press(t, m, "@") // open the browser
	m = press(t, m, "A") // attach to the focused channel
	if !m.attach.active {
		t.Fatal("A did not attach to the channel")
	}
	if m.sharesBrowse {
		t.Fatal("attaching should close the browser")
	}
	m = press(t, m, "esc") // esc detaches back to the human's view
	if m.attach.active {
		t.Fatal("esc should detach the attached channel")
	}
}

// In the browser, A applies the focused SECTION's view — j moves focus across
// sections, so applying picks the section under the cursor.
func TestBrowseApplyFocusedSection(t *testing.T) {
	m := testModel(t, sharesBoard(), 120, 30)
	m.handleAgent(agentapi.SpecAction{Mode: "tree", Title: "older tree", Session: "s1"}) // no beads (whole-board mode)
	m.handleAgent(agentapi.ShowAction{IDs: []string{"a"}, Title: "newer ids", Session: "s2"})

	m = press(t, m, "@")
	// Newest first: section 0 = "newer ids" ([a]), section 1 = "older tree" (empty).
	if m.shareSecIdx != 0 {
		t.Fatalf("focus should start on the first non-empty section, got %d", m.shareSecIdx)
	}
	// Force focus onto the tree section and apply it.
	m.shareSecIdx = 1
	m = press(t, m, "A")
	if m.view != ViewTree {
		t.Fatal("applying the tree section should arrange the board as a tree")
	}
}

// buildShareSections resolves each spec to beads against the FULL board: ids
// in-process, subtree= via the full-board resolver — no per-section bd call.
func TestBuildShareSectionsResolvesSpecs(t *testing.T) {
	m := testModel(t, nil, 120, 30)
	full := []bd.Issue{
		{ID: "E", Title: "epic", Status: "open"},
		{ID: "c1", Title: "child", Parent: "E", Status: "open"},
		{ID: "x", Title: "other", Status: "open"},
	}
	next, _ := m.Update(issuesMsg{seq: m.seq, issues: full, graph: full})
	m = next.(Model)
	m.handleAgent(agentapi.ShowAction{Query: "subtree=E", Title: "the E subtree", Session: "s1"})
	m.handleAgent(agentapi.ShowAction{IDs: []string{"x"}, Title: "just x", Session: "s2"})

	m.buildShareSections()
	if len(m.shareSections) != 2 {
		t.Fatalf("want 2 sections, got %d", len(m.shareSections))
	}
	// Newest first: [0] = "just x" ([x]); [1] = subtree=E (E + descendant c1).
	if got := ids(m.shareSections[0].beads); len(got) != 1 || got[0] != "x" {
		t.Fatalf("ids section = %v, want [x]", got)
	}
	if got := ids(m.shareSections[1].beads); len(got) != 2 {
		t.Fatalf("subtree section = %v, want E,c1", got)
	}
}

// j/k flow focus across ALL sections' beads, skipping sections that matched zero.
func TestBrowseCrossSectionNavSkipsEmpty(t *testing.T) {
	m := testModel(t, nil, 120, 30)
	board := []bd.Issue{{ID: "a", Status: "open"}, {ID: "b", Status: "open"}}
	next, _ := m.Update(issuesMsg{seq: m.seq, issues: board, graph: board})
	m = next.(Model)
	// Distinct sessions with explicit descending freshness so the order is
	// [0]=s3([b]) · [1]=s2(ghost, empty) · [2]=s1([a,b]) — the empty channel sits
	// in the MIDDLE, which j/k must skip.
	m.handleAgent(agentapi.NameDropAction{SessionID: "s1", TS: "2026-07-08T10:00:00Z", IDs: []string{"a", "b"}})
	m.handleAgent(agentapi.NameDropAction{SessionID: "s2", TS: "2026-07-08T11:00:00Z", IDs: []string{"ghost"}})
	m.handleAgent(agentapi.NameDropAction{SessionID: "s3", TS: "2026-07-08T12:00:00Z", IDs: []string{"b"}})

	m = press(t, m, "@")
	if is := m.shareBrowseFocused(); is == nil || is.ID != "b" {
		t.Fatalf("initial focus = %v, want b", is)
	}
	m = press(t, m, "j") // down skips the empty middle section
	if m.shareSecIdx != 2 {
		t.Fatalf("down should skip the empty section to section 2, got %d", m.shareSecIdx)
	}
	if is := m.shareBrowseFocused(); is == nil || is.ID != "a" {
		t.Fatalf("focus after skip = %v, want a", is)
	}
	m = press(t, m, "k") // up skips back across the empty section
	if m.shareSecIdx != 0 {
		t.Fatalf("up should skip back to section 0, got %d", m.shareSecIdx)
	}
}

// A share published WHILE the browser is open live-updates the open browser:
// the new section appears immediately (newest first) without waiting for a
// manual refresh, and the human's cursor is preserved by bead id.
func TestPublishWhileBrowsingLiveUpdates(t *testing.T) {
	m := testModel(t, sharesBoard(), 120, 30)
	m.handleAgent(agentapi.ShowAction{IDs: []string{"a", "b"}, Title: "first", Session: "s1"})
	m = press(t, m, "@") // open the browser on the one section
	if len(m.shareSections) != 1 {
		t.Fatalf("want 1 section on open, got %d", len(m.shareSections))
	}
	// Move focus to bead "b" in the section so we can prove focus survives.
	m = press(t, m, "j")
	if is := m.shareBrowseFocused(); is == nil || is.ID != "b" {
		t.Fatalf("focus before publish = %v, want b", is)
	}

	// An agent on a different session publishes a NEW share while the browser is
	// open — a new channel, its own section.
	m.handleAgent(agentapi.ShowAction{IDs: []string{"c"}, Title: "second", Session: "s2"})

	// The open browser rebuilt: the new section is present, newest-first.
	if len(m.shareSections) != 2 {
		t.Fatalf("open browser did not live-update: %d sections", len(m.shareSections))
	}
	if m.shareSections[0].sh.Name != "second" {
		t.Fatalf("newest section not first: %q", m.shareSections[0].sh.Name)
	}
	// Focus stays on "b" in the (now second) "first" section — not yanked to the top.
	if is := m.shareBrowseFocused(); is == nil || is.ID != "b" {
		t.Fatalf("focus not preserved across live publish = %v, want b", is)
	}
	// No phantom unseen accrues while actively browsing.
	if m.shareNew != 0 {
		t.Fatalf("shareNew should stay 0 while browsing, got %d", m.shareNew)
	}
}

// A board reload while the browser is open re-resolves each section's beads
// against the fresh board (a just-created bead now resolves), cursor preserved.
func TestBoardReloadWhileBrowsingReResolves(t *testing.T) {
	m := testModel(t, nil, 120, 30)
	board := []bd.Issue{{ID: "a", Title: "A", Status: "open"}, {ID: "b", Title: "B", Status: "open"}}
	next, _ := m.Update(issuesMsg{seq: m.seq, issues: board, graph: board})
	m = next.(Model)
	// A mentioned batch names a bead "d" that is NOT yet on the board.
	m.handleAgent(agentapi.NameDropAction{SessionID: "s", TS: "2026-07-08T10:00:00Z", IDs: []string{"a", "d"}})
	m = press(t, m, "@")
	if got := ids(m.shareSections[0].beads); len(got) != 1 || got[0] != "a" {
		t.Fatalf("section before reload = %v, want [a] (d not on board yet)", got)
	}

	// The board reloads with "d" now present (an agent created it via bd elsewhere).
	fuller := []bd.Issue{
		{ID: "a", Title: "A", Status: "open"},
		{ID: "b", Title: "B", Status: "open"},
		{ID: "d", Title: "D", Status: "open"},
	}
	next, _ = m.Update(issuesMsg{seq: m.seq, issues: fuller, graph: fuller})
	m = next.(Model)

	if !m.sharesBrowse {
		t.Fatal("a board reload should not close the open browser")
	}
	// The section re-resolved against the fresh board: "d" now appears.
	if got := ids(m.shareSections[0].beads); len(got) != 2 || got[0] != "a" || got[1] != "d" {
		t.Fatalf("section did not re-resolve against the fresh board = %v, want [a d]", got)
	}
}

// Focus is preserved by bead id across a rebuild even when the newest section
// pushes the focused section down the list.
func TestShareFocusPreservedByIDAcrossRebuild(t *testing.T) {
	m := testModel(t, sharesBoard(), 120, 30)
	m.handleAgent(agentapi.ShowAction{IDs: []string{"a", "b", "c"}, Title: "keep", Session: "s1"})
	m = press(t, m, "@")
	m = press(t, m, "j")
	m = press(t, m, "j") // focus on "c"
	if is := m.shareBrowseFocused(); is == nil || is.ID != "c" {
		t.Fatalf("setup focus = %v, want c", is)
	}
	shareID, beadID := m.focusedShareIdentity()
	if beadID != "c" {
		t.Fatalf("focusedShareIdentity bead = %q, want c", beadID)
	}
	// A rebuild that reorders (a new section jumps to the top) keeps the cursor.
	m.handleAgent(agentapi.ShowAction{IDs: []string{"a"}, Title: "newest", Session: "s2"})
	if is := m.shareBrowseFocused(); is == nil || is.ID != "c" {
		t.Fatalf("focus not preserved by id = %v, want c", is)
	}
	// The focused section identity is unchanged (still the "keep" share).
	gotShareID, _ := m.focusedShareIdentity()
	if gotShareID != shareID {
		t.Fatalf("focused section identity changed: %q vs %q", gotShareID, shareID)
	}
}

// The browser height-fits: many sections still render the frame at exactly the
// terminal height (the page-jump window windows the rows), never overflowing.
func TestSharesBrowseHeightFit(t *testing.T) {
	m := testModel(t, sharesBoard(), 120, 30)
	for i := 0; i < 30; i++ {
		m.handleAgent(agentapi.ShowAction{IDs: []string{"a", "b", "c"}, Title: "big view"})
	}
	m = press(t, m, "@")
	frameLines(t, m, 120, 30) // the whole frame is exactly 30 lines
	bodyH := m.layoutScreen().Body.H
	if got := len(strings.Split(m.viewSharesBrowse(bodyH), "\n")); got != bodyH {
		t.Fatalf("browser pane rendered %d lines, want bodyH=%d", got, bodyH)
	}
}

// A mentioned batch carrying excerpts renders as excerpt prose with inline
// chiclets (not a plain bead list); tab cycles focus across the chiclets in
// reading order and the preview follows the focused bead.
func TestMentionedExcerptsRenderAndNavigate(t *testing.T) {
	m := testModel(t, sharesBoard(), 120, 30)
	m.handleAgent(agentapi.NameDropAction{
		SessionID: "sess-1", ConvoName: "rework the mentioned entries",
		TS: "2026-07-08T10:00:00Z", IDs: []string{"a", "b", "c"},
		Excerpts: []agentapi.Excerpt{
			{Text: "closing a and starting b now", Mentions: []agentapi.Mention{
				{ID: "a", Start: 8, End: 9}, {ID: "b", Start: 23, End: 24}}},
			{Text: "then c blocks the release", Mentions: []agentapi.Mention{
				{ID: "c", Start: 5, End: 6}}},
		},
	})
	m = press(t, m, "@")
	sec := m.shareSections[0]
	if !sec.hasExcerpts() {
		t.Fatal("a mentioned batch with excerpts should render as excerpts")
	}
	// Three chiclets (a, b, c) across the two excerpts are the focus stops.
	if len(sec.focus) != 3 {
		t.Fatalf("want 3 focus stops (chiclets), got %d: %+v", len(sec.focus), sec.focus)
	}
	if sec.focus[0].beadID != "a" || sec.focus[1].beadID != "b" || sec.focus[2].beadID != "c" {
		t.Fatalf("chiclet reading order wrong: %+v", sec.focus)
	}
	// The header names the conversation, not a bead count.
	if got := m.shareSections[0].sh.Name; got != "rework the mentioned entries" {
		t.Fatalf("mentioned header name = %q", got)
	}
	// Initial focus + preview on the first chiclet's bead.
	if is := m.shareBrowseFocused(); is == nil || is.ID != "a" {
		t.Fatalf("initial focus = %v, want a", is)
	}
	// tab advances chiclet-by-chiclet; the preview follows.
	m = press(t, m, "tab")
	if is := m.shareBrowseFocused(); is == nil || is.ID != "b" {
		t.Fatalf("after tab, focus = %v, want b", is)
	}
	m = press(t, m, "tab") // crosses into the second excerpt
	if is := m.shareBrowseFocused(); is == nil || is.ID != "c" {
		t.Fatalf("after 2nd tab, focus = %v, want c", is)
	}
	if m.previewIssue() == nil || m.previewIssue().ID != "c" {
		t.Fatalf("preview did not follow the focused chiclet")
	}
	// The rendered body carries the excerpt prose (chiclets are inline in it).
	body := m.viewSharesBrowse(m.layoutScreen().Body.H)
	if !strings.Contains(body, "starting") || !strings.Contains(body, "blocks the release") {
		t.Fatalf("excerpt prose missing from the browser body:\n%s", body)
	}
}

// A legacy mentioned share (ids only, no excerpts) still renders as a plain bead
// list under a generic header — the backward-compat guard.
func TestLegacyMentionedRendersAsBeadList(t *testing.T) {
	m := testModel(t, sharesBoard(), 120, 30)
	// No ConvoName, no Excerpts — exactly what an older hook binary sends.
	m.handleAgent(agentapi.NameDropAction{SessionID: "s", TS: "2026-07-08T10:00:00Z", IDs: []string{"a", "b"}})
	m = press(t, m, "@")
	sec := m.shareSections[0]
	if sec.hasExcerpts() {
		t.Fatal("a mentioned share without excerpts must not render as excerpts")
	}
	if len(sec.beads) != 2 || len(sec.focus) != 2 {
		t.Fatalf("legacy mentioned should list its 2 beads, got beads=%d focus=%d", len(sec.beads), len(sec.focus))
	}
	// The flattened rows are bead rows, not excerpt lines.
	var beadRows, exRows int
	for _, r := range m.shareBrowseRows(m.boardWidth()) {
		switch r.kind {
		case sbBead:
			beadRows++
		case sbExcerpt:
			exRows++
		}
	}
	if beadRows != 2 || exRows != 0 {
		t.Fatalf("legacy render: beadRows=%d exRows=%d, want 2/0", beadRows, exRows)
	}
}

// Excerpts persist through the store round-trip (a restart reloads them).
func TestExcerptsPersist(t *testing.T) {
	m := testModel(t, sharesBoard(), 120, 30)
	m.handleAgent(agentapi.NameDropAction{
		SessionID: "s", ConvoName: "a thread", TS: "2026-07-08T10:00:00Z",
		IDs: []string{"a"}, Excerpts: []agentapi.Excerpt{{Text: "about a", Mentions: []agentapi.Mention{{ID: "a", Start: 6, End: 7}}}},
	})
	store := loadSessions()
	sess := findChannel(t, store, "s")
	if len(sess.AmbientBeads.Excerpts) != 1 || sess.AmbientBeads.Excerpts[0].Text != "about a" {
		t.Fatalf("excerpts did not persist: %+v", sess.AmbientBeads)
	}
}
