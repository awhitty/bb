package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/awhitty/bb/internal/bd"
)

// The top-level octopus VIEW is retired: the `6` key and the m-cycle step were
// removed (the Miller-columns navigator (C, columns.go) replaces its drill; the
// swimlane (R, swim.go) replaces its snapshot). What survives here is octopusBody
// — the compact, bidirectional, SELECTABLE relationship body the preview panel /
// detail Related tab renders (panel.go / input.go): ANCESTORS ↑ (what a bead
// depends on / its parent lineage) above, DESCENDANTS ↓ (what depends on it / its
// child subtree) below, returning the flat id list that drives the panel's link
// navigation. Its traversal follows BOTH dependency and hierarchy edges and is
// cycle-safe (a node reached twice renders as a leaf marked ↻). Tracked
// A future pass could replace octopusBody with a connector-style, selectable,
// bidirectional panel body, then delete this file — the Stage-4 downward-only
// rollup.Traverse is not a clean subset of this UP+DOWN, id-returning shape.

type octoDir int

const (
	octoUp   octoDir = iota // ancestors: blockers / parent
	octoDown                // descendants: dependents / children
)

type octoRow struct {
	issue     bd.Issue
	prefix    string // box-drawing connectors
	depth     int
	cyclic    bool // revisited node — a cycle guard leaf
	collapsed bool // its subtree is folded away (z)
	hidden    int  // descendants hidden while collapsed
}

// octoAdj returns the neighbors to follow from id in a direction — BOTH
// dependency and hierarchy edges, deduped and filtered to the loaded board.
func (m Model) octoAdj(id string, dir octoDir) []string {
	var out []string
	seen := map[string]bool{}
	add := func(ids ...string) {
		for _, x := range ids {
			if x == "" || seen[x] {
				continue
			}
			if _, ok := m.byID[x]; ok {
				seen[x] = true
				out = append(out, x)
			}
		}
	}
	if dir == octoUp {
		add(m.byID[id].Parent)
		add(bd.BlockerIDs(m.byID[id])...)
	} else {
		add(m.childrenOf[id]...)
		add(m.revDeps[id]...)
	}
	return out
}

// octoBuild walks a direction into a deep connector tree (spanning tree; a node
// seen before renders as a ↻ leaf). rootID's own row is NOT included.
func (m Model) octoBuild(rootID string, dir octoDir) []octoRow {
	var rows []octoRow
	seen := map[string]bool{rootID: true}
	var walk func(id, ancestors string, last bool, depth int)
	walk = func(id, ancestors string, last bool, depth int) {
		prefix := ancestors
		if depth > 0 {
			if last {
				prefix += "└─ "
			} else {
				prefix += "├─ "
			}
		}
		if seen[id] {
			rows = append(rows, octoRow{issue: m.byID[id], prefix: prefix, depth: depth, cyclic: true})
			return
		}
		seen[id] = true
		kids := m.octoAdj(id, dir)
		if m.collapsed[id] && len(kids) > 0 {
			rows = append(rows, octoRow{issue: m.byID[id], prefix: prefix, depth: depth,
				collapsed: true, hidden: len(kids)})
			return // subtree folded
		}
		rows = append(rows, octoRow{issue: m.byID[id], prefix: prefix, depth: depth})
		childAnc := ancestors
		if depth > 0 {
			if last {
				childAnc += "   "
			} else {
				childAnc += "│  "
			}
		}
		for i, c := range kids {
			walk(c, childAnc, i == len(kids)-1, depth+1)
		}
	}
	top := m.octoAdj(rootID, dir)
	for i, c := range top {
		walk(c, "", i == len(top)-1, 0)
	}
	return rows
}

// octoFlat is every navigable node id in display order (ancestors, focal,
// descendants) — what j/k steps through and enter re-roots on.
func (m Model) octoFlat(rootID string) []string {
	var ids []string
	for _, r := range m.octoBuild(rootID, octoUp) {
		if !r.cyclic {
			ids = append(ids, r.issue.ID)
		}
	}
	ids = append(ids, rootID)
	for _, r := range m.octoBuild(rootID, octoDown) {
		if !r.cyclic {
			ids = append(ids, r.issue.ID)
		}
	}
	return ids
}

// octoLine renders one octopus node: gutter · connectors · short id · title
// (flex) · right-aligned status.
func (m Model) octoLine(r octoRow, focused bool, w int) string {
	id := m.shortID(r.issue.ID)
	statusW := statusColW
	status := statusStyle(r.issue.Status, false).Width(statusW).Align(lipgloss.Right).Render(statusGlyph(r.issue.Status))
	prefixW := ansi.StringWidth(r.prefix)
	idCell := styDim.Render(padRight(id, 9))
	suffix := ""
	if r.cyclic {
		suffix = styDim.Render(" ↻")
	} else if r.collapsed {
		suffix = styDim.Render(fmt.Sprintf(" ▸ +%d", r.hidden))
	}
	ind := relIndicator(m.relFlagsFor(r.issue))
	titleW := w - focusGutter - prefixW - 9 - 1 - relSlot - 1 - statusW - 1 - ansi.StringWidth(suffix)
	if titleW < 4 {
		return gutter(focused) + ansi.Truncate(r.prefix+id, max(1, w-focusGutter), "…")
	}
	title := titleCell(r.issue.Title, titleW, focused)
	return gutterEmph(focused, m.issueEmph(r.issue.ID)) + styDim.Render(r.prefix) + idCell + " " + ind + " " + title + suffix + " " + status
}

// octopusBody renders the octopus for the detail Related tab / panel: the two
// sections without the top-level chrome. Returns the body and the flat id list.
func (m Model) octopusBody(rootID string, width, selIdx int) (string, []string) {
	flat := m.octoFlat(rootID)
	selID := ""
	if selIdx >= 0 && selIdx < len(flat) {
		selID = flat[selIdx]
	}
	var lines []string
	up := m.octoBuild(rootID, octoUp)
	if len(up) > 0 {
		lines = append(lines, styBold.Render("ancestors ↑"))
		for _, r := range up {
			lines = append(lines, m.octoLine(r, r.issue.ID == selID && !r.cyclic, width))
		}
	}
	down := m.octoBuild(rootID, octoDown)
	if len(down) > 0 {
		if len(up) > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, styBold.Render("descendants ↓"))
		for _, r := range down {
			lines = append(lines, m.octoLine(r, r.issue.ID == selID && !r.cyclic, width))
		}
	}
	if len(lines) == 0 {
		lines = append(lines, styDim.Render("(no relationships)"))
	}
	return strings.Join(lines, "\n"), flat
}
