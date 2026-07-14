package ui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/awhitty/bb/internal/agentapi"
	"github.com/awhitty/bb/internal/bd"
	"github.com/awhitty/bb/internal/rollup"
)

// shares.go is the agent-shares surface: everything a connected agent shares
// with the human — the ambient name-drop batches (what it is talking about, from
// the Claude Code Stop hook, type "mentioned") AND the deliberately published
// views (what it chose to show, from the MCP show/set_view/emphasize tools, type
// "view"). The agent PUBLISHES; it never seizes the screen. The human browses on
// their own schedule with `@`, and ATTACHES to a channel (A/enter) so the agent
// drives the board live; the attach is sticky (the human can fiddle and stay
// attached) and only esc detaches. This makes "the human outranks the agent"
// structural.
//
// The durable store underneath is the session-channel store (sessions.go), keyed
// by Claude Code SessionID and persisted to sessions.json. The `share` type here
// is the flat, newest-first PROJECTION the browser and view() render — publishing
// routes an entry into its channel, and the browser reads the channels back out
// as a flat stream.

type shareKind string

const (
	shareMentioned shareKind = "mentioned" // a name-drop batch: filter to these ids
	shareViewKind  shareKind = "view"      // a published arrangement: apply the whole spec
)

// shareSpec is the applyable arrangement an entry carries. Mode is the VIEW
// (list|kanban|tree|relationship|columns, plus the legacy status|type|root|
// blockers board aliases); Traverse and Group are the algebra knobs; Collapse
// and MinSubtree round-trip the tree fold/threshold state so an @-pulled view
// restores exactly what the agent published (they used to be silently dropped).
type shareSpec struct {
	Mode       string                 `json:"mode,omitempty"`
	Traverse   string                 `json:"traverse,omitempty"` // hierarchy | deps
	Group      string                 `json:"group,omitempty"`    // none|status|type|ancestor|blockers|label|priority
	Lane       string                 `json:"lane,omitempty"`     // kanban break-out lane facet (2nd axis)
	Root       string                 `json:"root,omitempty"`
	Scope      string                 `json:"scope,omitempty"` // relationship-focus scope root
	Query      string                 `json:"query,omitempty"`
	IDs        []string               `json:"ids,omitempty"`
	SortKey    string                 `json:"sort_key,omitempty"`
	SortDir    string                 `json:"sort_dir,omitempty"`
	Collapse   *agentapi.CollapseSpec `json:"collapse,omitempty"`
	MinSubtree *int                   `json:"min_subtree,omitempty"`
	Emphasis   []agentapi.Emphasis    `json:"emphasis,omitempty"`
	// Remarks are the ephemeral, issue-keyed notes this view carries (bead id →
	// short text). They restore with the view when the human re-pulls the share and
	// clear when they switch away; they are NEVER written to Beads. omitempty keeps
	// an older share (no remarks) byte-identical on the wire and in sessions.json.
	Remarks map[string]string `json:"remarks,omitempty"`
}

type share struct {
	ID        string    `json:"id"`
	Kind      shareKind `json:"type"`
	Name      string    `json:"name"`
	TS        time.Time `json:"ts"`
	SessionID string    `json:"session_id,omitempty"`
	Spec      shareSpec `json:"spec"`
	// seq is the global publish position, carried through the channel projection so
	// the flat stream orders stably regardless of clock resolution. Ephemeral (the
	// channels persist, this projection does not).
	seq int64 `json:"-"`
	// Excerpts is the CONTEXT a mentioned batch carries: the prose the agent
	// named these beads in, each bead id marked as an inline chiclet span. Absent
	// on a "view" share and on a legacy ids-only mentioned batch (which then
	// renders as a plain id list, unchanged).
	Excerpts []agentapi.Excerpt `json:"excerpts,omitempty"`
}

// --- publishing ---

// publishShare routes one entry into its session channel (sessions.go), persists
// the channels, and bumps the unseen count. The channel store keeps the ambient
// face latest-wins and the push ring bounded; this returns the projected entry so
// callers can footer-notice it.
func (m *Model) publishShare(s share) share {
	if s.ID == "" {
		s.ID = fmt.Sprintf("s%d", time.Now().UnixNano())
	}
	if s.TS.IsZero() {
		s.TS = time.Now()
	}
	if s.Name == "" {
		s.Name = deriveShareName(s)
	}
	s.seq = m.sessions.nextSeq()
	m.sessions.ingest(s)
	m.saveSessions()
	if m.sharesBrowse {
		// The browser is open: the new section appears live and is therefore
		// seen — rebuild in place (cursor preserved by id), no unseen accrues.
		m.rebuildShareSectionsPreservingFocus()
	} else {
		m.shareNew++
	}
	return s
}

// deriveShareName makes a short human-readable label when the agent didn't name
// the view (or for a name-drop batch).
func deriveShareName(s share) string {
	if s.Kind == shareMentioned {
		return fmt.Sprintf("%d bead(s)", len(s.Spec.IDs))
	}
	sp := s.Spec
	switch {
	case sp.Query != "":
		return "query " + sp.Query
	case sp.Mode == "relationship" || sp.Mode == "columns":
		return sp.Mode + " " + shortRoot(sp.Root)
	case sp.Mode != "":
		return sp.Mode
	case len(sp.IDs) > 0:
		return fmt.Sprintf("%d bead(s)", len(sp.IDs))
	case len(sp.Emphasis) > 0:
		return fmt.Sprintf("emphasis (%d)", len(sp.Emphasis))
	default:
		return "view"
	}
}

func shortRoot(r string) string {
	if i := len(r); i > 0 {
		return r
	}
	return "focus"
}

// specFromShow / specFromSpec / specFromEmphasis translate an agent action into
// the share spec (so the same three tools that used to seize now publish).
func specFromShow(a agentapi.ShowAction) shareSpec {
	return shareSpec{Mode: a.Mode, Query: a.Query, IDs: a.IDs, Remarks: a.Remarks}
}

func specFromSpecAction(a agentapi.SpecAction) shareSpec {
	sp := shareSpec{
		Mode: a.Mode, Traverse: a.Traverse, Group: a.Group, Lane: a.Lane,
		Root: a.Root, Scope: a.Scope,
		IDs: a.IDs, SortKey: a.SortKey, SortDir: a.SortDir,
		Collapse: a.Collapse, MinSubtree: a.MinSubtree, Remarks: a.Remarks,
	}
	if a.Query != nil {
		sp.Query = *a.Query
	}
	return sp
}

// ingestNameDrop rings a name-drop batch into the SAME stream as published
// views (the Stop hook's ambient signal). Delivered on the UI loop via
// Program.Send. When the human is attached to this session's channel it applies
// live (filters to the ids); otherwise it accumulates quietly with a footer notice.
func (m *Model) ingestNameDrop(a agentapi.NameDropAction) {
	ts, err := time.Parse(time.RFC3339, a.TS)
	if err != nil {
		ts = time.Now()
	}
	// The section header names the CONVERSATION the beads came from; fall back to
	// the one-line snippet an older hook binary sends, then to the bead count.
	name := a.ConvoName
	if name == "" {
		name = a.Snippet
	}
	s := m.publishShare(share{
		Kind: shareMentioned, Name: name, TS: ts,
		SessionID: a.SessionID, Spec: shareSpec{IDs: a.IDs},
		Excerpts: a.Excerpts,
	})
	if m.attachedTo(a.SessionID) {
		m.applyShare(s)
	}
	m.sharesFooterNotice(s)
}

// --- accessors ---

// sharesNewestFirst projects the session channels into the flat stream with the
// freshest entry first (the order the human browses and the @-latest points at).
func (m Model) sharesNewestFirst() []share {
	flat := m.sessions.flat()
	out := make([]share, len(flat))
	for i, s := range flat {
		out[len(flat)-1-i] = s
	}
	return out
}

// --- applying a share to the board ---

// applyShare arranges the board from a stream entry (a channel's face, or a live
// push on the attached channel). The attach layer owns the human's snapshot, so
// this only drives the knobs; detach restores the prior view. A query entry
// reloads; the rest rebuild in place.
func (m *Model) applyShare(s share) tea.Cmd {
	m.attach.title = s.Name
	sp := s.Spec
	deps, hasTrav := traverseDeps(sp.Traverse)
	if sp.Mode != "" {
		switch sp.Mode {
		case "list":
			m.setView(ViewList)
		case "kanban":
			m.setView(ViewKanban)
		case "tree":
			m.setView(ViewTree)
		case "relationship":
			m.enterSwimRooted(sp.Root)
		case "columns":
			d := m.millerDeps
			if hasTrav {
				d = deps
			}
			m.enterColumns(sp.Root, d)
		case string(rollup.ModeStatus), string(rollup.ModeType), string(rollup.ModeRoot), string(rollup.ModeBlockers):
			if m.view != ViewList && m.view != ViewKanban {
				m.setView(ViewKanban)
			}
			m.mode = rollup.Mode(sp.Mode)
		}
		m.modeBy = "agent"
	}
	// Traverse (tree deps↔hierarchy) — columns already threaded it above.
	if hasTrav && sp.Mode != "columns" {
		m.setTraverse(deps)
	}
	// Group facet for the now-active view (tree segmentation / swim columns /
	// board grouping).
	if sp.Group != "" {
		if f, ok := facetFromName(sp.Group); ok {
			m.applyGroup(f)
		}
	}
	// The kanban break-out lane (2nd board axis); "none" clears it to the flat board.
	if sp.Lane != "" {
		if f, ok := facetFromName(sp.Lane); ok {
			m.applyLane(f)
		}
	}
	// Relationship-focus scope — narrow every mode to this bead's neighborhood when
	// the root is on the loaded board (else leave the board unscoped).
	if sp.Scope != "" {
		if _, ok := m.byID[sp.Scope]; ok {
			m.enterScope(sp.Scope)
		}
	}
	// Tree fold + threshold state — these used to be dropped on a pulled view.
	if cs := sp.Collapse; cs != nil {
		if m.collapsed == nil {
			m.collapsed = map[string]bool{}
		}
		switch {
		case cs.ExpandAll:
			m.collapsed = map[string]bool{}
		case cs.Level != nil && *cs.Level == 0:
			m.collapseToRoots()
		}
		for _, id := range cs.NodeIDs {
			m.collapsed[id] = true
		}
	}
	if sp.MinSubtree != nil {
		m.minSubtree = *sp.MinSubtree
	}
	if sp.SortKey != "" {
		desc := rollup.DefaultDesc(rollup.SortKey(sp.SortKey))
		switch sp.SortDir {
		case "asc":
			desc = false
		case "desc":
			desc = true
		}
		srt := rollup.Sort{Key: rollup.SortKey(sp.SortKey), Desc: desc}
		if isFlatKey(sp.SortKey) {
			m.flatSort = srt
		} else {
			m.treeSort = srt
		}
	}
	m.setEmphasis(sp.Emphasis) // registers the esc layer when non-empty
	m.setRemarks(sp.Remarks)   // ephemeral issue-keyed notes; cleared on detach/switch
	if len(sp.IDs) > 0 {
		m.attach.ids = sp.IDs
		m.filterBy = "agent"
	}
	var cmd tea.Cmd
	if sp.Query != "" {
		m.activeQuery = sp.Query
		m.attach.query = sp.Query
		m.filterBy = "agent"
		cmd = m.loadCmd()
	} else {
		m.rebuild()
	}
	m.setDetailContent()
	return cmd
}

// sharesFooterNotice is the quiet, non-intrusive line shown when a share lands
// while the human is NOT following (they pull on their own schedule). It names
// the originating SESSION (the conversation name Alex gave it) so the human
// knows whose activity arrived; a name-drop reads as touched beads (its "title"
// is only the conversation name), a view push names the view it shared.
func (m *Model) sharesFooterNotice(s share) {
	who := m.sessionLabel(s.SessionID)
	var what string
	if s.Kind == shareMentioned {
		what = fmt.Sprintf("touched %d bead(s)", len(s.Spec.IDs))
	} else {
		what = fmt.Sprintf("shared “%s”", s.Name)
	}
	if m.sharesBrowse {
		// The human is already browsing; the new section is live in the list.
		m.setMessage(fmt.Sprintf("%s %s — added to the list", who, what), false)
		return
	}
	m.setMessage(fmt.Sprintf("%s %s (%d new) — @ to browse", who, what, m.shareNew), false)
}

// --- the @ surface: the live-sessions master-detail browser ---
//
// `@` opens a sectioned browser, one section per session channel (ordered
// live → stale → archived, then by freshness): the header names the channel (its
// conversation title + lifecycle state/freshness) and its FACE is listed inline
// underneath — a channel that pushed a deliberate view previews that view's
// beads, an un-pushed channel shows its own recently-touched beads. j/k move
// focus across ALL beads through every section and the preview panel (forced open
// on the right) follows live; A/enter ATTACHES to the focused channel so its agent
// drives the main board live (sticky — only esc detaches); esc/[ back out.

// shareSection is one session channel's row in the live-sessions browser: the
// channel it renders, the channel's FACE (sh — the entry the section previews
// and A applies: a pushed channel's latest view, or an un-pushed channel's
// ambient recently-touched batch), the beads that face resolves to against the
// FULL board (m.graph), and an optional one-line note. ambient marks the
// recently-touched face (no deliberate push). focus is the ordered list of
// focusable things — one per resolved bead row, or one per excerpt CHICLET (in
// reading order) when the ambient face carries excerpts.
type shareSection struct {
	ch      sessionChannel
	sh      share
	ambient bool
	beads   []bd.Issue
	note    string
	focus   []shareFocusTarget
}

// channelFace picks the entry a channel's section previews: a channel with a
// deliberate push shows its LATEST push (a view share); a channel with none
// shows its ambient recently-touched batch (a mentioned share). The bool is true
// for the ambient face.
func channelFace(ch sessionChannel) (share, bool) {
	if n := len(ch.PushRing); n > 0 {
		p := ch.PushRing[n-1]
		return share{
			ID: p.ID, Kind: shareViewKind, Name: p.Name, TS: p.TS,
			SessionID: ch.SessionID, Spec: p.Spec, seq: p.Seq,
		}, false
	}
	a := ch.AmbientBeads
	return share{
		ID: a.ID, Kind: shareMentioned, Name: a.Name, TS: a.TS,
		SessionID: ch.SessionID, Spec: shareSpec{IDs: a.IDs}, Excerpts: a.Excerpts, seq: a.Seq,
	}, true
}

// shareFocusTarget is one navigable stop in a section: the bead the preview
// follows, and (for an excerpt chiclet) which excerpt + mention it is, so the
// renderer can highlight the focused pill in place. exc == -1 marks a plain
// bead row.
type shareFocusTarget struct {
	beadID string
	exc    int // excerpt index, or -1 for a plain bead row
	men    int // mention index within that excerpt (or the bead index for -1)
}

// hasExcerpts reports whether a section renders as excerpt prose with inline
// chiclets (a mentioned batch carrying excerpts) rather than a plain bead list.
func (s shareSection) hasExcerpts() bool {
	return s.sh.Kind == shareMentioned && len(s.sh.Excerpts) > 0
}

// buildFocus builds a section's ordered focus stops from its excerpts (one per
// chiclet) or its resolved beads (one per row).
func buildFocus(s share, beads []bd.Issue) []shareFocusTarget {
	if s.Kind == shareMentioned && len(s.Excerpts) > 0 {
		var f []shareFocusTarget
		for ei := range s.Excerpts {
			for mi := range s.Excerpts[ei].Mentions {
				f = append(f, shareFocusTarget{beadID: s.Excerpts[ei].Mentions[mi].ID, exc: ei, men: mi})
			}
		}
		return f
	}
	f := make([]shareFocusTarget, len(beads))
	for i := range beads {
		f[i] = shareFocusTarget{beadID: beads[i].ID, exc: -1, men: i}
	}
	return f
}

// openSharesBrowser is `@`: build the sections from the stream and open the
// master-detail browser with the preview panel on the right.
func (m *Model) openSharesBrowser() {
	if m.sessions.empty() {
		m.setMessage("no agent shares yet — an agent publishes with show/set_view, or `bb hook install` for name-drops", false)
		return
	}
	m.shareNew = 0
	m.sharesBrowse = true
	m.sharePrevPanel = m.panelOpen
	m.panelOpen = true // the preview follows the focused bead while browsing
	m.panelFocus = false
	m.buildShareSections()
	m.shareBrowseFirst()
	m.setMessage(fmt.Sprintf("live sessions — %d channel(s), live first · j/k move · A/enter attach · esc back", len(m.shareSections)), false)
}

// closeSharesBrowser leaves the browser and restores the pre-browser panel state.
func (m *Model) closeSharesBrowser() {
	m.sharesBrowse = false
	m.panelOpen = m.sharePrevPanel
}

// buildShareSections builds one section per live session channel — ordered
// live → stale → archived, then by freshness — and resolves each channel's face
// (its latest pushed view, or its ambient recently-touched batch) to beads
// against the full board. Called on open and on `r` (rebuild against the latest
// board / channel set).
func (m *Model) buildShareSections() {
	chans := m.sessions.browseOrder()
	secs := make([]shareSection, 0, len(chans))
	for _, ch := range chans {
		sh, ambient := channelFace(ch)
		beads, note := m.resolveShareBeads(sh)
		if ambient {
			// The un-pushed channel's default face: the beads this session has
			// recently been talking about (its own name-drops).
			note = "recently touched"
		}
		secs = append(secs, shareSection{ch: ch, sh: sh, ambient: ambient, beads: beads, note: note, focus: buildFocus(sh, beads)})
	}
	m.shareSections = secs
	m.clampShareFocus()
}

// rebuildShareSectionsPreservingFocus rebuilds the open browser's sections
// against the current stream and board, keeping the cursor on the same bead
// (by section identity + bead id) so a live publish or a board reload never
// yanks focus. Falls back to the nearest bead in the same section, else the
// browser's first bead.
func (m *Model) rebuildShareSectionsPreservingFocus() {
	shareID, beadID := m.focusedShareIdentity()
	m.buildShareSections()
	m.restoreShareFocus(shareID, beadID)
}

// focusedShareIdentity returns the focused section's share id and focused bead
// id (either may be empty when nothing is focused yet).
func (m Model) focusedShareIdentity() (shareID, beadID string) {
	if m.shareSecIdx < len(m.shareSections) {
		shareID = m.shareSections[m.shareSecIdx].sh.ID
		foc := m.shareSections[m.shareSecIdx].focus
		if m.shareBeadIdx < len(foc) {
			beadID = foc[m.shareBeadIdx].beadID
		}
	}
	return
}

// restoreShareFocus lands the cursor back on the same section (by share id) and
// bead (by id) after a rebuild. If the section is gone, it clamps; if only the
// bead is gone, it keeps the nearest index within that section.
func (m *Model) restoreShareFocus(shareID, beadID string) {
	if shareID == "" {
		m.clampShareFocus()
		return
	}
	secIdx := -1
	for si := range m.shareSections {
		if m.shareSections[si].sh.ID == shareID {
			secIdx = si
			break
		}
	}
	if secIdx < 0 {
		m.clampShareFocus()
		return
	}
	m.shareSecIdx = secIdx
	foc := m.shareSections[secIdx].focus
	for bi := range foc {
		if foc[bi].beadID == beadID {
			m.shareBeadIdx = bi
			return
		}
	}
	// The focused bead vanished: keep the nearest position in the same section.
	m.shareBeadIdx = clamp(m.shareBeadIdx, 0, max(0, len(foc)-1))
}

// resolveShareBeads maps one share's spec to the beads to list under its header.
// ids and subtree= resolve in-process against the full board (the same
// resolution the applied-share path uses); a general filter runs once through
// bd; a mode-specific view centers on its root + immediate relations.
func (m *Model) resolveShareBeads(s share) ([]bd.Issue, string) {
	sp := s.Spec
	switch {
	case len(sp.IDs) > 0:
		// mentioned batches and show(ids)/set_view(ids): the named beads.
		return m.beadsByIDs(sp.IDs), ""
	case sp.Query != "":
		if root, ok := subtreeRoot(sp.Query); ok {
			return subtreeIssues(m.graphOrIssues(), root), ""
		}
		beads, err := m.client.Query(sp.Query)
		if err != nil {
			return nil, "query error: " + err.Error()
		}
		return beads, ""
	case sp.Mode == "relationship" || sp.Mode == "columns":
		if sp.Root == "" {
			return nil, sp.Mode + " view"
		}
		return m.centerBeads(sp.Root), sp.Mode + " · rooted at " + m.shortID(sp.Root)
	case sp.Mode != "":
		return nil, sp.Mode + " view (whole board)"
	case len(sp.Emphasis) > 0:
		return m.emphasisBeads(sp.Emphasis), fmt.Sprintf("emphasis · %d target(s)", len(sp.Emphasis))
	}
	return nil, ""
}

// graphOrIssues is the full board when loaded, else the display set (a defensive
// fallback before the first load).
func (m Model) graphOrIssues() []bd.Issue {
	if m.graph != nil {
		return m.graph
	}
	return m.issues
}

// beadsByIDs resolves an id list against the full board (via byID), preserving
// the given order — a mentioned batch's order is meaningful.
func (m Model) beadsByIDs(ids []string) []bd.Issue {
	out := make([]bd.Issue, 0, len(ids))
	for _, id := range ids {
		if is, ok := m.byID[id]; ok {
			out = append(out, is)
		}
	}
	return out
}

// centerBeads is a mode-specific view's inline set: the root plus its immediate
// related beads (parent / children / blockers / dependents), from the in-memory
// indexes — the beads that view centers on.
func (m Model) centerBeads(root string) []bd.Issue {
	is, ok := m.byID[root]
	if !ok {
		return nil
	}
	out := []bd.Issue{is}
	for _, l := range m.relatedLinks(is) {
		if r, ok := m.byID[l.id]; ok {
			out = append(out, r)
		}
	}
	return out
}

// emphasisBeads collects the issue-kind emphasis targets (Ref = a bead id).
func (m Model) emphasisBeads(targets []agentapi.Emphasis) []bd.Issue {
	var out []bd.Issue
	for _, e := range targets {
		if is, ok := m.byID[e.Ref]; ok {
			out = append(out, is)
		}
	}
	return out
}

// --- browser focus + navigation (section, bead-in-section) ---

func (m *Model) clampShareFocus() {
	if len(m.shareSections) == 0 {
		m.shareSecIdx, m.shareBeadIdx = 0, 0
		return
	}
	m.shareSecIdx = clamp(m.shareSecIdx, 0, len(m.shareSections)-1)
	m.shareBeadIdx = clamp(m.shareBeadIdx, 0, max(0, len(m.shareSections[m.shareSecIdx].focus)-1))
}

// shareBrowseFirst lands focus on the first stop of the first non-empty section.
func (m *Model) shareBrowseFirst() {
	for si := range m.shareSections {
		if len(m.shareSections[si].focus) > 0 {
			m.shareSecIdx, m.shareBeadIdx = si, 0
			return
		}
	}
	m.shareSecIdx, m.shareBeadIdx = 0, 0
}

func (m *Model) shareBrowseLast() {
	for si := len(m.shareSections) - 1; si >= 0; si-- {
		if n := len(m.shareSections[si].focus); n > 0 {
			m.shareSecIdx, m.shareBeadIdx = si, n-1
			return
		}
	}
}

// shareBrowseDown / shareBrowseUp flow focus across ALL stops through every
// section (bead rows AND excerpt chiclets, in reading order), skipping sections
// that matched zero.
func (m *Model) shareBrowseDown() {
	secs := m.shareSections
	if len(secs) == 0 {
		return
	}
	if m.shareBeadIdx < len(secs[m.shareSecIdx].focus)-1 {
		m.shareBeadIdx++
		return
	}
	for si := m.shareSecIdx + 1; si < len(secs); si++ {
		if len(secs[si].focus) > 0 {
			m.shareSecIdx, m.shareBeadIdx = si, 0
			return
		}
	}
}

func (m *Model) shareBrowseUp() {
	secs := m.shareSections
	if len(secs) == 0 {
		return
	}
	if m.shareBeadIdx > 0 {
		m.shareBeadIdx--
		return
	}
	for si := m.shareSecIdx - 1; si >= 0; si-- {
		if n := len(secs[si].focus); n > 0 {
			m.shareSecIdx, m.shareBeadIdx = si, n-1
			return
		}
	}
}

// shareBrowseFocused is the bead the preview panel follows (nil when every
// section matched zero, or the focused chiclet's bead is off the board).
func (m Model) shareBrowseFocused() *bd.Issue {
	if m.shareSecIdx < len(m.shareSections) {
		foc := m.shareSections[m.shareSecIdx].focus
		if m.shareBeadIdx < len(foc) {
			if is, ok := m.byID[foc[m.shareBeadIdx].beadID]; ok {
				return &is
			}
		}
	}
	return nil
}

// attachToFocusedSection (A/enter) ATTACHES to the focused section's channel: it
// snapshots the human's view, applies the channel's current face, and raises the
// attach layer so the channel's live pushes keep applying until esc detaches (the
// attach is sticky — local moves keep it). Switching to a different section swaps
// the attach cleanly. Closes the browser.
func (m *Model) attachToFocusedSection() tea.Cmd {
	if m.shareSecIdx >= len(m.shareSections) {
		return nil
	}
	sec := m.shareSections[m.shareSecIdx]
	m.closeSharesBrowser()
	cmd := m.attachTo(sec.ch.SessionID, sec.sh)
	m.setMessage(fmt.Sprintf("attached to “%s” — the agent drives · esc detaches", shareHeaderTitle(sec)), false)
	return cmd
}

// --- browser rows + rendering ---

type shareBrowseRowKind int

const (
	sbHeader  shareBrowseRowKind = iota
	sbNote                       // a one-line note under a header (mode-specific views)
	sbBead                       // a resolved bead row
	sbExcerpt                    // one wrapped line of a mentioned batch's excerpt prose
	sbEmpty                      // "(no matches)" under a header
	sbSpacer
)

type shareBrowseRow struct {
	kind    shareBrowseRowKind
	sec     int
	beadIdx int
	issue   *bd.Issue
	section *shareSection
	text    string // pre-rendered content for sbExcerpt (chiclets + focus baked in)
	focused bool   // sbExcerpt: this line holds the focused chiclet
}

// shareBrowseRows flattens the sections into header / note / bead / excerpt /
// empty rows for the one page-jump window (the same shape as the sectioned
// list). Excerpt lines are pre-rendered at width w with their chiclets and the
// focused-pill highlight already applied.
func (m Model) shareBrowseRows(w int) []shareBrowseRow {
	var rows []shareBrowseRow
	for si := range m.shareSections {
		sec := &m.shareSections[si]
		rows = append(rows, shareBrowseRow{kind: sbHeader, sec: si, section: sec})
		if sec.note != "" {
			rows = append(rows, shareBrowseRow{kind: sbNote, sec: si, section: sec})
		}
		switch {
		case sec.hasExcerpts():
			lines, focusLine := m.renderMentionedBody(sec, w, si == m.shareSecIdx)
			for li, ln := range lines {
				rows = append(rows, shareBrowseRow{kind: sbExcerpt, sec: si, text: ln, focused: li == focusLine})
			}
		case len(sec.beads) == 0:
			rows = append(rows, shareBrowseRow{kind: sbEmpty, sec: si, section: sec})
		default:
			for bi := range sec.beads {
				is := sec.beads[bi]
				rows = append(rows, shareBrowseRow{kind: sbBead, sec: si, beadIdx: bi, issue: &is})
			}
		}
		rows = append(rows, shareBrowseRow{kind: sbSpacer})
	}
	if n := len(rows); n > 0 && rows[n-1].kind == sbSpacer {
		rows = rows[:n-1]
	}
	return rows
}

// shareBrowseFocusIndex is the flat position of the focused stop in the rows:
// the focused bead row, or (for a mentioned batch) the excerpt line carrying the
// focused chiclet.
func (m Model) shareBrowseFocusIndex(rows []shareBrowseRow) int {
	for i, r := range rows {
		if r.kind == sbBead && r.sec == m.shareSecIdx && r.beadIdx == m.shareBeadIdx {
			return i
		}
		if r.kind == sbExcerpt && r.sec == m.shareSecIdx && r.focused {
			return i
		}
	}
	return 0
}

// viewSharesBrowse renders the sectioned browser into the left pane at bodyH
// lines, one page-jump window over all rows (the preview panel is drawn beside
// it by the root View composition).
func (m Model) viewSharesBrowse(bodyH int) string {
	w := m.boardWidth()
	if len(m.shareSections) == 0 {
		return lipgloss.Place(w, bodyH, lipgloss.Center, lipgloss.Center,
			styDim.Render("no agent shares yet"))
	}
	rows := m.shareBrowseRows(w)
	now := time.Now()
	return m.renderNavList(bodyH, len(rows), m.shareBrowseFocusIndex(rows), "\x00shares",
		func(i int, focused bool) string {
			return m.renderShareBrowseRow(rows[i], focused, w, now)
		}, moreTop, moreBottom)
}

func (m Model) renderShareBrowseRow(r shareBrowseRow, focused bool, w int, now time.Time) string {
	switch r.kind {
	case sbHeader:
		return renderShareSectionHeader(*r.section, w, now)
	case sbNote:
		return styDim.Render(ansi.Truncate("  "+r.section.note, w, "…"))
	case sbBead:
		return formatRow(*r.issue, focused, w, true, m.relFlagsFor(*r.issue), m.issueEmph(r.issue.ID), false, m.ageCellFn(now))
	case sbExcerpt:
		return pad(r.text, w)
	case sbEmpty:
		return styDim.Render(ansi.Truncate("  (no matches)", w, "…"))
	default:
		return ""
	}
}

// --- mentioned excerpts: inline chiclets within wrapped prose ---

// excerptIndent is the left margin the excerpt body reads at (aligns under the
// section header's name).
const excerptIndent = 2

// exToken is one unit of an excerpt line: a plain word or an inline bead
// chiclet. width is its display width; a chiclet never breaks across a line.
type exToken struct {
	text    string // rendered (styled) text
	width   int    // display width
	chiclet bool
}

// renderMentionedBody renders a mentioned section's excerpts as wrapped lines
// with each bead reference rendered as an inline chiclet, tinted by the bead's
// priority. When the section is focused, the chiclet at the section's focused
// stop (m.shareBeadIdx, a flat index across all excerpts' mentions in reading
// order) is highlighted. Returns the lines plus the index of the line carrying
// that focused chiclet (-1 if none).
func (m Model) renderMentionedBody(sec *shareSection, w int, focusedSection bool) (lines []string, focusLine int) {
	focusLine = -1
	avail := max(8, w-excerptIndent)
	g := 0 // flat mention index across all excerpts (matches focus ordering)
	for ei := range sec.sh.Excerpts {
		ex := sec.sh.Excerpts[ei]
		var toks []exToken
		markFocused := -1 // token index of the focused chiclet in this excerpt
		pos := 0
		for _, mn := range ex.Mentions {
			s, e := mn.Start, mn.End
			if s < pos || s > len(ex.Text) || e > len(ex.Text) || s >= e {
				g++ // keep the flat index aligned with the focus list even if the span is bad
				continue
			}
			toks = appendWords(toks, ex.Text[pos:s])
			p := 2
			if is, ok := m.byID[mn.ID]; ok {
				p = is.Priority
			}
			isFoc := focusedSection && g == m.shareBeadIdx
			short := m.shortID(mn.ID)
			if isFoc {
				markFocused = len(toks)
			}
			toks = append(toks, exToken{text: chicletStyle(p, isFoc).Render(short), width: len([]rune(short)) + 2, chiclet: true})
			pos = e
			g++
		}
		toks = appendWords(toks, ex.Text[pos:])

		exLines, focIdx := wrapTokens(toks, avail, markFocused)
		for _, ln := range exLines {
			lines = append(lines, strings.Repeat(" ", excerptIndent)+ln)
		}
		if focIdx >= 0 {
			focusLine = len(lines) - len(exLines) + focIdx
		}
	}
	return lines, focusLine
}

// appendWords splits plain prose into word tokens (styled as body text) and
// appends them.
func appendWords(toks []exToken, s string) []exToken {
	for _, wd := range strings.Fields(s) {
		toks = append(toks, exToken{text: styExcerpt.Render(wd), width: ansi.StringWidth(wd)})
	}
	return toks
}

// wrapTokens greedily packs tokens into lines of at most width w, single space
// between tokens, never splitting a token. markTok is the token index of the
// focused chiclet; the returned focusLine is the line it lands on (-1 if none).
func wrapTokens(toks []exToken, w, markTok int) (lines []string, focusLine int) {
	focusLine = -1
	var cur strings.Builder
	curW := 0
	li := 0
	flush := func() {
		lines = append(lines, cur.String())
		cur.Reset()
		curW = 0
		li++
	}
	for i, t := range toks {
		add := t.width
		if curW > 0 {
			add++ // the separating space
		}
		if curW > 0 && curW+add > w {
			flush()
			add = t.width
		}
		if curW > 0 {
			cur.WriteByte(' ')
		}
		cur.WriteString(t.text)
		curW += add
		if i == markTok {
			focusLine = li
		}
	}
	if curW > 0 || len(lines) == 0 {
		lines = append(lines, cur.String())
	}
	return lines, focusLine
}

// renderShareSectionHeader draws a channel's section head: the channel Title
// (bold, left) and its lifecycle state + freshness (right). A live channel reads
// "live · 2m", a decayed one "stale · 40m", an ended one "ended".
func renderShareSectionHeader(sec shareSection, w int, now time.Time) string {
	ch := sec.ch
	age := relAge(ch.Freshness.Format(time.RFC3339), now)
	var meta string
	switch ch.State {
	case channelArchived:
		meta = styDim.Render("ended")
	case channelStale:
		meta = styDim.Render("stale")
		if age != "" {
			meta += styDim.Render(" · " + age)
		}
	default: // live
		meta = styDetailTitle.Render("live")
		if age != "" {
			meta += styDim.Render(" · " + age)
		}
	}
	name := stySection.Render(ansi.Truncate(shareHeaderTitle(sec), max(1, w-lipgloss.Width(meta)-2), "…"))
	gap := w - lipgloss.Width(name) - lipgloss.Width(meta)
	if gap < 1 {
		return ansi.Truncate(name+" "+meta, w, "")
	}
	return name + strings.Repeat(" ", gap) + meta
}

// shareHeaderTitle names a channel's section: the conversation Title when the
// hook sent one, else the face's own name, else a short session label (or the
// unattributed-default fallback for pushes that carried no session).
func shareHeaderTitle(sec shareSection) string {
	if sec.ch.Title != "" {
		return sec.ch.Title
	}
	if sec.sh.Name != "" {
		return sec.sh.Name
	}
	if sec.ch.SessionID == "" {
		return "unattributed views"
	}
	id := sec.ch.SessionID
	if len(id) > 12 {
		id = id[:12]
	}
	return "session " + id
}

// handleSharesBrowseKey routes every keypress while the master-detail browser is
// open — it is a self-contained mode, so it swallows input rather than letting
// board keys leak through underneath.
func (m Model) handleSharesBrowseKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "j", "down", "tab", "right":
		// tab/→ cycle forward through the focus stops (chiclets or bead rows) in
		// reading order; j/↓ do the same.
		m.shareBrowseDown()
	case "k", "up", "shift+tab", "left":
		m.shareBrowseUp()
	case " ":
		// space (re)opens the preview on the focused bead — it already follows
		// focus live, so this is the explicit affordance.
		m.panelOpen = true
	case "g":
		m.shareBrowseFirst()
	case "G":
		m.shareBrowseLast()
	case "enter", "A":
		// A and enter both ATTACH to the focused channel — the browser's purpose is
		// to hand the board to a live agent; the preview panel already shows beads.
		return m, m.attachToFocusedSection()
	case "r", "R":
		m.rebuildShareSectionsPreservingFocus()
		m.setMessage("shares refreshed against the current board", false)
	case "@", "esc", "[":
		m.closeSharesBrowser()
		m.setMessage("", false)
		m.navigatingHistory = true
	case "q", "ctrl+c":
		return m, tea.Quit
	}
	return m, nil
}
