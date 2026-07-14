// Package ui is the bubbletea front end: a hand-rolled kanban board plus
// bubbles components (textinput prompts, viewport detail, spinner, help) and
// glamour markdown in the detail view.
package ui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/log"
	"github.com/charmbracelet/x/ansi"

	"github.com/awhitty/bb/internal/agentapi"
	"github.com/awhitty/bb/internal/bd"
	"github.com/awhitty/bb/internal/discover"
	"github.com/awhitty/bb/internal/dolt"
	"github.com/awhitty/bb/internal/nlq"
	"github.com/awhitty/bb/internal/rollup"
)

// maxRerolls caps `r` on the NL review screen; past it, edit is the tool.
const maxRerolls = 3

// slowCompile is the budget past which the footer suggests a smaller model.
const slowCompile = 8 * time.Second

// Priority bounds bd recognizes: 0 highest … 4 lowest.
const (
	priMin = 0
	priMax = 4
)

type promptKind int

const (
	promptNone  promptKind = iota
	promptQuery            // the smart `/` prompt: a raw bd filter OR a natural-language ask
	promptNLEdit
	promptNLReview
	promptAnalyst
	promptJump    // jump to an issue by id
	promptComment // add a comment to the open detail's bead
)

// --- messages ---

type issuesMsg struct {
	seq int
	// query is the bd query this load ran ("" for a plain list). It lets the
	// handler label a bd rejection as a "query error" and revert the filter to
	// the last one that loaded, so a rejected query never wedges the board.
	query  string
	issues []bd.Issue
	// graph is the FULL board (all issues, incl. closed) used ONLY to resolve
	// relationships — parent/children/blockers/dependents. It is decoupled from
	// the displayed set so a filter (e.g. parent=X, which never loads X itself)
	// can't blank the relationship view: the neighborhood is always the true
	// graph, while the columns/tree stay filtered. Equals issues when the load
	// was already the whole board.
	graph []bd.Issue
	// statusSince is id → last status-transition time, parsed from the
	// interactions journal in the same async load so the age column's
	// time-in-status mode reads it without a per-frame file read. See age.go.
	statusSince map[string]time.Time
	err         error
}

type detailMsg struct {
	issue    bd.Issue
	history  []bd.HistoryEntry
	comments []map[string]any
	err      error
}

type prioritySyncedMsg struct {
	id  string
	err error
}

type commentAddedMsg struct {
	id       string
	comments []map[string]any
	err      error
}

// BoardChangedMsg is delivered (via Program.Send) by the store watcher and
// the slow-poll fallback when the beads store changes on disk — an agent (or
// any bd command) mutating the tracker from another terminal. The board
// reloads while preserving the user's context.
type BoardChangedMsg struct{}

// staleSweepMsg drives the session-channel freshness sweep: on each tick, silent
// live channels that have not refreshed within stalenessThreshold decay to stale,
// so a crashed or never-ended session never keeps reading live.
type staleSweepMsg struct{}

// staleSweepInterval is how often the freshness sweep runs.
const staleSweepInterval = time.Minute

// staleSweepCmd arms the next freshness sweep tick.
func staleSweepCmd() tea.Cmd {
	return tea.Tick(staleSweepInterval, func(time.Time) tea.Msg { return staleSweepMsg{} })
}

type nlCompiledMsg struct {
	seq int
	res nlq.Result
	err error
}

type resolvedMsg struct {
	r discover.Resolved
}

func resolveCmd(logger *log.Logger) tea.Cmd {
	return func() tea.Msg {
		return resolvedMsg{r: discover.Resolve(logger)}
	}
}

type resolveRetryMsg struct{}

type autostartDoneMsg struct {
	err     error
	elapsed time.Duration
}

func autostartCmd(logger *log.Logger) tea.Cmd {
	return func() tea.Msg {
		t0 := time.Now()
		err := discover.Autostart(logger)
		return autostartDoneMsg{err: err, elapsed: time.Since(t0)}
	}
}

type analystEventMsg struct {
	ch <-chan nlq.StreamEvent
	ev nlq.StreamEvent
}

func listenAnalyst(ch <-chan nlq.StreamEvent) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			ev = nlq.StreamEvent{Done: true}
		}
		return analystEventMsg{ch: ch, ev: ev}
	}
}

// viewKind is the single active nav-view discriminant. The five values are
// mutually exclusive by construction (one field, so the old flag-priority order
// is gone). ViewList is the zero value; New boots to ViewKanban. The activity
// feed and the shares browser are NOT members here — they compose OVER a nav
// view as overlays (activityView / sharesBrowse). setView is the only mutator.
type viewKind int

const (
	ViewList    viewKind = iota // full-width single-column sectioned list
	ViewKanban                  // side-by-side multi-column board
	ViewTree                    // nested hierarchy/dep outline
	ViewSwim                    // relationship swimlane board
	ViewColumns                 // Miller-columns relationship navigator
)

// viewState is the per-view display cluster: which nav view is active, every
// view's cursor/root/drill, the grouping/sort/collapse knobs, the active
// filter, and the open panel/detail. It is embedded in Model, so every field
// stays reachable as m.<field>, and its value is what the two snapshot/restore
// pairs copy: snapshotNav and snapshotAttach each capture the whole struct, while
// restoreNav and restoreAttach each write back only their own subset (nav.go /
// agent.go). Grouping the fields here kills the two field-by-field enumerations
// those pairs used to carry.
type viewState struct {
	// view is the active nav view (see viewKind) — the ONLY selector of the body
	// renderer. b toggles ViewList/ViewKanban; 5 opens the tree; R/C open the
	// swimlane/columns. Mutated only through setView.
	view viewKind

	// mode is the board/list grouping facet (status/type/root/blockers); colIdx/
	// rowIdx are the board cursor, treeIdx the tree cursor.
	mode    rollup.Mode
	colIdx  int
	rowIdx  int
	treeIdx int

	// depsTree flips the tree between hierarchy and dependency edges.
	depsTree bool

	// treeDir inverts the tree's nesting DIRECTION, orthogonal to the relation
	// (depsTree). Forward (false) is the natural nesting — parents over children,
	// issues under what blocks them; invert (true) reverses each edge — ancestors
	// nest under descendants, issues under what THEY block (rollup's *Reverse
	// builders). Only the tree reads it (buildTreeRows selects the builder from
	// depsTree×treeDir); the columns navigator shares only the relation axis
	// (millerDeps), not direction. A value field, so viewState.clone() carries it.
	treeDir bool

	// Relationship swimlane board (key R): a 2D overview rooted at swimRoot —
	// COLUMNS are a facet (swimFacet; status by default), ROWS (lanes) are the
	// relationship kind. swimCol is the focused column (index into the SHOWN
	// columns); swimPos is the focused card within that column's lane-major card
	// list. swimFacet's zero value "" means status — the byte-identical everyday
	// board; a non-status facet is reached through the grouper control (M) and
	// set_view. See swim.go.
	swimRoot  string
	swimCol   int
	swimPos   int
	swimFacet rollup.Facet

	// Miller-columns relationship navigator (key C): a Finder-style horizontal
	// stack. millerPath is the drilled-into bead ids (col 0's selection is
	// millerPath[0], and so on down the path); millerSel is the selected row in
	// the focused (rightmost) column; millerDeps toggles the appended relation
	// between hierarchy children and the dependency neighborhood. See columns.go.
	millerPath []string
	millerSel  int
	millerDeps bool

	// Sort (S cycles the key for the current view): flatSort orders issues within
	// a section (and the flat list/board); treeSort orders tree siblings and
	// ancestor sections by whole-subtree metrics. Defaults set in New.
	flatSort rollup.Sort
	treeSort rollup.Sort

	// boardFacet overrides the board/list grouping axis with any rollup.Facet. Its
	// zero value "" means "derive from mode" (rollup.FacetForMode) — the everyday
	// board, byte-unchanged. A legacy mode key (1-4/m) clears it via setMode; the
	// grouper control (M) / set_view set it, which is the ONLY way to group the
	// board by label or priority (facets with no legacy Mode).
	boardFacet rollup.Facet

	// treeFacet segments the tree: when set (non-empty), each level's children are
	// grouped into facet sections and the two-level sort runs recursively at every
	// depth (see rollup.SegmentChildren). The zero value "" means no segmentation —
	// the everyday tree, byte-unchanged. Reached through the grouper control (M)
	// and set_view.
	treeFacet rollup.Facet

	// boardLane is the kanban break-out lane axis: a SECOND facet, distinct from the
	// grouper (boardFacet/mode), that splits each board column into stacked lane
	// bands — turning the 1D board into a columns×lanes cross-tab. Its zero value ""
	// means no lanes (the everyday 1D board, byte-unchanged); a non-empty facet is
	// set only through the lane control (L). Unlike the swim board's fixed four
	// relationship lanes, this lane axis is any rollup.Facet, chosen on the kanban
	// itself. It is a value field, so viewState.clone() carries it with the struct
	// copy (no reference-typed deep copy needed). See board.go for the band split.
	boardLane rollup.Facet

	// scopeRoot generalizes the swim/columns relationship focus into a mode-
	// independent SCOPE: when set (a bead id), visibleIssues() narrows every mode
	// (list/board/tree, plus swim/columns) and every Stage 1-4 control to that
	// bead's neighborhood — its children, blockers, dependents, and siblings, plus
	// the root itself (scopeIDs, the same gather the swimlane and the columns
	// navigator use). Entered through R/enterSwim (and detail's R), so the swimlane
	// becomes ONE presentation of a scoped root; esc exits, restoring the pre-scope
	// arrangement from Model.scopeReturn. The zero value "" means unscoped (the
	// whole board). A value field, so viewState.clone() and the nav snapshots carry
	// it with the struct copy.
	scopeRoot string

	// Collapse state (tree): ids whose subtree is folded away, keyed by id so it
	// survives a refresh. minSubtree hides trees with fewer than N descendants
	// (0 = show all).
	collapsed  map[string]bool
	minSubtree int

	// activeQuery is the live board filter ("" = none).
	activeQuery string

	// panelOpen is the preview panel; detail is the open bead's full view (nil =
	// none) and detailTab its active sub-tab.
	panelOpen bool
	detail    *bd.Issue
	detailTab detailTab
}

// clone deep-copies the reference-typed fields (the slices and the collapse
// map) so a saved snapshot can't be mutated in place by a later live edit — the
// same discipline the old field-by-field snapshots kept.
func (vs viewState) clone() viewState {
	c := vs
	c.millerPath = append([]string(nil), vs.millerPath...)
	if vs.collapsed != nil {
		c.collapsed = make(map[string]bool, len(vs.collapsed))
		for k, v := range vs.collapsed {
			c.collapsed[k] = v
		}
	}
	return c
}

// detailID is the open detail bead's id, "" when none — the stable key the nav
// snapshot compares and restores by (a *bd.Issue pointer isn't stable across a
// reopen).
func (vs viewState) detailID() string {
	if vs.detail == nil {
		return ""
	}
	return vs.detail.ID
}

// Model is the whole TUI state.
type Model struct {
	client   *bd.Client
	provider *nlq.Provider
	feedback *nlq.FeedbackLog
	logger   *log.Logger

	width, height int

	issues []bd.Issue
	// graph is the full board (all issues incl. closed) used only to resolve
	// relationships (parent/children/blockers/dependents), so a display filter
	// never blanks the neighborhood. rebuildIndexes builds byID/childrenOf/
	// revDeps from this; the columns/tree/list use issues (filtered).
	graph   []bd.Issue
	columns []rollup.Column

	// boardBands is the kanban's 2D lane split: when boardLane is set, entry i holds
	// column i's lane bands (parallel to columns), and columns[i].Issues is the
	// band-major flatten so the board cursor walks lane order. It is nil for the 1D
	// board (boardLane == ""). Derived state, rebuilt by rebuild(); the lane control
	// snapshots boardLane (in viewState) and cancel→rebuild recomputes this. See
	// board.go.
	boardBands [][]laneBand

	// listRows is the flattened single-column sectioned layout (ViewList);
	// ViewKanban opts into the side-by-side multi-column board instead.
	listRows []listRow

	// viewState groups the per-view display cluster (active view, every cursor/
	// root/drill, sort/collapse, filter, panel/detail). Embedded, so each field
	// stays reachable as m.<field>; the snapshot/restore pairs copy it as a unit.
	viewState

	// Navigation history ([ / ]): browser-style back/forward through the user's
	// own cursor positions (bead + view + filter). navigatingHistory suppresses
	// recording while a restore is in flight.
	navBack           []navPos
	navFwd            []navPos
	navigatingHistory bool
	// layers is the OTHER stack: the esc dismissal stack of things composed OVER
	// the active view (emphasis, an agent arrangement, an analyst filter, the
	// activity feed, a relationship scope, the facet control), in most-recently-
	// raised order. esc pops the top; [ / ] walk navBack/navFwd independently. See
	// layers.go.
	layers []layerKind
	// suppressDetailCheckpoint skips the one detail-open checkpoint the detailMsg
	// handler would otherwise push, for the ONE case where the detail is opening
	// because history is being restored (restoreNav sets it; the handler consumes
	// it). Without it, stepping back INTO a remembered detail would re-checkpoint.
	suppressDetailCheckpoint bool

	// scopeReturn is the pre-scope arrangement, captured (snapshotNav) the moment
	// relationship focus is first entered and restored verbatim by exitScope when
	// esc leaves the scope. It is the scope's dedicated return target — a FULL
	// viewState (every facet/sort/collapse knob and the cursor), so leaving restores
	// the exact prior view, not the nav timeline's deliberately partial write-back.
	// Valid only while scopeRoot != "".
	scopeReturn navPos

	// Activity feed (key a): global reverse-chron change events from the Dolt
	// history layer (internal/dolt). activitySrc is opened lazily.
	activityView   bool
	activitySrc    *dolt.Source
	activityEvents []dolt.Event
	activityIdx    int
	activityErr    string

	// treeRows is the flattened tree layout (ViewTree), rebuilt from the visible
	// set and the depsTree/collapse/minSubtree knobs in viewState.
	treeRows []treeRow

	// winStart holds each column's page-jump window offset, keyed by column
	// key. A map is a reference type, so View (value receiver) can update it.
	winStart map[string]int

	showClosed bool
	// lastAppliedQuery is the query behind the CURRENTLY displayed board — the
	// last one bd accepted. When a user-driven load hits a bd rejection, the
	// filter reverts to this so a bad query never wedges the board on a broken
	// (or blank) state; the board stays on the prior view with a footer error.
	lastAppliedQuery string
	message          string
	msgIsError       bool

	prompt promptKind
	input  textinput.Model
	review nlq.Result
	// vocab is the workspace vocabulary for prompt grounding, refreshed on
	// every UNFILTERED board load (a filtered subset would impoverish it).
	vocab nlq.Vocab
	// re-roll state for the current NL ask.
	rolls      int
	priorRolls []string
	// Live preview of the NL review: while the compiled-query review prompt is
	// open, the compiled query is applied to the board behind it as a temporary
	// filter, so each re-roll/edit visibly reshapes the board and the user
	// accepts the one that looks right. previewPrevQuery is the pre-ask
	// activeQuery, restored verbatim on cancel (esc/n); previewEdited records
	// whether the user hand-edited the previewed query (feedback verdict).
	previewing       bool
	previewPrevQuery string
	previewEdited    bool
	// compile latency accounting: elapsed shows in the footer, esc cancels
	// (stale generations are dropped), slow compiles earn a model hint.
	compileT0      time.Time
	compileSeq     int
	compileElapsed time.Duration

	detailVP viewport.Model
	// Tabbed detail sub-views (Overview/History/Related/Comments). History and
	// comments are fetched once when the detail opens.
	detailHistory  []bd.HistoryEntry
	detailComments []map[string]any
	// md is the shared, cached glamour renderer (fixed style; see New).
	md *mdRenderer

	// Preview panel (space): live view of the highlighted card + its
	// traversable links. Indexes are derived from the loaded issue set.
	panelFocus   bool
	panelLinkIdx int
	panelMD      *panelMDCache
	byID         map[string]bd.Issue
	childrenOf   map[string][]string
	revDeps      map[string][]string
	idPrefix     string // common "<prefix>-" stripped from every id display

	// statusSince is id → the bead's last status-transition time, parsed from
	// the interactions journal once per data refresh (loadIssues) and read by
	// the age column in time-in-status mode. ageMode selects what that column
	// means (updated_at vs time-in-status); the zero value is last-activity, the
	// pre-feature default. See age.go.
	statusSince map[string]time.Time
	ageMode     ageMode

	// Model-server resolution (zero-config): discovery/autostart run async
	// so the board never waits; NL features gate on the outcome.
	resolving      bool
	resolved       bool
	resolveErr     string
	resolveSummary string
	lastResolve    time.Time
	autostartTried bool
	resolveRetries int

	// Agent (MCP) state: the attach layer (the one channel the human is attached
	// to) + provenance tracking.
	attach    attachSlot
	agentSeen bool
	modeBy    string // "user" | "agent"
	filterBy  string

	// Emphasis is an ephemeral decoration layer OVER the view, keyed by target
	// and applied at render time (never per-view state). It is orthogonal to
	// filter/sort — it decorates rows/sections in place, never removing or
	// reordering. esc clears it. See emphasis.go.
	emphasis []agentapi.Emphasis

	// remarks are the ephemeral, issue-keyed notes a shared view carries (bead id
	// → short text): a reviewer sees WHY each bead was selected without the remark
	// ever reaching Beads. It rides WITH the active share — applyShare sets it,
	// detach/channel-switch clears it — so switching away removes remarks from the
	// board and reopening the share restores them. Nothing here is ever written to
	// an issue. See remark.go.
	remarks map[string]string

	// Agent-shares surface (@, shares.go + sessions.go): everything a connected
	// agent shares — ambient name-drop batches (type "mentioned", from the Stop
	// hook) AND deliberately published views (type "view", from MCP show/set_view/
	// emphasize). The durable store is the session-channel store, keyed by Claude
	// Code SessionID and persisted to sessions.json; the browser reads a flat,
	// newest-first projection of it. The agent publishes; it never seizes the
	// screen. `@` opens a master-detail BROWSER (sharesBrowse); A/enter attaches to
	// a channel so its pushes apply live (m.attach).
	sessions       sessionStore
	sharesBrowse   bool           // the master-detail shares browser is open
	shareSections  []shareSection // resolved sections (built on open, rebuilt on r)
	shareSecIdx    int            // focused section (index into shareSections)
	shareBeadIdx   int            // focused bead within the section
	sharePrevPanel bool           // panel-open state to restore when the browser closes
	shareNew       int            // shares arrived since the human last looked (quiet footer)

	// Analyst (!): whole-board questions streamed into the panel surface.
	analyst       *nlq.Analyst
	boardCtx      string // stable prefix body; rebuilt only on board refresh
	analystActive bool   // a stream is in flight
	analystText   string // streamed answer so far (or the finished answer)
	analystQ      string
	analystIDs    []string // finished answer's ids → board filter (esc clears)
	analystWarmed bool     // first ask after a load pays the big prefill
	analystT0     time.Time
	analystFirst  time.Duration // time to first token
	analystChunks int           // streamed deltas so far (≈ tokens)

	spin      spinner.Model
	loading   bool
	compiling bool

	help       help.Model
	keys       keyMap
	detailKeys detailKeyMap

	// control is the open facet control (S and the coming per-facet controls): a
	// small dropdown anchored under its header strip segment, driving ONE facet
	// with live preview. It composes OVER the board (never a full-body modal); the
	// pre-open viewState is snapshotted for esc-revert. See control.go.
	control facetControl

	// Full help overlay (?): all bindings grouped by category, generated from
	// the registry (keys.go). The viewport scrolls it so it never overflows the
	// terminal height. `?`/esc closes it.
	showHelp bool
	helpVP   viewport.Model

	seq int // load generation; stale results are dropped

	// Live board watch: a store change (agent mutating via bd elsewhere)
	// triggers a silent reload that preserves the user's context.
	// boardSig is a signature of the loaded set — an auto-refresh that finds
	// nothing changed stays quiet (the slow poll must not spam the footer).
	// focusIDBefore carries the focused issue id across a reload so selection
	// survives by id (priority edits and external moves reorder rows).
	boardSig       string
	focusIDBefore  string
	autoRefresh    bool // the in-flight reload came from the watcher/poll
	pendingRefresh bool // a store change arrived while a prompt was open
}

// New builds the model. mdStyle is the glamour style name ("dark"/"light"/
// "notty"), resolved by the caller BEFORE the bubbletea Program starts:
// resolving it later (auto-style) would query the terminal on stdin that
// bubbletea already owns and deadlock the first card open.
func New(client *bd.Client, provider *nlq.Provider, analyst *nlq.Analyst, feedback *nlq.FeedbackLog, logger *log.Logger, mdStyle string) Model {
	ti := textinput.New()
	ti.CharLimit = 0
	sp := spinner.New(spinner.WithSpinner(spinner.MiniDot))
	return Model{
		client:   client,
		provider: provider,
		analyst:  analyst,
		feedback: feedback,
		logger:   logger,
		md:       newMdRenderer(mdStyle),
		panelMD:  &panelMDCache{},
		viewState: viewState{
			mode:      rollup.ModeStatus,
			view:      ViewKanban, // the multi-column board is the default layout; b toggles to the single-column list
			flatSort:  rollup.Sort{Key: rollup.SortPriority, Desc: rollup.DefaultDesc(rollup.SortPriority)},
			treeSort:  rollup.Sort{Key: rollup.SortSubtreeSize, Desc: rollup.DefaultDesc(rollup.SortSubtreeSize)},
			collapsed: map[string]bool{},
		},
		winStart:   map[string]int{},
		input:      ti,
		spin:       sp,
		loading:    true, // Init fires the first load
		resolving:  true, // Init fires discovery
		help:       help.New(),
		keys:       defaultKeyMap(),
		detailKeys: defaultDetailKeyMap(),
		sessions:   loadSessions(), // the persisted session channels survive restarts
	}
}

// Init runs the generation-0 load. (Init has a value receiver, so it must
// not rely on mutating the model — loadIssues is pure.)
func (m Model) Init() tea.Cmd {
	return tea.Batch(
		loadIssues(m.client, m.logger, m.seq, m.activeQuery, m.showClosed),
		resolveCmd(m.logger),
		m.spin.Tick,
		staleSweepCmd(),
	)
}

// --- commands ---

func (m *Model) loadCmd() tea.Cmd {
	m.loading = true
	m.seq++
	if is := m.focusedIssue(); is != nil {
		m.focusIDBefore = is.ID // restored by id after the reload settles
	}
	return loadIssues(m.client, m.logger, m.seq, m.activeQuery, m.showClosed)
}

// boardSignature is a cheap fingerprint of what's VISIBLE on a loaded set —
// id, status, priority, type, title. The slow poll reloads unconditionally,
// so this is how an unchanged board stays silent (no "board updated" flash
// every 15s). Two properties matter:
//   - updated_at is excluded: an optimistic priority edit already reflects the
//     store's value, and a bumped updated_at alone must not read as external.
//   - order-independent (sorted by id): a priority edit bumps updated_at,
//     which reorders `bd list`, so an unsorted fingerprint would flag our own
//     edit as a change.
func boardSignature(issues []bd.Issue) string {
	parts := make([]string, len(issues))
	for i, is := range issues {
		parts[i] = fmt.Sprintf("%s:%s:%d:%s:%s", is.ID, is.Status, is.Priority, is.IssueType, is.Title)
	}
	sort.Strings(parts)
	return strings.Join(parts, "\n")
}

func loadIssues(client *bd.Client, logger *log.Logger, seq int, q string, all bool) tea.Cmd {
	return func() tea.Msg {
		var issues, graph []bd.Issue
		var err error
		if q != "" {
			// boardQuery resolves the `subtree=<id>` pseudo-query in-process (bd
			// cannot OR parent with id); for it graph comes back as the full board.
			issues, graph, err = boardQuery(client, q)
		} else {
			issues, err = client.List(all)
		}
		if err != nil {
			logger.Error("load", "query", q, "err", err)
			return issuesMsg{seq: seq, query: q, issues: issues, err: err}
		}
		logger.Debug("load", "query", q, "issues", len(issues))
		// The relationship graph is the WHOLE board (incl. closed): a filtered
		// display must still resolve a bead's true parent/children/blockers even
		// when they're off the filter. Reuse the just-loaded set when it already
		// is the whole board; otherwise one extra list (loads aren't hot).
		if graph == nil {
			graph = issues
			if q != "" || !all {
				if full, ferr := client.List(true); ferr == nil {
					graph = full
				}
			}
		}
		// Parse the interactions journal once here (off the render path) so the
		// age column's time-in-status mode reads a prebuilt map, never the file.
		return issuesMsg{seq: seq, query: q, issues: issues, graph: graph, statusSince: loadStatusAges(), err: err}
	}
}

func (m *Model) addCommentCmd(id, text string) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		if err := client.AddComment(id, text); err != nil {
			return commentAddedMsg{id: id, err: err}
		}
		comments, _ := client.Comments(id) // refetch so it shows immediately
		return commentAddedMsg{id: id, comments: comments}
	}
}

func (m *Model) showCmd(id string) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		issue, err := client.Show(id)
		if err != nil {
			return detailMsg{err: err}
		}
		// History + comments feed the tabs; best-effort so a fetch failure
		// just renders an empty tab, never blocks the detail.
		hist, _ := client.History(id)
		comments, _ := client.Comments(id)
		return detailMsg{issue: issue, history: hist, comments: comments}
	}
}

func (m *Model) compileCmd(nl string) tea.Cmd {
	provider := m.provider
	vocab := m.vocab
	prior := append([]string(nil), m.priorRolls...)
	client := m.client
	m.compileSeq++
	m.compileT0 = time.Now()
	seq := m.compileSeq
	return func() tea.Msg {
		// exec pre-runs a candidate query: row count + 2-3 sample rows are the
		// repair loop's execution-feedback signal.
		exec := func(q string) (int, []string, error) {
			// Route through boardQuery so a `subtree=<id>` candidate pre-executes
			// in-process (bd would reject it) — the repair loop sees a real count,
			// never a false parse-error.
			issues, _, err := boardQuery(client, q)
			if err != nil {
				return 0, nil, err
			}
			sample := make([]string, 0, 3)
			for i, is := range issues {
				if i >= 3 {
					break
				}
				sample = append(sample, is.ID+" "+is.Title)
			}
			return len(issues), sample, nil
		}
		var res nlq.Result
		var err error
		if len(prior) == 0 {
			// First compile: value grounding + execution-feedback repair.
			res, err = provider.CompileWithRepair(nl, vocab, nlq.LexicalGrounder{}, exec)
		} else {
			// Re-roll: the user drives it; still pre-execute for the count.
			res, err = provider.Compile(nl, vocab, prior)
			if err == nil {
				if c, _, xerr := exec(res.Query); xerr == nil {
					res.Count = c
				} else {
					res.Count = -1
				}
			}
		}
		return nlCompiledMsg{seq: seq, res: res, err: err}
	}
}

func (m *Model) startAnalyst(question string) tea.Cmd {
	m.analystQ = question
	m.analystText = ""
	m.analystIDs = nil
	m.analystActive = true
	m.pushLayer(layerAnalyst) // esc clears the answer + its id filter (layers.go)
	m.panelOpen = true
	m.panelFocus = false
	m.analystT0 = time.Now()
	m.analystFirst = 0
	m.analystChunks = 0
	if m.analystWarmed {
		m.setMessage("asking "+m.analyst.Label+"…", false)
	} else {
		m.setMessage("warming board context (first ask)…", false)
	}
	ch := m.analyst.Ask(nlq.AnalystPrefix(m.boardCtx), question)
	return tea.Batch(listenAnalyst(ch), m.spin.Tick)
}

func (m *Model) boardCtxSet(issues []bd.Issue) {
	m.boardCtx = nlq.BoardContext(issues)
	m.analystWarmed = false
	if !m.msgIsError && !m.resolving {
		m.setMessage("", false)
	}
}

// requireModels gates NL features on resolution, with one actionable line.
func (m *Model) requireModels() (tea.Cmd, bool) {
	if m.resolved {
		return nil, true
	}
	if m.resolving {
		m.setMessage("still finding a model server — one moment", false)
		return nil, false
	}
	// Failed earlier: one keypress retries discovery (autostart included).
	m.resolving = true
	m.autostartTried = false
	m.setMessage("retrying model discovery… ("+m.resolveErr+")", false)
	return tea.Batch(resolveCmd(m.logger), m.spin.Tick), false
}

// maybeReResolve fires a re-discovery when a model call fails in a way that
// smells like the server moved (rate-limited; env-pinned setups excluded by
// discovery itself honoring env last).
func (m *Model) maybeReResolve(err error) tea.Cmd {
	if m.resolving || time.Since(m.lastResolve) < 10*time.Second {
		return nil
	}
	s := err.Error()
	if !strings.Contains(s, "unreachable") && !strings.Contains(s, "401") &&
		!strings.Contains(s, "404") && !strings.Contains(s, "not found") {
		return nil
	}
	m.resolving = true
	m.resolved = false
	m.autostartTried = false
	m.setMessage("model server changed — re-discovering…", false)
	m.logger.Info("re-resolve", "cause", s)
	return tea.Batch(resolveCmd(m.logger), m.spin.Tick)
}

// --- state helpers ---

func (m *Model) rebuild() {
	visible := m.visibleIssues()
	facet := rollup.FacetForMode(m.mode)
	if m.boardFacet != "" {
		facet = m.boardFacet // grouper/set_view override — enables label/priority grouping
	}
	item := rollup.ItemSort{Flat: m.flatSort}
	m.columns = rollup.Group(visible, facet)
	m.columns = rollup.SortGrouped(m.columns,
		rollup.SectionSort{Facet: facet, Tree: m.treeSort}, item)
	// 2D board: split each column into lane bands by the break-out lane facet. The
	// bands honor the same two-level sort (the lane facet fixes the band order,
	// flatSort orders cards within a band), and columns[i].Issues is rewritten to
	// the band-major flatten so the board cursor (colIdx/rowIdx) walks lane order.
	// A multi-valued label fans a card into every matching lane (rollup.Group's
	// fan-out), consistent with the flat label board.
	if m.boardLane != "" {
		laneSection := rollup.SectionSort{Facet: m.boardLane, Tree: m.treeSort}
		m.boardBands = make([][]laneBand, len(m.columns))
		for i := range m.columns {
			bands := laneBands(m.columns[i].Issues, m.boardLane, laneSection, item)
			m.boardBands[i] = bands
			m.columns[i].Issues = flattenBands(bands)
		}
	} else {
		m.boardBands = nil
	}
	m.listRows = buildListRows(m.columns)
	m.treeRows = m.buildTreeRows(visible)
	m.clampFocus()
}

// scopeIDs is the relationship-focus neighborhood of scopeRoot: the root itself
// plus its children, blockers, dependents, and siblings — the same gather the
// swimlane (enterSwimRooted's gather) and the columns navigator (millerChildIDs)
// use, resolved against the true graph (byID/childrenOf/revDeps, which are built
// from m.graph). It returns nil when unscoped or the root has left the board, so
// visibleIssues can fall back to the whole board. Deduped, root first.
func (m Model) scopeIDs() []string {
	if m.scopeRoot == "" {
		return nil
	}
	root, ok := m.byID[m.scopeRoot]
	if !ok {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	add := func(id string) {
		if id == "" || seen[id] {
			return
		}
		if _, ok := m.byID[id]; !ok {
			return
		}
		seen[id] = true
		out = append(out, id)
	}
	add(m.scopeRoot) // the focus itself anchors its neighborhood
	for _, id := range m.childrenOf[m.scopeRoot] {
		add(id) // sub-issues
	}
	for _, id := range bd.BlockerIDs(root) {
		add(id) // blocked-by
	}
	for _, id := range m.revDeps[m.scopeRoot] {
		add(id) // blocking (dependents)
	}
	if root.Parent != "" {
		for _, id := range m.childrenOf[root.Parent] {
			add(id) // siblings (share the root's parent; the root is already added)
		}
	}
	return out
}

// scopedIssues is the neighborhood in graph order — the visible set while scoped.
// It resolves against m.graph (the whole board, incl. closed), like the relationship
// views, so a neighbor that a display filter never loaded still appears. nil when
// the scope root is gone (visibleIssues then falls back to the unscoped board).
func (m Model) scopedIssues() []bd.Issue {
	ids := m.scopeIDs()
	if len(ids) == 0 {
		return nil
	}
	set := make(map[string]bool, len(ids))
	for _, id := range ids {
		set[id] = true
	}
	out := make([]bd.Issue, 0, len(ids))
	for _, is := range m.graph {
		if set[is.ID] {
			out = append(out, is)
		}
	}
	return out
}

// visibleIssues narrows the whole board to what a mode renders. Relationship-focus
// scope wins first: while scopeRoot is set, every mode sees only the neighborhood
// (scopedIssues), so cycling list/board/tree and every facet control pivots inside
// it. Otherwise the id filters apply (esc clears them): the agent's arrangement
// list wins over analyst matches.
//
// An explicit id filter (an applied share, a name-drop batch, or analyst
// matches) resolves against the WHOLE board (m.graph), NOT the filtered display
// set (m.issues). The named ids routinely fall outside m.issues — a mentioned
// batch names beads the agent just closed, and a default load excludes closed
// entirely — so intersecting with m.issues silently drops them and blanks the
// board. Ids and relations resolve against the true graph, never the columns.
func (m *Model) visibleIssues() []bd.Issue {
	if scoped := m.scopedIssues(); scoped != nil {
		return scoped
	}
	ids := m.attach.ids
	if len(ids) == 0 {
		ids = m.analystIDs
	}
	if len(ids) == 0 {
		return m.issues
	}
	source := m.graph
	if source == nil {
		source = m.issues // pre-first-load / defensive fallback
	}
	set := make(map[string]bool, len(ids))
	for _, id := range ids {
		set[id] = true
	}
	out := make([]bd.Issue, 0, len(ids))
	for _, is := range source {
		if set[is.ID] {
			out = append(out, is)
		}
	}
	return out
}

// emptyBoardText is the centered message a view shows when it has no rows. An
// applied share / analyst id-filter that legitimately resolves to zero beads
// must read as "this view is empty", never the generic broken-board line.
func (m Model) emptyBoardText() string {
	switch {
	case len(m.attach.ids) > 0:
		return "no matches for this share — esc to undo"
	case len(m.analystIDs) > 0:
		return "no matches for this view — esc clears"
	case m.activeQuery != "":
		return "no matches — esc clears the filter"
	default:
		return "no issues — r refresh · c include closed · / query"
	}
}

func (m *Model) clampFocus() {
	m.treeIdx = clamp(m.treeIdx, 0, max(0, len(m.treeRows)-1))
	if len(m.columns) == 0 {
		m.colIdx, m.rowIdx = 0, 0
		return
	}
	m.colIdx = clamp(m.colIdx, 0, len(m.columns)-1)
	m.rowIdx = clamp(m.rowIdx, 0, max(0, len(m.columns[m.colIdx].Issues)-1))
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// previewIssue is what the panel shows: the board's highlighted card.
func (m *Model) previewIssue() *bd.Issue {
	return m.focusedIssue()
}

// setView switches the active nav view. It is the ONLY site that mutates
// m.view. It deliberately touches nothing else: the activity/shares overlays
// compose OVER the nav view and are never routed through here, and per-view
// roots/paths (swimRoot, millerPath, …) are reset by the enter* funcs.
func (m *Model) setView(k viewKind) {
	m.view = k
}

// cycleView steps the active view directly — list → board → tree → list — with a
// single key and no menu; setView stays the only mutator of m.view. Any facet
// override re-syncs m.mode through facetToMode so the header and the digit/m
// grouping keys read right after the switch. The tree/relationship views step
// back to the list. The digit/m grouping keys themselves are unchanged.
func (m *Model) cycleView() {
	m.modeBy = "user"
	m.activityView = false
	switch m.view {
	case ViewList:
		m.setView(ViewKanban)
	case ViewKanban:
		m.setView(ViewTree)
	default: // ViewTree and the relationship views wrap back to the list
		m.setView(ViewList)
	}
	if mode, ok := facetToMode(m.boardFacet); ok {
		m.mode = mode
	}
}

func (m *Model) focusedIssue() *bd.Issue {
	// The overlays (shares browser, activity feed) take focus priority over
	// whatever nav view is beneath them.
	if m.sharesBrowse {
		return m.shareBrowseFocused()
	}
	if m.activityView {
		if is, ok := m.byID[m.activityFocusID()]; ok {
			return &is
		}
		return nil
	}
	switch m.view {
	case ViewSwim:
		return m.swimFocusedIssue()
	case ViewColumns:
		if is, ok := m.byID[m.millerSelectedID()]; ok {
			return &is
		}
		return nil
	case ViewTree:
		if m.treeIdx >= len(m.treeRows) {
			return nil
		}
		is := m.treeRows[m.treeIdx].issue
		return &is
	}
	// ViewList / ViewKanban both select from the columns model.
	if m.colIdx >= len(m.columns) {
		return nil
	}
	col := m.columns[m.colIdx]
	if m.rowIdx >= len(col.Issues) {
		return nil
	}
	is := col.Issues[m.rowIdx]
	return &is
}

// jumpTo moves the selection (tree row or board card) to the given issue.
func (m *Model) jumpTo(id string) bool {
	if m.view == ViewTree {
		for i, r := range m.treeRows {
			if r.issue.ID == id {
				m.treeIdx = i
				return true
			}
		}
		return false
	}
	for ci, col := range m.columns {
		for ri, is := range col.Issues {
			if is.ID == id {
				m.colIdx, m.rowIdx = ci, ri
				return true
			}
		}
	}
	return false
}

func (m *Model) nav(dc, dr int) {
	if len(m.columns) == 0 {
		return
	}
	m.colIdx = clamp(m.colIdx+dc, 0, len(m.columns)-1)
	m.rowIdx = clamp(m.rowIdx+dr, 0, max(0, len(m.columns[m.colIdx].Issues)-1))
}

func (m *Model) setMessage(s string, isErr bool) {
	m.message = s
	m.msgIsError = isErr
}

// hierarchicalView reports whether the current view sorts by whole-subtree
// metrics (treeSort) rather than the flat within-section order (flatSort).
func (m Model) hierarchicalView() bool {
	return m.view == ViewTree || m.mode == rollup.ModeRoot
}

// activeSort is the sort in effect for the current view.
func (m Model) activeSort() rollup.Sort {
	if m.hierarchicalView() {
		return m.treeSort
	}
	return m.flatSort
}

func nextSort(keys []rollup.SortKey, cur rollup.SortKey) rollup.Sort {
	idx := 0
	for i, k := range keys {
		if k == cur {
			idx = (i + 1) % len(keys)
			break
		}
	}
	k := keys[idx]
	return rollup.Sort{Key: k, Desc: rollup.DefaultDesc(k)}
}

// cycleSort advances the sort key for the current view (S). Hierarchical views
// cycle the subtree-metric keys; flat views cycle the row keys.
func (m *Model) cycleSort() {
	if m.hierarchicalView() {
		m.treeSort = nextSort(rollup.TreeSortKeys, m.treeSort.Key)
	} else {
		m.flatSort = nextSort(rollup.FlatSortKeys, m.flatSort.Key)
	}
	m.rebuild()
}

// toggleCollapse folds/unfolds the focused node's subtree (z), in the tree or
// octopus. Persisted by id, so it survives a refresh.
func (m *Model) toggleCollapse() {
	is := m.focusedIssue()
	if is == nil {
		return
	}
	if m.collapsed[is.ID] {
		delete(m.collapsed, is.ID)
	} else {
		m.collapsed[is.ID] = true
	}
	m.rebuild()
}

// toggleCollapseAll folds every branch in the current tree relation, or
// expands everything when anything is already folded (Z).
func (m *Model) toggleCollapseAll() {
	if len(m.collapsed) > 0 {
		m.collapsed = map[string]bool{}
	} else {
		m.collapseAllBranches()
	}
	m.rebuild()
}

// subtreeFilterSteps are the min-descendant thresholds F cycles through.
var subtreeFilterSteps = []int{0, 2, 5, 10}

// cycleSubtreeFilter advances the min-subtree-size filter (F): show only trees
// with at least N descendants.
func (m *Model) cycleSubtreeFilter() {
	idx := 0
	for i, s := range subtreeFilterSteps {
		if s == m.minSubtree {
			idx = (i + 1) % len(subtreeFilterSteps)
			break
		}
	}
	m.minSubtree = subtreeFilterSteps[idx]
	m.rebuild()
}

func (m *Model) setMode(mode rollup.Mode) {
	// A legacy mode key returns to mode-driven grouping, clearing any grouper/
	// set_view facet override; when nothing changes the early return preserves the
	// old byte-identical behavior.
	if mode == m.mode && m.boardFacet == "" {
		return
	}
	m.mode = mode
	m.boardFacet = ""
	m.rebuild()
}

// changePriority raises (delta -1) or lowers (delta +1) the focused issue's
// priority — the one mutation the TUI performs. bd's scale runs 0 highest … 4
// lowest, so raising priority decreases the number. Optimistic: the card
// repaints (and reorders, since columns sort by priority) immediately, bd
// syncs in the background, and a reload confirms or rolls back.
func (m *Model) changePriority(delta int) tea.Cmd {
	var target *bd.Issue
	if m.detail != nil {
		target = m.detail
	} else {
		target = m.focusedIssue()
	}
	if target == nil {
		return nil
	}
	id := target.ID
	next := clamp(target.Priority+delta, priMin, priMax)
	if next == target.Priority {
		dir := "highest"
		if delta > 0 {
			dir = "lowest"
		}
		m.setMessage(fmt.Sprintf("%s already at %s priority (P%d)", id, dir, target.Priority), false)
		return nil
	}
	for i := range m.issues {
		if m.issues[i].ID == id {
			m.issues[i].Priority = next
		}
	}
	for i := range m.graph {
		if m.graph[i].ID == id {
			m.graph[i].Priority = next
		}
	}
	if m.detail != nil && m.detail.ID == id {
		d := *m.detail
		d.Priority = next
		m.detail = &d
	}
	m.rebuild()
	m.jumpTo(id) // priority reorders within the column; keep focus on the card
	// Fold the edit into the signature so the watcher's reconciling refresh
	// (our own last-touched write) stays silent and this confirmation holds.
	m.boardSig = boardSignature(m.issues)
	m.setMessage(fmt.Sprintf("%s → P%d", id, next), false)
	client := m.client
	logger := m.logger
	return func() tea.Msg {
		err := client.SetPriority(id, next)
		if err != nil {
			logger.Error("priority", "id", id, "next", next, "err", err)
		}
		return prioritySyncedMsg{id: id, err: err}
	}
}

func (m *Model) openPrompt(kind promptKind, prefill, promptStr string) tea.Cmd {
	m.prompt = kind
	m.input.Prompt = promptStr
	m.input.SetValue(prefill)
	m.input.CursorEnd()
	m.input.Width = max(10, m.width-ansi.StringWidth(promptStr)-4)
	return m.input.Focus()
}

func (m *Model) closePrompt() {
	m.prompt = promptNone
	m.input.Blur()
	m.input.SetValue("")
}

// openHelp shows the full help overlay, sized to the body region so it fits the
// terminal exactly; the viewport scrolls if the grid is taller than the body.
func (m *Model) openHelp() {
	m.showHelp = true
	scr := m.layoutScreen()
	m.helpVP = viewport.New(m.width, scr.Body.H)
	m.helpVP.SetContent(m.renderHelpContent(m.width))
	m.helpVP.GotoTop()
}

// previewCmd applies a query as the live board filter behind the NL review
// (the existing filter machinery — activeQuery + reload). Selection survives by
// id, so each re-roll/edit visibly reshapes the board under the prompt.
func (m *Model) previewCmd(query string) tea.Cmd {
	if query == m.activeQuery {
		return nil // already showing this filter — no flash
	}
	m.activeQuery = query
	return m.loadCmd()
}

// endPreview clears the NL-preview session. revert restores the pre-ask filter
// (cancel); otherwise the previewed query is kept as the committed filter.
func (m *Model) endPreview(revert bool) tea.Cmd {
	if !m.previewing {
		return nil
	}
	m.previewing = false
	if revert {
		return m.previewCmd(m.previewPrevQuery)
	}
	return nil
}

// drainPendingRefresh fires a deferred auto-refresh once no prompt is open —
// a store change that arrived mid-prompt is applied when the prompt closes.
func (m *Model) drainPendingRefresh() tea.Cmd {
	if !m.pendingRefresh || m.prompt != promptNone {
		return nil
	}
	m.pendingRefresh = false
	m.autoRefresh = true
	return tea.Batch(m.loadCmd(), m.spin.Tick)
}

func (m *Model) recordFeedback(action nlq.FeedbackAction, final string) {
	m.feedback.Append(nlq.FeedbackRecord{
		Provider: m.provider.Label,
		NL:       m.review.NL,
		Compiled: m.review.Query,
		Action:   action,
		Final:    final,
	})
}

// detailVPHeight is the scrollable body height inside the detail box: bodyH
// minus border(2), title(1), tab strip(1).
func (m Model) detailVPHeight() int { return max(1, m.height-2-4) }

func (m *Model) openDetail(issue bd.Issue, history []bd.HistoryEntry, comments []map[string]any) {
	m.detail = &issue
	m.detailHistory = history
	m.detailComments = comments
	m.detailTab = tabOverview
	m.detailVP = viewport.New(max(1, m.width-4), m.detailVPHeight())
	m.setDetailContent()
	m.detailVP.GotoTop()
}

// setDetailContent rebuilds the viewport body for the active tab.
func (m *Model) setDetailContent() {
	if m.detail == nil {
		return
	}
	m.detailVP.SetContent(m.detailTabContent(*m.detail, m.detailTab, max(10, m.width-6)))
}

func (m *Model) refreshDetail() {
	if m.detail == nil {
		return
	}
	m.detailVP.Width = max(1, m.width-4)
	m.detailVP.Height = m.detailVPHeight()
	m.setDetailContent()
}

// --- update ---

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.help.Width = msg.Width
		if m.detail != nil {
			m.refreshDetail()
		}
		if m.showHelp {
			scr := m.layoutScreen()
			m.helpVP.Width = m.width
			m.helpVP.Height = scr.Body.H
			m.helpVP.SetContent(m.renderHelpContent(m.width))
		}
		return m, nil

	case spinner.TickMsg:
		if !m.loading && !m.compiling && !m.analystActive && !m.resolving {
			return m, nil
		}
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd

	case issuesMsg:
		if msg.seq != m.seq {
			return m, nil // stale generation
		}
		m.loading = false
		auto := m.autoRefresh
		m.autoRefresh = false
		if msg.err != nil {
			// An auto-refresh racing a transient bd error stays quiet; the
			// poll retries. A user-driven load surfaces the error and, for a
			// rejected query (bad field, unsupported operator, parse error),
			// reverts the filter to the last one bd accepted so the board stays
			// on the prior view instead of wedging on the broken query.
			if !auto {
				if msg.query != "" {
					m.setMessage("query error: "+msg.err.Error(), true)
					m.activeQuery = m.lastAppliedQuery
				} else {
					m.setMessage("error: "+msg.err.Error(), true)
				}
			}
			return m, nil
		}
		m.lastAppliedQuery = msg.query
		newSig := boardSignature(msg.issues)
		changed := newSig != m.boardSig
		m.boardSig = newSig
		m.issues = msg.issues
		// While an NL preview is live, the board IS the compiled query's result,
		// so the review footer's count reflects the real loaded set (accurate
		// after an edit/re-roll, no separate pre-execution).
		if m.previewing {
			m.review.Count = len(msg.issues)
		}
		if msg.graph != nil {
			m.graph = msg.graph
		} else {
			m.graph = msg.issues
		}
		m.statusSince = msg.statusSince // time-in-status map for the age column
		m.rebuildIndexes()
		m.panelMD.id = "" // descriptions may have changed; drop the cache
		m.rebuild()

		// The @ shares browser, if open, re-resolves its sections against the
		// fresh board so each section's beads update in place (a just-closed or
		// just-created bead re-resolves) — cursor preserved by id.
		if m.sharesBrowse {
			m.rebuildShareSectionsPreservingFocus()
		}

		// Restore selection by id: priority edits and external moves reorder
		// rows, so a row index would drift. If the focused issue is gone, the
		// clamp in rebuild() already left focus somewhere valid.
		gone := ""
		if m.focusIDBefore != "" && !m.jumpTo(m.focusIDBefore) {
			gone = m.focusIDBefore
		}
		m.focusIDBefore = ""

		// Remember the footer so a SILENT reconcile (our own edit's refresh,
		// or an idle poll) doesn't blank a standing confirmation.
		prevMsg, prevErr := m.message, m.msgIsError

		if m.activeQuery != "" {
			if !auto {
				m.setMessage("query: "+m.activeQuery, false)
			}
		} else {
			m.vocab = nlq.DeriveVocab(msg.issues)
			// New board → new stable analyst prefix; the next ask re-warms
			// the server's prefix cache. (This clears any transient message.)
			m.boardCtxSet(msg.issues)
		}

		// Set the footer last, so it survives boardCtxSet's reset.
		switch {
		case auto && changed && gone != "":
			m.setMessage("board updated — "+gone+" left the view, moved focus", false)
		case auto && changed:
			m.setMessage("board updated", false)
		case auto:
			m.setMessage(prevMsg, prevErr) // silent reconcile: leave the footer be
		}
		return m, nil

	case detailMsg:
		m.loading = false
		if msg.err != nil {
			m.setMessage("error: "+msg.err.Error(), true)
			return m, nil
		}
		// Opening a detail is a coarse-timeline checkpoint: capture the pre-detail
		// position (the board + the bead you opened from) BEFORE openDetail mutates
		// m.detail, so `[`/esc returns there and `]` reopens. Skipped only when the
		// detail is itself the target of a history restore.
		if m.suppressDetailCheckpoint {
			m.suppressDetailCheckpoint = false
		} else if !m.attach.active {
			// While attached the open is ephemeral within the attach layer (sticky
			// attach), so it must not checkpoint the [ / ] timeline either.
			m.pushNav(m.snapshotNav())
		}
		m.openDetail(msg.issue, msg.history, msg.comments)
		return m, nil

	case prioritySyncedMsg:
		if msg.err != nil {
			// Surface bd's real message verbatim, and reload to roll the
			// optimistic paint back to the store's truth.
			m.setMessage(msg.err.Error(), true)
			return m, tea.Batch(m.loadCmd(), m.spin.Tick)
		}
		// Success: the optimistic paint already matches the store. The
		// watcher's silent auto-refresh (triggered by our own write)
		// reconciles updated_at; no explicit reload, so the confirmation
		// footer survives.
		return m, nil

	case BoardChangedMsg:
		// The store changed on disk (an agent mutating via bd elsewhere).
		// Never interrupt a live prompt/review — defer until it closes.
		if m.prompt != promptNone {
			m.pendingRefresh = true
			return m, nil
		}
		m.autoRefresh = true
		return m, tea.Batch(m.loadCmd(), m.spin.Tick)

	case staleSweepMsg:
		m.sweepStale(time.Now())
		return m, staleSweepCmd()

	case activityMsg:
		return m.handleActivity(msg)

	case commentAddedMsg:
		if msg.err != nil {
			m.setMessage("comment failed: "+msg.err.Error(), true)
			return m, nil
		}
		if m.detail != nil && m.detail.ID == msg.id {
			m.detailComments = msg.comments
			m.setDetailContent()
		}
		m.setMessage("comment added", false)
		return m, nil

	case nlCompiledMsg:
		if msg.seq != m.compileSeq {
			return m, nil // cancelled or superseded
		}
		m.compiling = false
		m.compileElapsed = time.Since(m.compileT0)
		if msg.err != nil {
			if cmd := m.maybeReResolve(msg.err); cmd != nil {
				m.closePrompt()
				return m, cmd
			}
		}
		if msg.err != nil {
			m.closePrompt()
			m.setMessage("nlq error: "+msg.err.Error(), true)
			return m, nil
		}
		m.review = msg.res
		// Log the repair trail (only when a revision actually happened) —
		// each attempt's query + count + trigger is training signal.
		if len(msg.res.Attempts) > 1 {
			for _, att := range msg.res.Attempts {
				m.feedback.Append(nlq.FeedbackRecord{
					Provider: m.provider.Label,
					NL:       msg.res.NL,
					Compiled: att.Query,
					Action:   nlq.Repaired,
					Count:    att.Count,
					Trigger:  att.Trigger,
				})
			}
		}
		m.prompt = promptNLReview
		m.input.Blur()
		m.setMessage("", false)
		// Live-preview the compiled query on the board behind the review prompt.
		// The pre-ask filter is captured ONCE (the first review of this ask), so
		// re-rolls re-preview without losing the state to revert to on cancel.
		if !m.previewing {
			m.previewing = true
			m.previewPrevQuery = m.activeQuery
			m.previewEdited = false
		}
		return m, tea.Batch(m.previewCmd(m.review.Query), m.spin.Tick)

	case resolvedMsg:
		m.lastResolve = time.Now()
		r := msg.r
		if r.Err != "" {
			if r.NoServer && !m.autostartTried && discover.CanAutostart() {
				// Stage 2: nothing answered — spawn omlx and wait for health.
				m.autostartTried = true
				m.setMessage("starting local model server…", false)
				m.logger.Info("autostart stage", "cause", r.Err)
				return m, tea.Batch(autostartCmd(m.logger), m.spin.Tick)
			}
			if r.NoServer && m.autostartTried && m.resolveRetries < 5 {
				// The server we just started is still settling; probe again
				// shortly rather than declaring failure.
				m.resolveRetries++
				return m, tea.Tick(700*time.Millisecond, func(time.Time) tea.Msg { return resolveRetryMsg{} })
			}
			m.resolving = false
			m.resolved = false
			m.resolveErr = r.Err
			m.resolveSummary = ""
			m.setMessage("models off: "+r.Err+" — see `bb status`", true)
			return m, nil
		}
		m.resolving = false
		m.resolveRetries = 0
		m.resolved = true
		m.resolveErr = ""
		m.provider.Model = r.Compiler.Model
		m.provider.URL = r.Compiler.URL
		m.provider.Key = r.Compiler.Key
		m.provider.Label = nlq.LabelFor(r.Compiler.Model)
		m.analyst.Model = r.Analyst.Model
		m.analyst.URL = r.Analyst.URL
		m.analyst.Key = r.Analyst.Key
		m.analyst.Label = nlq.LabelFor(r.Analyst.Model)
		m.resolveSummary = r.Summary()
		note := "models: " + m.resolveSummary
		if r.Notice != "" {
			note = r.Notice + " · " + note
		}
		m.setMessage(note, false)
		m.logger.Info("resolution applied", "summary", m.resolveSummary)
		return m, nil

	case resolveRetryMsg:
		return m, tea.Batch(resolveCmd(m.logger), m.spin.Tick)

	case autostartDoneMsg:
		if msg.err != nil {
			m.resolving = false
			m.resolved = false
			m.resolveErr = msg.err.Error()
			m.setMessage("models off: "+msg.err.Error(), true)
			return m, nil
		}
		// Stage 3: server is up — resolve again (fast now).
		m.setMessage(fmt.Sprintf("model server started (%.1fs) — picking models…", msg.elapsed.Seconds()), false)
		return m, tea.Batch(resolveCmd(m.logger), m.spin.Tick)

	case analystEventMsg:
		return m.handleAnalystEvent(msg)

	case agentapi.Request:
		if !m.agentSeen {
			m.agentSeen = true
			m.logger.Info("first agent connection")
		}
		resp, cmd := m.handleAgent(msg.Action)
		select {
		case msg.Reply <- resp:
		default: // the caller gave up (timeout); never block the UI
		}
		return m, cmd

	case tea.KeyMsg:
		before := m.snapshotNav()
		layersBefore := len(m.layers)
		updated, cmd := m.handleKey(msg)
		// A store change deferred while a prompt was open drains the moment
		// the prompt closes (drainPendingRefresh no-ops otherwise).
		if mm, ok := updated.(Model); ok {
			// A keypress that RAISED a layer (a relationship scope, an attach, an
			// emphasis, the activity feed, a facet control) is a LAYER-stack
			// interaction — esc dismisses it. It must not ALSO checkpoint the
			// HISTORY timeline, or the same interaction would sit on BOTH stacks
			// (a stray `[` back after esc already dismissed it). Recording runs
			// only when no new layer was raised, so scope/attach entry stays off
			// the [ / ] timeline while mode switches and drills — which raise no
			// layer — still checkpoint. This is the "no interaction on both
			// stacks" invariant (layers.go).
			//
			// While ATTACHED, nothing checkpoints: the attach is sticky, so every
			// local move/view-change under it is ephemeral within the layer (input.go)
			// — detach restores the exact pre-attach view, and no fiddling may leak
			// onto the history timeline.
			if len(mm.layers) <= layersBefore && !mm.attach.active {
				mm.recordNav(before) // browser-style history for [ / ]
			}
			if drain := mm.drainPendingRefresh(); drain != nil {
				return mm, tea.Batch(cmd, drain)
			}
			return mm, cmd
		}
		return updated, cmd
	}
	return m, nil
}

func (m Model) handleAnalystEvent(msg analystEventMsg) (tea.Model, tea.Cmd) {
	ev := msg.ev
	switch {
	case ev.Err != nil:
		m.analystActive = false
		if cmd := m.maybeReResolve(ev.Err); cmd != nil {
			return m, cmd
		}
		m.setMessage("analyst error: "+ev.Err.Error(), true)
		return m, nil
	case ev.Done:
		m.analystActive = false
		m.analystWarmed = true
		total := time.Since(m.analystT0)
		ids := nlq.ParseIDs(m.analystText)
		m.analystIDs = ids
		m.feedback.Append(nlq.FeedbackRecord{
			Provider: m.analyst.Label,
			NL:       m.analystQ,
			Compiled: strings.Join(ids, " "),
			Action:   nlq.AnalystAsked,
		})
		m.logger.Info("analyst", "q", m.analystQ, "ids", len(ids),
			"first", m.analystFirst.Seconds(), "total", total.Seconds())
		lat := fmt.Sprintf("first token %.1fs · total %.1fs", m.analystFirst.Seconds(), total.Seconds())
		if len(ids) > 0 {
			m.rebuild()
			m.setMessage(fmt.Sprintf("analyst: %d beads matched (%s) — esc clears", len(ids), lat), false)
		} else {
			m.setMessage("analyst answered, no ids block ("+lat+")", false)
		}
		return m, nil
	default:
		if m.analystFirst == 0 {
			m.analystFirst = time.Since(m.analystT0)
		}
		m.analystChunks++
		m.analystText += ev.Chunk
		return m, listenAnalyst(msg.ch)
	}
}
