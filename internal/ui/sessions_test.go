package ui

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/awhitty/bb/internal/agentapi"
)

// The session-channel store round-trips through sessions.json: channels, their
// ambient faces, push rings, lifecycle state, and the global sequence all survive
// a save + reload, and the flat projection reconstitutes the same ordered stream.
func TestSessionStoreRoundTrip(t *testing.T) {
	t.Setenv("BB_CONFIG_DIR", t.TempDir())

	var s sessionStore
	t0 := time.Date(2026, 7, 8, 10, 0, 0, 0, time.UTC)

	// A view push into the unattributed default channel, then a name-drop into a
	// named session channel, then a second view push — each takes the next seq.
	ingest := func(sh share) {
		sh.seq = s.nextSeq()
		s.ingest(sh)
	}
	ingest(share{ID: "v1", Kind: shareViewKind, Name: "first view", TS: t0, Spec: shareSpec{IDs: []string{"a"}}})
	ingest(share{ID: "m1", Kind: shareMentioned, Name: "a thread", SessionID: "sess-1", TS: t0.Add(time.Minute),
		Spec:     shareSpec{IDs: []string{"b", "c"}},
		Excerpts: []agentapi.Excerpt{{Text: "about b and c", Mentions: []agentapi.Mention{{ID: "b", Start: 6, End: 7}}}}})
	ingest(share{ID: "v2", Kind: shareViewKind, Name: "second view", TS: t0.Add(2 * time.Minute), Spec: shareSpec{Mode: "tree"}})

	if err := s.save(); err != nil {
		t.Fatalf("save: %v", err)
	}
	got := loadSessions()

	// Two channels: the default "" (two pushes in its ring) and "sess-1" (ambient).
	if len(got.Channels) != 2 {
		t.Fatalf("reloaded %d channels, want 2", len(got.Channels))
	}
	if got.Seq != 3 {
		t.Fatalf("reloaded seq = %d, want 3", got.Seq)
	}
	def := findChannel(t, got, "")
	if len(def.PushRing) != 2 || def.PushRing[0].Name != "first view" || def.PushRing[1].Spec.Mode != "tree" {
		t.Fatalf("default channel push ring did not round-trip: %+v", def.PushRing)
	}
	if def.State != channelLive {
		t.Fatalf("default channel state = %q, want live", def.State)
	}
	sess := findChannel(t, got, "sess-1")
	if sess.Title != "a thread" || len(sess.AmbientBeads.IDs) != 2 || len(sess.AmbientBeads.Excerpts) != 1 {
		t.Fatalf("sess-1 channel did not round-trip: %+v", sess)
	}
	if !sess.Freshness.Equal(t0.Add(time.Minute)) {
		t.Fatalf("sess-1 freshness = %v, want %v", sess.Freshness, t0.Add(time.Minute))
	}

	// The flat projection reconstitutes all three entries in publish (seq) order.
	flat := got.flat()
	if len(flat) != 3 {
		t.Fatalf("flat projection = %d entries, want 3", len(flat))
	}
	wantIDs := []string{"v1", "m1", "v2"}
	for i, id := range wantIDs {
		if flat[i].ID != id {
			t.Fatalf("flat[%d].ID = %q, want %q (order: %+v)", i, flat[i].ID, id, flat)
		}
	}
}

// A fresh store persists at 0600 (the secrets-hygiene default the store honors).
func TestSessionStoreFileMode(t *testing.T) {
	t.Setenv("BB_CONFIG_DIR", t.TempDir())
	var s sessionStore
	s.nextSeq()
	s.ingest(share{ID: "v", Kind: shareViewKind, Name: "v", TS: time.Now(), Spec: shareSpec{IDs: []string{"x"}}})
	if err := s.save(); err != nil {
		t.Fatalf("save: %v", err)
	}
	info, err := os.Stat(sessionsPath())
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("sessions.json mode = %o, want 600", perm)
	}
}

// archive marks the ended session's channel State=archived (the SessionEnd
// trigger), is a no-op for an unknown session, and is idempotent. A fresh
// name-drop afterward re-livens the channel.
func TestSessionArchive(t *testing.T) {
	var s sessionStore
	t0 := time.Date(2026, 7, 8, 10, 0, 0, 0, time.UTC)
	s.ingest(share{ID: "m1", Kind: shareMentioned, SessionID: "sess-1", TS: t0, Spec: shareSpec{IDs: []string{"a"}}})

	if s.archive("nope") {
		t.Fatal("archiving an unknown session must be a no-op")
	}
	if !s.archive("sess-1") {
		t.Fatal("archiving a live channel must report a change")
	}
	if st := findChannel(t, s, "sess-1").State; st != channelArchived {
		t.Fatalf("channel state = %q, want archived", st)
	}
	if s.archive("sess-1") {
		t.Fatal("archiving an already-archived channel must be a no-op")
	}
	// A later name-drop from the same session re-livens it.
	s.ingest(share{ID: "m2", Kind: shareMentioned, SessionID: "sess-1", TS: t0.Add(time.Hour), Spec: shareSpec{IDs: []string{"b"}}})
	if st := findChannel(t, s, "sess-1").State; st != channelLive {
		t.Fatalf("re-ingested channel state = %q, want live", st)
	}
}

// browseOrder lists one channel per section, ordered live → stale → archived and
// then by freshness (newest first); a channel with neither a push nor an ambient
// face is dropped.
func TestBrowseOrder(t *testing.T) {
	var s sessionStore
	base := time.Date(2026, 7, 8, 10, 0, 0, 0, time.UTC)
	mention := func(id string, when time.Time) share {
		return share{ID: id, Kind: shareMentioned, SessionID: id, TS: when, Spec: shareSpec{IDs: []string{"x"}}}
	}
	ingest := func(sh share) { sh.seq = s.nextSeq(); s.ingest(sh) }

	ingest(mention("live-old", base.Add(-time.Hour)))
	ingest(mention("live-new", base))
	ingest(mention("stale-one", base.Add(-2*time.Hour)))
	ingest(mention("ended-one", base.Add(-3*time.Hour)))
	// An empty channel (registered, but no push and no ambient beads) is dropped.
	s.channel("empty")

	s.markStale(base, 30*time.Minute) // live-old, stale-one, ended-one all decay
	// Re-liven the two we want live; stale-one stays stale.
	ingest(mention("live-old", base))
	ingest(mention("live-new", base.Add(time.Minute)))
	s.archive("ended-one")

	got := s.browseOrder()
	want := []string{"live-new", "live-old", "stale-one", "ended-one"}
	if len(got) != len(want) {
		t.Fatalf("browseOrder returned %d channels, want %d: %+v", len(got), len(want), got)
	}
	for i, id := range want {
		if got[i].SessionID != id {
			t.Fatalf("browseOrder[%d] = %q, want %q (full: %+v)", i, got[i].SessionID, id, sessionIDs(got))
		}
	}
}

func sessionIDs(chs []sessionChannel) []string {
	out := make([]string, len(chs))
	for i, ch := range chs {
		out[i] = ch.SessionID
	}
	return out
}

// A SessionEnd action, dispatched through the UI loop exactly as the /session-end
// hook delivers it, archives that session's channel (the conversation-end
// trigger) — the dispatch wiring, not just the store method. The archived channel
// stops reading live: a stale sweep never touches it again.
func TestSessionEndActionArchivesChannel(t *testing.T) {
	m := testModel(t, sharesBoard(), 120, 30)
	// A live session channel, registered via a name-drop.
	m.handleAgent(agentapi.NameDropAction{SessionID: "sess-1", ConvoName: "a chat", TS: "2026-07-08T10:00:00Z", IDs: []string{"a"}})
	if st := findChannel(t, m.sessions, "sess-1").State; st != channelLive {
		t.Fatalf("precondition: channel state = %q, want live", st)
	}

	// The conversation ends: the SessionEnd action archives the channel.
	m.handleAgent(agentapi.SessionEndAction{SessionID: "sess-1"})
	if st := findChannel(t, m.sessions, "sess-1").State; st != channelArchived {
		t.Fatalf("SessionEnd did not archive the channel: state = %q", st)
	}
	// Archived is terminal for the freshness sweep — it never reads live again.
	m.sessions.markStale(time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC), 30*time.Minute)
	if st := findChannel(t, m.sessions, "sess-1").State; st != channelArchived {
		t.Fatalf("an archived channel must stay archived through a sweep, got %q", st)
	}
}

// The old flat mentions ring (shares.json) is retired: loading the session store
// deletes a lingering shares.json so the old stored shares are cleared and never
// re-read, and the channels come only from sessions.json.
func TestLegacySharesFileCleared(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("BB_CONFIG_DIR", dir)
	// Seed a leftover shares.json from the retired mentions-ring era.
	legacy := filepath.Join(dir, "shares.json")
	if err := os.WriteFile(legacy, []byte(`[{"id":"old","type":"mentioned","name":"stale mention"}]`), 0o600); err != nil {
		t.Fatalf("seed legacy shares.json: %v", err)
	}

	store := loadSessions()

	// The legacy file is gone (the old entries can never resurface).
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatalf("loadSessions did not clear the legacy shares.json (stat err=%v)", err)
	}
	// Nothing from the old ring leaked into the channel store.
	if len(store.Channels) != 0 || !store.empty() {
		t.Fatalf("legacy shares leaked into the session store: %+v", store)
	}
}

// markStale decays only silent LIVE channels past the threshold; a fresh channel,
// an archived channel, and an already-stale channel are left as-is.
func TestSessionMarkStale(t *testing.T) {
	var s sessionStore
	base := time.Date(2026, 7, 8, 10, 0, 0, 0, time.UTC)
	// live-old: last refreshed an hour ago; live-new: just now; ended: archived.
	s.ingest(share{ID: "a", Kind: shareMentioned, SessionID: "live-old", TS: base.Add(-time.Hour), Spec: shareSpec{IDs: []string{"x"}}})
	s.ingest(share{ID: "b", Kind: shareMentioned, SessionID: "live-new", TS: base, Spec: shareSpec{IDs: []string{"y"}}})
	s.ingest(share{ID: "c", Kind: shareMentioned, SessionID: "ended", TS: base.Add(-time.Hour), Spec: shareSpec{IDs: []string{"z"}}})
	s.archive("ended")

	if !s.markStale(base, 30*time.Minute) {
		t.Fatal("markStale must report a change when a channel decays")
	}
	if st := findChannel(t, s, "live-old").State; st != channelStale {
		t.Fatalf("live-old state = %q, want stale", st)
	}
	if st := findChannel(t, s, "live-new").State; st != channelLive {
		t.Fatalf("live-new state = %q, want live (still fresh)", st)
	}
	if st := findChannel(t, s, "ended").State; st != channelArchived {
		t.Fatalf("ended state = %q, want archived (terminal, never restaled)", st)
	}
	// A second sweep changes nothing (stale is idempotent).
	if s.markStale(base, 30*time.Minute) {
		t.Fatal("second sweep must be a no-op")
	}
}
