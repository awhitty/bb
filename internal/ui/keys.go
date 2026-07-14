package ui

import "github.com/charmbracelet/bubbles/key"

// keys.go is the ONE keybinding registry. Every binding is a key.Binding
// carrying its keys + help text, grouped into categories. This registry is the
// single source of truth for three things, so they can never drift:
//   - input dispatch (input.go matches keypresses against these bindings),
//   - the always-visible condensed footer (bubbles/help short view),
//   - the full help overlay (`?`, grouped by category — view.go renders it).
//
// Contextual movement (h/l/j/k, enter, space, tab, esc) is matched on the raw
// key string in input.go because the SAME key means different things per view
// (list vs tree vs board vs swimlane vs columns); its keys still live here for
// display.

// keyCategory is one labeled group in the help overlay.
type keyCategory struct {
	name     string
	bindings []key.Binding
}

// keyMap holds every board-level binding. Fields are grouped by the category
// they belong to; categories() wires them into the overlay's groups.
type keyMap struct {
	// Move
	Move      key.Binding
	TopBottom key.Binding
	History   key.Binding
	// Open
	Open       key.Binding
	Preview    key.Binding
	FocusPanel key.Binding
	Jump       key.Binding
	// Views
	View       key.Binding
	Rollup     key.Binding
	Board      key.Binding
	Swimlane   key.Binding
	Columns    key.Binding
	Activity   key.Binding
	AgentShare key.Binding
	// Shape
	Sort          key.Binding
	Group         key.Binding
	Lanes         key.Binding
	TreeExpand    key.Binding
	Fold          key.Binding
	FoldAll       key.Binding
	SubtreeFilter key.Binding
	EdgeNest      key.Binding
	Closed        key.Binding
	Age           key.Binding
	// Act & Query
	Priority key.Binding
	Query    key.Binding
	Analyst  key.Binding
	Refresh  key.Binding
	// System
	Help  key.Binding
	Theme key.Binding
	Quit  key.Binding
	Back  key.Binding
}

func defaultKeyMap() keyMap {
	return keyMap{
		// Move
		Move:      key.NewBinding(key.WithKeys("h", "j", "k", "l", "up", "down", "left", "right"), key.WithHelp("hjkl", "move")),
		TopBottom: key.NewBinding(key.WithKeys("g", "G"), key.WithHelp("g/G", "top/bottom")),
		History:   key.NewBinding(key.WithKeys("[", "]"), key.WithHelp("[ ]", "history back/fwd")),
		// Open
		Open:       key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "open card")),
		Preview:    key.NewBinding(key.WithKeys(" "), key.WithHelp("space", "preview panel")),
		FocusPanel: key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "focus panel")),
		Jump:       key.NewBinding(key.WithKeys("i"), key.WithHelp("i", "jump to id")),
		// Views
		View:       key.NewBinding(key.WithKeys("v"), key.WithHelp("v", "cycle list/board/tree")),
		Rollup:     key.NewBinding(key.WithKeys("m", "1", "2", "3", "4", "5"), key.WithHelp("m/1-5", "grouping")),
		Board:      key.NewBinding(key.WithKeys("b"), key.WithHelp("b", "single-column list")),
		Swimlane:   key.NewBinding(key.WithKeys("R"), key.WithHelp("R", "relationship board (rooted here)")),
		Columns:    key.NewBinding(key.WithKeys("C"), key.WithHelp("C", "column navigator (Finder-style drill)")),
		Activity:   key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "activity feed")),
		AgentShare: key.NewBinding(key.WithKeys("@"), key.WithHelp("@", "live sessions (browse + attach)")),
		// Shape
		Sort:          key.NewBinding(key.WithKeys("S"), key.WithHelp("S", "sort key")),
		Group:         key.NewBinding(key.WithKeys("M"), key.WithHelp("M", "group by (menu)")),
		Lanes:         key.NewBinding(key.WithKeys("L"), key.WithHelp("L", "break-out lanes (kanban)")),
		TreeExpand:    key.NewBinding(key.WithKeys("h", "l", "left", "right"), key.WithHelp("←/→", "tree: collapse/expand")),
		Fold:          key.NewBinding(key.WithKeys("z"), key.WithHelp("z", "fold subtree")),
		FoldAll:       key.NewBinding(key.WithKeys("Z"), key.WithHelp("Z", "fold/expand all")),
		SubtreeFilter: key.NewBinding(key.WithKeys("F"), key.WithHelp("F", "min subtree size")),
		EdgeNest:      key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "traverse (relation·direction)")),
		Closed:        key.NewBinding(key.WithKeys("c"), key.WithHelp("c", "toggle closed")),
		Age:           key.NewBinding(key.WithKeys("t"), key.WithHelp("t", "age column: activity ⇄ time-in-status")),
		// Act & Query
		Priority: key.NewBinding(key.WithKeys("+", "=", "-", "_"), key.WithHelp("+/-", "priority")),
		Query:    key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "filter or ask")),
		Analyst:  key.NewBinding(key.WithKeys("!"), key.WithHelp("!", "analyst")),
		Refresh:  key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "refresh")),
		// System
		Help:  key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
		Theme: key.NewBinding(key.WithKeys("T"), key.WithHelp("T", "theme (dark/light)")),
		Quit:  key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit")),
		Back:  key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back/clear")),
	}
}

// categories groups the bindings for the full help overlay. Order is the
// reading order the overlay lays out.
func (k keyMap) categories() []keyCategory {
	return []keyCategory{
		{"Move", []key.Binding{k.Move, k.TopBottom, k.History}},
		{"Open", []key.Binding{k.Open, k.Preview, k.FocusPanel, k.Jump}},
		{"Views", []key.Binding{k.View, k.Rollup, k.Board, k.Swimlane, k.Columns, k.Activity, k.AgentShare}},
		{"Shape", []key.Binding{k.Sort, k.Group, k.Lanes, k.TreeExpand, k.Fold, k.FoldAll, k.SubtreeFilter, k.EdgeNest, k.Closed, k.Age}},
		{"Act & Query", []key.Binding{k.Priority, k.Query, k.Analyst, k.Refresh}},
		{"System", []key.Binding{k.Help, k.Theme, k.Quit, k.Back}},
	}
}

// ShortHelp is the condensed footer line (bubbles/help truncates it to width).
// A curated subset of the registry — the daily verbs, ending with `? help` so
// the full overlay is always one keypress away and stays visible on narrow
// terminals.
func (k keyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Move, k.Open, k.Rollup, k.Query, k.Priority, k.Help, k.Quit}
}

// FullHelp returns the categories as columns (used by bubbles/help to lay out
// each category's key/desc pairs; the overlay adds the headers).
func (k keyMap) FullHelp() [][]key.Binding {
	cats := k.categories()
	groups := make([][]key.Binding, len(cats))
	for i, c := range cats {
		groups[i] = c.bindings
	}
	return groups
}

// detailKeys is the help line while the detail view is open.
type detailKeyMap struct {
	Tabs     key.Binding
	Scroll   key.Binding
	Priority key.Binding
	Comment  key.Binding
	Swimlane key.Binding
	Theme    key.Binding
	Back     key.Binding
}

func defaultDetailKeyMap() detailKeyMap {
	return detailKeyMap{
		Tabs:     key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "overview·history·relationships·comments")),
		Scroll:   key.NewBinding(key.WithKeys("j", "k"), key.WithHelp("j/k", "scroll")),
		Priority: key.NewBinding(key.WithKeys("+", "-"), key.WithHelp("+/-", "priority")),
		Comment:  key.NewBinding(key.WithKeys("c"), key.WithHelp("c", "comment")),
		Swimlane: key.NewBinding(key.WithKeys("R"), key.WithHelp("R", "relationship board")),
		Theme:    key.NewBinding(key.WithKeys("T"), key.WithHelp("T", "theme (dark/light)")),
		Back:     key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back")),
	}
}

func (k detailKeyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Tabs, k.Scroll, k.Priority, k.Comment, k.Swimlane, k.Theme, k.Back}
}

func (k detailKeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{k.ShortHelp()}
}
