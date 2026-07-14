// Package agentapi is the contract between the MCP server and the TUI: the
// request messages an agent can send onto the bubbletea loop, and the view
// serialization the TUI answers with — a physical description of what is
// literally on screen, never an abstract state dump.
package agentapi

import (
	"github.com/awhitty/bb/internal/bd"
)

// Request is one agent action, delivered to the UI via tea.Program.Send.
// The UI computes the reply on its own goroutine and answers on Reply.
type Request struct {
	Action Action
	Reply  chan Response // buffered(1); the UI never blocks on it
}

// Action is one of the concrete *Action structs.
type Action interface{ isAction() }

// ViewAction reads the current screen (no mutation).
type ViewAction struct{}

// ShowAction arranges the board: an id list and/or a pre-validated query,
// optional mode, and the agent's title for the arrangement. Issues carries
// the pre-fetched result set when Query is set (fetched off-thread by the
// server so the UI never waits on bd).
type ShowAction struct {
	Query  string
	IDs    []string
	Title  string
	Mode   string // "", status|type|root|blockers|tree
	Issues []bd.Issue
	// Remarks are ephemeral, issue-keyed notes the agent attaches to THIS view
	// (bead id → short text): a reviewer sees WHY a bead was selected without the
	// remark ever touching the issue record. Additive — an older client omits it.
	Remarks map[string]string
	// Session is the originating Claude Code SessionID (the same key the Stop
	// hook registers a channel under). The published view lands on that
	// session's channel; empty routes to the shared "unattributed" channel.
	Session string
}

// SelectAction moves the focused card. Errors (in Response.Err) when the id
// is not in the current view rather than rearranging.
type SelectAction struct {
	ID    string
	Panel *bool // open/close the preview panel alongside; nil = leave as is
}

// ResetAction drops agent arrangements and restores the user's prior view.
type ResetAction struct{}

// RefreshAction applies a fresh board load (Issues fetched off-thread).
type RefreshAction struct {
	Issues []bd.Issue
}

// ReprioritizeAction sets an issue's priority (0-4) — the ONE mutation exposed
// over MCP. Applied optimistically with a footer notice, then synced via bd.
type ReprioritizeAction struct {
	ID       string
	Priority int
}

// Emphasis is one render-time decoration keyed by target. It is orthogonal to
// filter/sort: it decorates in place and never removes or reorders anything.
// Kind "issue" (Ref = id) decorates that row wherever it appears; kind
// "section" (Ref = description|notes|related|overview|history|comments)
// decorates that region of the detail/panel.
type Emphasis struct {
	Kind  string `json:"kind"`            // issue | section
	Ref   string `json:"ref"`             // issue id, or section name
	Style string `json:"style"`           // highlight | outline | marker | spotlight
	Label string `json:"label,omitempty"` // optional short callout
}

// EmphasizeAction adds a decoration layer over the current view (replacing any
// prior emphasis). SpecAction below can also carry sort/collapse/threshold.
type EmphasizeAction struct {
	Targets []Emphasis
	// Session is the originating Claude Code SessionID; the published emphasis
	// view lands on that session's channel (empty → the unattributed channel).
	Session string
}

// ClearEmphasisAction drops all emphasis.
type ClearEmphasisAction struct{}

// SpecAction sets the view-spec knobs an agent can drive: mode, filter, sort,
// collapse, and thresholds. Empty/zero fields are left unchanged. Issues
// carries the pre-fetched query result when Query is set (fetched off-thread).
type SpecAction struct {
	Mode       string     // "" = leave; the VIEW: list|kanban|tree|relationship|columns (legacy aliases status|type|root|blockers still map to a grouped board)
	Traverse   string     // "" = leave; the tree/columns nesting relation: hierarchy | deps
	Group      string     // "" = leave; the facet the current view groups by: none|status|type|ancestor|blockers|label|priority
	Lane       string     // "" = leave; the kanban break-out lane facet (2nd board axis): none|status|type|ancestor|blockers|label|priority
	Root       string     // relationship-board / columns root id (mode "relationship" | "columns")
	Scope      string     // "" = leave; relationship-focus scope root: narrows every mode to this bead's neighborhood
	Query      *string    // nil = leave; "" = clear the filter
	IDs        []string   // explicit id list (nil = leave)
	Issues     []bd.Issue // pre-fetched when Query is set
	Title      string
	SortKey    string // "" = leave
	SortDir    string // asc | desc (default per key)
	Collapse   *CollapseSpec
	MinGroup   *int // ancestor: min members for a section (reserved; threshold is always-on)
	MinSubtree *int // tree: hide trees with fewer than N descendants
	// Remarks are ephemeral, issue-keyed notes the agent attaches to THIS view
	// (bead id → short text): a reviewer sees WHY a bead was selected without the
	// remark ever touching the issue record. Additive — an older client omits it.
	Remarks map[string]string
	// Session is the originating Claude Code SessionID (the same key the Stop
	// hook registers a channel under). The published view lands on that
	// session's channel; empty routes to the shared "unattributed" channel.
	Session string
}

// CollapseSpec sets the tree collapse state: Level 0 rolls up to the top-level
// roots; NodeIDs collapses exactly those ids; ExpandAll clears all folds.
type CollapseSpec struct {
	Level     *int
	NodeIDs   []string
	ExpandAll bool
}

// Mention marks one bead-id reference inside an excerpt's Text: the full id and
// the byte span [Start,End) it occupies, so the TUI can render that slice as an
// inline chiclet.
type Mention struct {
	ID    string `json:"id"`
	Start int    `json:"start"`
	End   int    `json:"end"`
}

// Excerpt is a bounded slice of the agent's turn around one or more mentions —
// the CONTEXT the beads came up in. Text is the (whitespace-collapsed) prose;
// Mentions marks where each bead id sits in it.
type Excerpt struct {
	Text     string    `json:"text"`
	Mentions []Mention `json:"mentions"`
}

// NameDropAction is an AMBIENT signal (not an explicit agent tool call): a
// Claude Code Stop hook noticed the agent mention these bead ids in its latest
// turn and pushed them in, along with the CONVERSATION they came from and the
// EXCERPTS of prose around each mention. The TUI rings the batch into a bounded
// history so the human sees which thread named which beads, and why, without
// either party doing anything extra. ConvoName + Excerpts are additive; IDs +
// Snippet stay for backward-compat (an older hook binary sends only those).
type NameDropAction struct {
	SessionID string    `json:"session_id"`
	ConvoName string    `json:"convo_name,omitempty"`
	TS        string    `json:"ts"`
	IDs       []string  `json:"ids"`
	Snippet   string    `json:"snippet,omitempty"`
	Excerpts  []Excerpt `json:"excerpts,omitempty"`
}

// SessionEndAction archives a session's channel. The Claude Code SessionEnd hook
// fires ONCE, at the end of a conversation, and POSTs the ended SessionID (via
// `bb hook-end`). Unlike the Stop hook — which fires every turn and means
// register/refresh — SessionEnd is the distinct, once-per-conversation archive
// trigger, so no signal is overloaded. On receipt the TUI marks that session's
// channel State=archived: it no longer reads live.
type SessionEndAction struct {
	SessionID string `json:"session_id"`
}

func (ViewAction) isAction()          {}
func (ShowAction) isAction()          {}
func (SelectAction) isAction()        {}
func (ResetAction) isAction()         {}
func (RefreshAction) isAction()       {}
func (ReprioritizeAction) isAction()  {}
func (EmphasizeAction) isAction()     {}
func (ClearEmphasisAction) isAction() {}
func (SpecAction) isAction()          {}
func (NameDropAction) isAction()      {}
func (SessionEndAction) isAction()    {}

// Response carries the narrated view plus the same facts as structured data.
type Response struct {
	Text string // the view serialization (or an error hint)
	Data any    // structuredContent (a *View, usually)
	Err  string // non-empty → tool error
}

// --- structured view (mirrors the narration, field for field) ---

type View struct {
	Screen   ScreenInfo   `json:"screen"`
	Mode     Provenanced  `json:"mode"`
	Filter   Provenanced  `json:"filter"`
	Sort     SortInfo     `json:"sort"`
	Board    *BoardInfo   `json:"board,omitempty"`
	Tree     *TreeInfo    `json:"tree,omitempty"`
	Swim     *SwimInfo    `json:"relationship_board,omitempty"`
	Columns  *ColumnsInfo `json:"columns,omitempty"`
	Panel    PanelInfo    `json:"panel"`
	Emphasis []Emphasis   `json:"emphasis,omitempty"`
	Shares   *SharesInfo  `json:"agent_shares,omitempty"`
	Footer   string       `json:"footer"`
}

// SharesInfo reports the agent-shares stream: what the agent has published (and
// name-drops), whether the human is following (live-apply) or pulling on their
// own schedule with @, and the newest entries so the agent knows what it shared.
type SharesInfo struct {
	Total     int          `json:"total"`
	Unseen    int          `json:"unseen"`
	Following bool         `json:"following"`
	Entries   []ShareEntry `json:"entries,omitempty"` // newest first, capped
}

type ShareEntry struct {
	ID   string `json:"id"`
	Type string `json:"type"` // mentioned | view
	Name string `json:"name"`
	TS   string `json:"ts"`
}

// SortInfo is the active ordering for the current view.
type SortInfo struct {
	Key string `json:"key"`
	Dir string `json:"dir"` // asc | desc
}

type ScreenInfo struct {
	Width  int    `json:"width"`
	Height int    `json:"height"`
	Focus  string `json:"focus"` // BOARD | PANEL | PROMPT(<kind>) | DETAIL
}

// Provenanced is a value plus who arranged it.
type Provenanced struct {
	Value string `json:"value"`
	SetBy string `json:"set_by"`          // "user" | "agent"
	Title string `json:"title,omitempty"` // the agent's name for its arrangement
}

type BoardInfo struct {
	TotalColumns int          `json:"total_columns"`
	FirstVisible int          `json:"first_visible_column"` // 1-based
	LastVisible  int          `json:"last_visible_column"`
	Columns      []ColumnInfo `json:"columns"`
}

type ColumnInfo struct {
	Name        string    `json:"name"`
	Total       int       `json:"total"`
	FirstRow    int       `json:"first_visible_row"` // 1-based
	LastRow     int       `json:"last_visible_row"`
	HiddenAbove int       `json:"hidden_above"`
	HiddenBelow int       `json:"hidden_below"`
	Rows        []RowInfo `json:"rows"`
}

type RowInfo struct {
	ID       string `json:"id"`
	Row      int    `json:"row"` // 1-based position within the column/tree
	Title    string `json:"title"`
	Priority int    `json:"priority"`
	Status   string `json:"status"`
	// Decision signal: how many OPEN issues block this one (0 = ready to work).
	Blockers int  `json:"blockers,omitempty"`
	Depth    int  `json:"depth,omitempty"` // tree nesting depth
	Focused  bool `json:"user_focus,omitempty"`
}

type TreeInfo struct {
	Relation    string    `json:"relation"` // hierarchy | deps
	TotalRows   int       `json:"total_rows"`
	FirstRow    int       `json:"first_visible_row"`
	LastRow     int       `json:"last_visible_row"`
	HiddenAbove int       `json:"hidden_above"`
	HiddenBelow int       `json:"hidden_below"`
	Rows        []RowInfo `json:"rows"`
}

// SwimInfo mirrors the relationship swimlane board: a 2D matrix rooted at one
// bead. Lanes are relationship kinds; each lane's cells are the related beads
// bucketed by status. Only populated lanes and status columns are present.
type SwimInfo struct {
	Root      string         `json:"root"`
	RootTitle string         `json:"root_title"`
	Focus     string         `json:"focus,omitempty"` // focused card id
	Statuses  []string       `json:"statuses"`        // shown status columns, in order
	Lanes     []SwimLaneInfo `json:"lanes"`
}

type SwimLaneInfo struct {
	Relation string         `json:"relation"` // sub-issues | blocked-by | blocking | siblings
	Total    int            `json:"total"`
	Cells    []SwimCellInfo `json:"cells"`
}

type SwimCellInfo struct {
	Status string    `json:"status"`
	Cards  []RowInfo `json:"cards"`
}

// ColumnsInfo mirrors the Miller-columns navigator: the active drill relation,
// the drilled path (col 0's selection … the focused column's parent), the
// selected bead, and the columns currently on screen (a horizontal window over
// the full stack; older path columns scroll off the left).
type ColumnsInfo struct {
	Relation     string       `json:"relation"` // children | deps
	Path         []string     `json:"path"`     // drilled-into bead ids, root → deepest
	Focus        string       `json:"focus"`    // selected bead id
	TotalColumns int          `json:"total_columns"`
	FirstVisible int          `json:"first_visible_column"` // 1-based
	Columns      []ColumnInfo `json:"columns"`
}

type PanelInfo struct {
	Open        bool       `json:"open"`
	Focused     bool       `json:"focused,omitempty"`
	IssueID     string     `json:"issue_id,omitempty"`
	Meta        string     `json:"meta,omitempty"`
	Labels      []string   `json:"labels,omitempty"`
	Description string     `json:"description,omitempty"` // shown/clipped note, never the text
	Notes       string     `json:"notes,omitempty"`       // present/absent note
	Related     []LinkInfo `json:"related,omitempty"`
}

type LinkInfo struct {
	Kind        string `json:"kind"` // parent | child | needs | blocks
	ID          string `json:"id"`
	Title       string `json:"title"`
	Status      string `json:"status"`
	Highlighted bool   `json:"highlighted,omitempty"`
}
