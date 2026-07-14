package mcpserv

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/awhitty/bb/internal/bd"
)

// --- schema: orient a cold agent in one call ---

func (s *Server) schemaTool(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	prefix := ""
	if list, err := s.client.List(false); err == nil {
		prefix = commonPrefix(list)
	}
	out := map[string]any{
		"overview": "beads (bd) is a lightweight issue tracker with first-class dependencies. " +
			"Issues are chained: a hierarchy (parent/children, e.g. an epic and its sub-issues) and a " +
			"dependency graph (an issue depends-on the issues that must finish first; the reverse is blocked-by).",
		"id_prefix": prefix,
		"fields": map[string]string{
			"id":              "unique id, e.g. " + prefix + "pqr.4",
			"title":           "one-line summary",
			"status":          "open | in_progress | blocked | deferred | closed",
			"priority":        "0-4, 0 = highest (critical), 4 = lowest (backlog)",
			"type":            "bug | feature | task | epic | chore | decision",
			"parent":          "id of the parent issue (hierarchy)",
			"assignee/owner":  "person strings, matched EXACTLY in queries",
			"labels":          "free-form tags",
			"depends_on":      "ids this issue is blocked by (must finish first)",
			"blocked_by_this": "ids that depend on this issue",
			"description":     "full body (markdown)",
			"notes":           "running notes appended over time",
			"comment_count":   "number of comments (fetch with issue(comments:true))",
		},
		"relationships": map[string]string{
			"hierarchy":  "parent ⇄ children. Group work under an epic. Query direct children with parent=<id>.",
			"dependency": "depends_on ⇄ blocked_by. An OPEN issue in a chain's depends_on is what actively blocks. Use graph() to walk it transitively.",
		},
		"query_language": bd.QueryGrammar,
		"tools": map[string]string{
			"schema":         "this — the data model and tool map",
			"search":         "fuzzy full-text recall over title/description/notes/labels",
			"issues":         "runs a bd-language query directly (exact field match); paginated silent read — always returns total_count + truncated, with next_cursor + a hint when more remains",
			"issue":          "one issue in full + its neighbors (add comments:true / history:true)",
			"graph":          "transitive blocker/dependent/hierarchy traversal from an id",
			"view":           "the XML of what the human sees now; each card carries blockers=\"N\" (absent ⇒ ready to work); reports the active sort, any emphasis, and the agent-shares stream (what you've published + whether the human is attached to your channel)",
			"show":           "PUBLISH a named view into the agent-shares stream (query/ids/mode/title, plus optional per-bead remarks); the human pulls it with @ — you do not seize the screen",
			"set_view":       "PUBLISH a named view (mode, filter, sort {key,dir}, collapse, thresholds, title, plus optional per-bead remarks) into the agent-shares stream; the human pulls it with @. mode=relationship + root=<id> = the relationship swimlane board; mode=columns [+ root=<id>] = the Miller-columns navigator (Finder-style drill). remarks = {bead-id: \"why it's here\"} — an ephemeral note shown on the card/panels, never written to the issue",
			"select":         "focus a card in the current view; optional preview panel",
			"emphasize":      "PUBLISH an emphasis view into the agent-shares stream: decorate targets (issue rows / sections) — highlight|marker|outline|spotlight + optional label; the human pulls it with @",
			"clear_emphasis": "drop the emphasis layer (same as the human's esc)",
			"reprioritize":   "set an issue's priority 0-4 — the ONE data mutation you may make (status/close stay in bd)",
			"reset":          "detach the human from your channel + drop emphasis, restoring their own view",
			"refresh":        "reload the board from bd",
		},
		"recipe": "view() cards show blockers=\"N\" — a card with no blockers attribute is READY, one with blockers=\"2\" is waiting on 2 open issues. " +
			"To answer \"what blocks X\": graph(id:X, relation:blockers) → the open nodes are the live blockers; issue(id) returns X plus its grouped neighbors. " +
			"To find by topic when you lack exact values: search(text). You may reprioritize(id, priority) to re-rank work; everything else (claim/close/assign/edit) is done by the human in language with bd.",
		"levers": "You never seize the screen — you PUBLISH into the agent-shares stream and the human pulls with @. " +
			"READ silent (schema/search/issues/issue/graph — research here first); " +
			"PUBLISH a named view (show/set_view/emphasize — leaves a view in the stream; always give it a clear title); " +
			"ACT (reprioritize — the one write); RESTORE (reset detaches the human from your channel).",
		"etiquette": "The human owns their screen; you leave named views in the agent-shares stream and they reach them with @ on their schedule (and can ATTACH to your channel so your pushes drive their board live until they navigate away). " +
			"view() before you act, and name every view clearly so it reads in their list. Say what you shared and why in chat. Don't thrash — one well-named view beats a flurry. The human outranks you by construction.",
	}
	return jsonResult(out), nil
}

// --- graph: transitive traversal ---

type graphNode struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Status   string `json:"status"`
	Type     string `json:"type"`
	Priority int    `json:"priority"`
	Depth    int    `json:"depth"`
	Blocking bool   `json:"blocking,omitempty"` // an open node in a blocker chain
}

type graphEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
}

func (s *Server) graphTool(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id, err := req.RequireString("id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	args := req.GetArguments()
	relation, _ := args["relation"].(string)
	if relation == "" {
		relation = "blockers"
	}
	depth := 20
	if d, ok := args["depth"].(float64); ok && d > 0 {
		depth = int(d)
	}

	all, err := s.client.List(true)
	if err != nil {
		return mcp.NewToolResultError("bd list failed: " + err.Error()), nil
	}
	byID := make(map[string]bd.Issue, len(all))
	for _, is := range all {
		byID[is.ID] = is
	}
	root, ok := byID[id]
	if !ok {
		return mcp.NewToolResultError(id + " is not on the board"), nil
	}

	// adjacency per relation
	var adj func(string) []string
	switch relation {
	case "blockers":
		adj = func(x string) []string { return bd.BlockerIDs(byID[x]) }
	case "dependents":
		rev := map[string][]string{}
		for _, is := range all {
			for _, b := range bd.BlockerIDs(is) {
				rev[b] = append(rev[b], is.ID)
			}
		}
		adj = func(x string) []string { return rev[x] }
	case "hierarchy":
		children := map[string][]string{}
		for _, is := range all {
			if is.Parent != "" {
				children[is.Parent] = append(children[is.Parent], is.ID)
			}
		}
		adj = func(x string) []string {
			out := append([]string(nil), children[x]...)
			if p := byID[x].Parent; p != "" {
				out = append(out, p)
			}
			return out
		}
	default:
		return mcp.NewToolResultError("relation must be blockers | dependents | hierarchy"), nil
	}

	nodes, edges := traverse(id, byID, adj, depth)

	out := map[string]any{
		"root":     graphNode{ID: root.ID, Title: root.Title, Status: root.Status, Type: root.IssueType, Priority: root.Priority},
		"relation": relation,
		"nodes":    nodes,
		"edges":    edges,
		"summary":  graphSummary(relation, id, nodes),
	}
	return jsonResult(out), nil
}

// traverse does a breadth-first walk, returning reached nodes (excluding the
// root) in BFS order with their depth, plus every edge followed.
func traverse(start string, byID map[string]bd.Issue, adj func(string) []string, maxDepth int) ([]graphNode, []graphEdge) {
	type qi struct {
		id    string
		depth int
	}
	nodes := []graphNode{}
	edges := []graphEdge{}
	seen := map[string]bool{start: true}
	queue := []qi{{start, 0}}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if cur.depth >= maxDepth {
			continue
		}
		for _, nb := range adj(cur.id) {
			edges = append(edges, graphEdge{From: cur.id, To: nb})
			if seen[nb] {
				continue
			}
			seen[nb] = true
			is := byID[nb]
			nodes = append(nodes, graphNode{
				ID: nb, Title: is.Title, Status: is.Status, Type: is.IssueType,
				Priority: is.Priority, Depth: cur.depth + 1,
				Blocking: is.Status != bd.StatusClosed,
			})
			queue = append(queue, qi{nb, cur.depth + 1})
		}
	}
	return nodes, edges
}

func graphSummary(relation, id string, nodes []graphNode) string {
	if len(nodes) == 0 {
		switch relation {
		case "blockers":
			return id + " has no blockers — nothing is preventing it."
		case "dependents":
			return "nothing depends on " + id + "."
		default:
			return id + " has no related issues in the hierarchy."
		}
	}
	if relation == "blockers" {
		var open []string
		for _, n := range nodes {
			if n.Blocking {
				open = append(open, n.ID)
			}
		}
		if len(open) == 0 {
			return fmt.Sprintf("%d issue(s) in the blocker chain, all closed — %s is unblocked.", len(nodes), id)
		}
		return fmt.Sprintf("%d issue(s) in the blocker chain; %d still open and actively blocking %s: %s",
			len(nodes), len(open), id, strings.Join(open, ", "))
	}
	return fmt.Sprintf("%d issue(s) reached via %s from %s.", len(nodes), relation, id)
}

// --- search: lexical fuzzy recall ---

func (s *Server) searchTool(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	text, err := req.RequireString("text")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	limit := 20
	if l, ok := req.GetArguments()["limit"].(float64); ok && l > 0 {
		limit = int(l)
	}
	if limit > maxLimit {
		limit = maxLimit
	}
	terms := searchTerms(text)
	if len(terms) == 0 {
		return mcp.NewToolResultError("give at least one search word"), nil
	}
	all, err := s.client.List(true)
	if err != nil {
		return mcp.NewToolResultError("bd list failed: " + err.Error()), nil
	}

	type hit struct {
		is      bd.Issue
		score   int
		snippet string
	}
	var hits []hit
	for _, is := range all {
		title := strings.ToLower(is.Title)
		desc := strings.ToLower(is.Description)
		notes := strings.ToLower(is.Notes)
		labels := strings.ToLower(strings.Join(is.Labels, " "))
		score := 0
		for _, t := range terms {
			if strings.Contains(title, t) {
				score += 3
			}
			if strings.Contains(labels, t) {
				score += 2
			}
			if strings.Contains(desc, t) {
				score++
			}
			if strings.Contains(notes, t) {
				score++
			}
		}
		if score == 0 {
			continue
		}
		hits = append(hits, hit{is: is, score: score, snippet: snippet(is, terms)})
	}
	sort.SliceStable(hits, func(a, b int) bool {
		if hits[a].score != hits[b].score {
			return hits[a].score > hits[b].score
		}
		return hits[a].is.ID < hits[b].is.ID
	})

	total := len(hits)
	if len(hits) > limit {
		hits = hits[:limit]
	}
	items := make([]map[string]any, 0, len(hits))
	for _, h := range hits {
		item := map[string]any{
			"id": h.is.ID, "title": h.is.Title, "status": h.is.Status,
			"type": h.is.IssueType, "priority": h.is.Priority, "score": h.score,
		}
		if h.snippet != "" {
			item["snippet"] = h.snippet
		}
		items = append(items, item)
	}
	out := map[string]any{"query": text, "total": total, "shown": len(items), "results": items}
	return jsonResult(out), nil
}

// searchTerms lowercases and keeps words of length >= 2.
func searchTerms(text string) []string {
	var out []string
	for _, w := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9')
	}) {
		if len(w) >= 2 {
			out = append(out, w)
		}
	}
	return out
}

// snippet returns a short window of description/notes around the first term.
func snippet(is bd.Issue, terms []string) string {
	for _, body := range []string{is.Description, is.Notes} {
		low := strings.ToLower(body)
		for _, t := range terms {
			if i := strings.Index(low, t); i >= 0 {
				start := i - 50
				if start < 0 {
					start = 0
				}
				end := i + len(t) + 70
				if end > len(body) {
					end = len(body)
				}
				seg := strings.TrimSpace(body[start:end])
				if start > 0 {
					seg = "…" + seg
				}
				if end < len(body) {
					seg += "…"
				}
				return strings.Join(strings.Fields(seg), " ")
			}
		}
	}
	return ""
}

// commonPrefix returns the most common "<prefix>-" of the board's ids.
func commonPrefix(list []bd.Issue) string {
	counts := map[string]int{}
	for _, is := range list {
		if i := strings.Index(is.ID, "-"); i > 0 {
			counts[is.ID[:i+1]]++
		}
	}
	best, bestN := "", 0
	for p, n := range counts {
		if n > bestN {
			best, bestN = p, n
		}
	}
	return best
}

// neighbors builds issue()'s enriched relation groups: each relation carries
// {id, title, status}; blockers add a "blocking" flag (still open) so the
// agent can reason about the neighborhood in one call.
func neighbors(is bd.Issue, all []bd.Issue, dependents []string) map[string]any {
	byID := make(map[string]bd.Issue, len(all))
	for _, x := range all {
		byID[x.ID] = x
	}
	brief := func(id string) map[string]any {
		x := byID[id]
		return map[string]any{"id": id, "title": x.Title, "status": x.Status}
	}
	nb := map[string]any{}
	if _, ok := byID[is.Parent]; ok {
		nb["parent"] = brief(is.Parent)
	}
	var children, siblings, blockedBy, blocks []map[string]any
	for _, x := range all {
		if x.Parent == is.ID {
			children = append(children, brief(x.ID))
		}
		if is.Parent != "" && x.Parent == is.Parent && x.ID != is.ID {
			siblings = append(siblings, brief(x.ID))
		}
	}
	for _, bid := range bd.BlockerIDs(is) {
		if x, ok := byID[bid]; ok {
			b := brief(bid)
			b["blocking"] = x.Status != bd.StatusClosed
			blockedBy = append(blockedBy, b)
		}
	}
	for _, d := range dependents {
		blocks = append(blocks, brief(d))
	}
	if len(children) > 0 {
		nb["children"] = children
	}
	if len(siblings) > 0 {
		nb["siblings"] = siblings
	}
	if len(blockedBy) > 0 {
		nb["blocked_by"] = blockedBy
	}
	if len(blocks) > 0 {
		nb["blocks"] = blocks
	}
	return nb
}

func jsonResult(out map[string]any) *mcp.CallToolResult {
	raw, _ := json.MarshalIndent(out, "", " ")
	res := mcp.NewToolResultText(string(raw))
	res.StructuredContent = out
	return res
}
