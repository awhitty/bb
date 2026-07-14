# internal/ui — architecture

How the TUI is layered, and the patterns every change follows. Read this before
adding a view or reaching for a new abstraction.

## Layers (each depends only on the ones above it)

1. **Domain** — `internal/bd`, `internal/rollup`, `internal/nlq`, `internal/mcpserv`.
   Pure data + logic: the bd CLI client, the rollup grouping, NL→query, the MCP
   surface. Knows nothing about rendering.
2. **Layout engine** — `internal/ui/layout`. Pure geometry (ints only): `Compute`
   returns named rects (`Header/Body/Main/Panel/Footer`) whose heights sum to the
   terminal height BY CONSTRUCTION, and `Window` is the page-jump scroll math.
   This is the single source of truth for sizes — nothing else re-derives the
   height/width budget. It is why frames can't overflow.
3. **Row formatter** — `row.go`. The ONE way to render an issue row: fixed-width
   lipgloss column cells (`priorityCell`/`statusCell`/`titleCell`/`ageCell` +
   `gapCell`) composed with `JoinHorizontal`. Every list/board/tree row aligns
   identically because they all go through these cells.
4. **View components** — `board.go`, `list.go`, `tree.go`, `swim.go`,
   `columns.go`, `panel.go`, and `detail.go`. Each renders one recurring surface into a rect using
   the layout engine + row formatter. They own their own content; they do not
   re-implement geometry.
5. **Root Model** — `model.go` (state and message updates), `input.go` (key routing),
   and `view.go` (composition). The Model is a **router/composer**: it holds state,
   routes each keypress by focus (prompt / panel / detail / board), and composes
   the view components into the layout's rects. It must NOT re-implement geometry
   or per-view navigation — that belongs in the layout engine and the components.

## The pattern

Each recurring interaction is one small, composable piece built on the layout
engine + row formatter. A **new view = compose existing pieces**, never
hand-roll windowing/geometry/nav:

- Need a scrolling, focus-tracking list? Use `renderNavList` (the `navlist`
  component below) with a `row(i, focused)` closure and `formatRow` for the
  rows — it owns the page-jump window (`layout.Window`) and overflow lines.
- Need to fit a region? Take a rect from `m.layoutScreen()`; never recompute
  `height-2`.
- Need aligned columns? Use the `row.go` cells; never hand-pad with spaces.

## The anti-pattern — do NOT build a framework

Bubbletea is deliberately un-frameworky, and Go generics get ugly fast. Add an
abstraction ONLY when it removes more duplication than the plumbing it adds. The
goal is a handful of tasteful, named pieces — NOT a widget-tree system with
lifecycle hooks, a component registry, or a reactive-binding layer. When in
doubt, inline it; extract only the third time you copy it.

## Components

Built:

- **layout engine** (`layout.Compute` / `layout.Window`) — geometry + windowing.
- **row formatter** (`row.go`) — the aligned issue-row cells.
- **sectioned list** (`list.go`) — the default full-width grouped list.
- **tree** (`tree.go`) — the nested outline with right-pinned columns.
- **relationship swimlane** (`swim.go`) — one issue's children, blockers,
  dependents, and siblings grouped by status.
- **columns navigator** (`columns.go`) — hierarchy navigation composed from the
  whole board.
- **tabbed detail / preview panel** (`detail.go` / `panel.go`) — header + tab
  strip + glamour body; both render the same sub-tab content builders.
- **navlist** (`navlist.go`) — `renderNavList`: the ONE page-jump windowed-list
  renderer. Windows N items into a `bodyH`-tall region with the reserved top/
  bottom overflow lines (always present, so a jump changes line CONTENT not
  COUNT), a per-item `row(i, focused)` closure, and injected overflow-line
  builders. The sectioned list, the tree, and the activity feed are instances
  (VHS-proven pixel-identical: `list-*`/`tree-*` byte-identical before/after).
  It is one function taking a renderer, NOT a widget framework. The octopus and
  panel-related links were deliberately NOT folded in: they are section-composed
  and select over a flat id list without a page-jump window, so forcing them
  through this renderer would change behavior and add framework plumbing — the
  guardrail below wins. The window math itself stays in `layout.Window`.
- **declarative keybindings** (`keys.go`) — one `bubbles/key` registry grouped
  by category is the single source of truth for input dispatch (discrete board
  actions match via `key.Matches`; contextual movement — `h/l/j/k`, `g/G`, and
  directional pairs — stays on raw keys, its keys still sourced from the
  registry for display), the condensed footer (`bubbles/help` short view), and
  the full help overlay (`?`). The overlay is generated from the registry
  (`renderHelpContent` in `view.go`) so footer/overlay/dispatch can never drift;
  it renders into the layout's body rect via a viewport, so a long grid scrolls
  rather than overflowing the terminal.

Planned (extract when the win is clear; do not pre-build):

- **`pane` primitive** — header + scroll-body + footer composed into a rect, that
  detail / preview / neighborhood share.
- **`prompt` flow** — one component unifying `/`, `!`, `i` (the textinput
  prompts) instead of the per-kind branches in `input.go`.

## Visual verification

Changes to any view are verified by eye via the VHS harness, not tmux ASCII: run
`scripts/shots.sh` to render every view to a PNG in `vhs/out/` against the
deterministic fixture (`vhs/fixture/board.jsonl`), then read the PNGs. A refactor
that should not change pixels (e.g. the `navlist` extraction) is proven by
regenerating the shots and confirming they're identical.
