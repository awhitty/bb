package ui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/awhitty/bb/internal/agentapi"
	"github.com/awhitty/bb/internal/discover"
)

// sessions.go is the session-channel store: the agent-shares surface keyed by
// Claude Code SessionID. It replaced the flat mentions-stream ring (the old
// bounded []share persisted to shares.json). One channel per session collects
// that session's activity: its LATEST ambient name-drop (the beads it is talking
// about) and a bounded browsable ring of the views it published. The store
// persists to ~/.config/bb/sessions.json (0600) via the discover.ConfigDir
// seam, so channels survive restarts.
//
// This is the store layer only. The richer behaviors — attributing a view push
// to its originating session (the MCP `session` argument), freshness-based
// staleness, archive-on-SessionEnd, and the per-channel browser face — are wired
// in later stages. Here a freshly ingested channel is simply live, and the
// existing @ browser reads a flat projection (flat) of the channels.

// channelState is a session channel's lifecycle stage. The store holds the field;
// the transitions between the states are a later stage.
type channelState string

const (
	channelLive     channelState = "live"
	channelStale    channelState = "stale"
	channelArchived channelState = "archived"
)

// maxPushRing bounds a channel's browsable push history — latest-wins keeps the
// freshest push at the front of the face, the last few stay browsable.
const maxPushRing = 8

// stalenessThreshold is how long a channel may go without a refresh (a Stop-hook
// name-drop or a view push) before the sweep marks it stale. The Stop hook fires
// every turn, so a channel quiet this long is a crashed or abandoned session, not
// an in-flight turn — it must never keep reading live.
const stalenessThreshold = 30 * time.Minute

// ambientBeads is a session's LATEST name-dropped batch: the bead ids it named
// and the excerpt prose around them. Latest-wins — a fresh name-drop from the
// same session replaces it. Name/ID/Seq/TS carry the browse metadata the @ list
// needs (the conversation header, a stable id, and the publish position/time).
type ambientBeads struct {
	ID       string             `json:"id,omitempty"`
	Name     string             `json:"name,omitempty"`
	Seq      int64              `json:"seq,omitempty"`
	TS       time.Time          `json:"ts,omitempty"`
	IDs      []string           `json:"ids,omitempty"`
	Excerpts []agentapi.Excerpt `json:"excerpts,omitempty"`
}

// empty reports whether the ambient face carries nothing to show.
func (a ambientBeads) empty() bool { return len(a.IDs) == 0 && len(a.Excerpts) == 0 }

// pushEntry is one browsable push in a channel's ring. Spec is the set_view MCP
// algebra (shareSpec) VERBATIM — the push payload; the surrounding fields are the
// browse metadata (name/time/id) the @ browser lists it by.
type pushEntry struct {
	ID   string    `json:"id"`
	Name string    `json:"name,omitempty"`
	Seq  int64     `json:"seq"`
	TS   time.Time `json:"ts"`
	Spec shareSpec `json:"spec"`
}

// sessionChannel is one Claude Code session's channel: the beads it is talking
// about (AmbientBeads, latest-wins) plus the views it published (PushRing, a
// bounded browsable ring). Title is the conversation name; Freshness tracks the
// newest activity; State is the lifecycle stage.
type sessionChannel struct {
	SessionID    string       `json:"session_id"`
	Title        string       `json:"title,omitempty"`
	Freshness    time.Time    `json:"freshness"`
	State        channelState `json:"state"`
	AmbientBeads ambientBeads `json:"ambient_beads"`
	PushRing     []pushEntry  `json:"push_ring,omitempty"`
}

// sessionStore is the per-session channel store. Seq is the monotonic publish
// counter that orders the flat projection stably regardless of clock resolution.
type sessionStore struct {
	Channels []sessionChannel `json:"channels"`
	Seq      int64            `json:"seq"`
}

// sessionsPath is the persisted store location under the config dir seam.
func sessionsPath() string { return filepath.Join(discover.ConfigDir(), "sessions.json") }

// loadSessions reads the persisted channels (best-effort: a missing/corrupt file
// is just an empty store, never a user-facing error) and retires the old flat
// mentions ring (shares.json) if it is still lying around.
func loadSessions() sessionStore {
	_ = os.Remove(filepath.Join(discover.ConfigDir(), "shares.json"))
	raw, err := os.ReadFile(sessionsPath())
	if err != nil {
		return sessionStore{}
	}
	var s sessionStore
	if json.Unmarshal(raw, &s) != nil {
		return sessionStore{}
	}
	return s
}

// save persists the store to sessions.json (0600).
func (s sessionStore) save() error {
	if err := os.MkdirAll(discover.ConfigDir(), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(s, "", " ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(sessionsPath(), raw, 0o600); err != nil {
		return err
	}
	return os.Chmod(sessionsPath(), 0o600)
}

// saveSessions persists the store (best-effort; a write failure is logged, never
// fatal — the in-memory store still works for the session).
func (m *Model) saveSessions() {
	if err := m.sessions.save(); err != nil {
		m.logger.Error("sessions save", "err", err)
	}
}

// archiveSession archives the ended session's channel (the SessionEnd trigger),
// persists, and rebuilds the open browser so an ended conversation stops reading
// live immediately.
func (m *Model) archiveSession(id string) {
	if m.sessions.archive(id) {
		m.saveSessions()
		if m.sharesBrowse {
			m.rebuildShareSectionsPreservingFocus()
		}
	}
}

// sweepStale transitions silent live channels to stale on the freshness schedule,
// persisting and refreshing the open browser only when something actually changed.
func (m *Model) sweepStale(now time.Time) {
	if m.sessions.markStale(now, stalenessThreshold) {
		m.saveSessions()
		if m.sharesBrowse {
			m.rebuildShareSectionsPreservingFocus()
		}
	}
}

// nextSeq hands out the next global publish position.
func (s *sessionStore) nextSeq() int64 { s.Seq++; return s.Seq }

// channel finds the channel for a session id, creating a live one if absent. The
// unattributed default channel (id "") holds pushes not yet tied to a session.
func (s *sessionStore) channel(id string) *sessionChannel {
	for i := range s.Channels {
		if s.Channels[i].SessionID == id {
			return &s.Channels[i]
		}
	}
	s.Channels = append(s.Channels, sessionChannel{SessionID: id, State: channelLive})
	return &s.Channels[len(s.Channels)-1]
}

// ingest routes a published share into its session channel. A "mentioned" batch
// replaces the channel's ambient face (latest-wins); a "view" push appends to the
// browsable ring (bounded to the last few). Freshness and state track the newest
// activity.
func (s *sessionStore) ingest(sh share) {
	ch := s.channel(sh.SessionID)
	ch.Freshness = sh.TS
	ch.State = channelLive
	switch sh.Kind {
	case shareMentioned:
		if sh.Name != "" {
			ch.Title = sh.Name
		}
		ch.AmbientBeads = ambientBeads{
			ID: sh.ID, Name: sh.Name, Seq: sh.seq, TS: sh.TS,
			IDs: sh.Spec.IDs, Excerpts: sh.Excerpts,
		}
	default: // shareViewKind
		ch.PushRing = append(ch.PushRing, pushEntry{ID: sh.ID, Name: sh.Name, Seq: sh.seq, TS: sh.TS, Spec: sh.Spec})
		if len(ch.PushRing) > maxPushRing {
			ch.PushRing = ch.PushRing[len(ch.PushRing)-maxPushRing:]
		}
	}
}

// archive marks a session's channel State=archived — the SessionEnd trigger.
// Find-only: a SessionEnd for a session that never registered a channel is a
// no-op (nothing to archive). Archived is terminal for the freshness sweep, but a
// later name-drop or push re-livens the channel (ingest resets State to live).
// Returns whether it changed anything.
func (s *sessionStore) archive(id string) bool {
	for i := range s.Channels {
		if s.Channels[i].SessionID != id {
			continue
		}
		if s.Channels[i].State == channelArchived {
			return false
		}
		s.Channels[i].State = channelArchived
		return true
	}
	return false
}

// markStale sweeps live channels whose newest activity (Freshness) predates
// now-threshold to State=stale, so a crashed or never-ended session decays to
// stale and never keeps reading live. Only live→stale transitions: archived is
// terminal, and a stale channel stays stale until a fresh ingest relives it.
// Returns whether any channel changed.
func (s *sessionStore) markStale(now time.Time, threshold time.Duration) bool {
	changed := false
	for i := range s.Channels {
		if s.Channels[i].State != channelLive {
			continue
		}
		if now.Sub(s.Channels[i].Freshness) >= threshold {
			s.Channels[i].State = channelStale
			changed = true
		}
	}
	return changed
}

// stateRank orders the lifecycle stages for the browser: live channels first,
// then stale, then archived.
func stateRank(s channelState) int {
	switch s {
	case channelStale:
		return 1
	case channelArchived:
		return 2
	default: // live (and any unset)
		return 0
	}
}

// latestSeq is a channel's newest publish position (across its ambient face and
// push ring) — a clock-independent freshness key for stable ordering.
func (ch sessionChannel) latestSeq() int64 {
	s := ch.AmbientBeads.Seq
	for _, p := range ch.PushRing {
		if p.Seq > s {
			s = p.Seq
		}
	}
	return s
}

// browseOrder returns the channels the live-sessions browser lists: one per
// channel that has anything to show (a push ring or a non-empty ambient face),
// ordered live → stale → archived, then by freshness (newest first), tie-broken
// by the newest publish position so equal timestamps stay stable.
func (s sessionStore) browseOrder() []sessionChannel {
	out := make([]sessionChannel, 0, len(s.Channels))
	for _, ch := range s.Channels {
		if len(ch.PushRing) == 0 && ch.AmbientBeads.empty() {
			continue
		}
		out = append(out, ch)
	}
	sort.SliceStable(out, func(i, j int) bool {
		ri, rj := stateRank(out[i].State), stateRank(out[j].State)
		if ri != rj {
			return ri < rj
		}
		if !out[i].Freshness.Equal(out[j].Freshness) {
			return out[i].Freshness.After(out[j].Freshness)
		}
		return out[i].latestSeq() > out[j].latestSeq()
	})
	return out
}

// flat projects the channels back into the append-ordered []share stream the
// existing @ browser and view() consume: one entry per push-ring push and one
// per non-empty ambient face, ordered by the global publish sequence.
func (s sessionStore) flat() []share {
	var out []share
	for _, ch := range s.Channels {
		for _, p := range ch.PushRing {
			out = append(out, share{
				ID: p.ID, Kind: shareViewKind, Name: p.Name, TS: p.TS,
				SessionID: ch.SessionID, Spec: p.Spec, seq: p.Seq,
			})
		}
		if !ch.AmbientBeads.empty() {
			a := ch.AmbientBeads
			out = append(out, share{
				ID: a.ID, Kind: shareMentioned, Name: a.Name, TS: a.TS,
				SessionID: ch.SessionID, Spec: shareSpec{IDs: a.IDs}, Excerpts: a.Excerpts, seq: a.Seq,
			})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].seq < out[j].seq })
	return out
}

// empty reports whether the store projects to nothing to show.
func (s sessionStore) empty() bool { return len(s.flat()) == 0 }

// sessionLabel names a session for a human-facing announcement — the attach
// badge and the arriving-share footer. It resolves the session id to the
// conversation name the hook registered on its channel (the label Alex gives a
// Claude Code session); when the session never named itself it falls back to a
// short session-id stub, and the unattributed default channel (a push that
// carried no session at all) reads as a generic live session.
func (m Model) sessionLabel(id string) string {
	for _, ch := range m.sessions.Channels {
		if ch.SessionID == id && ch.Title != "" {
			return ch.Title
		}
	}
	if id == "" {
		return "live session"
	}
	return "session " + shortSession(id)
}

// shortSession trims a session id to a compact stub for the unnamed fallback.
func shortSession(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}
