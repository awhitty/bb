package ui

import (
	"regexp"

	"github.com/awhitty/bb/internal/bd"
)

// subtree.go resolves the bb `subtree=<id>` pseudo-query: an epic <id>
// together with its whole descendant subtree. bd cannot express this — its
// query engine rejects `parent=<id> OR id=<id>` (parent supports = only, and
// not inside an OR) — so bb resolves it in-process against the full
// board. The NL compiler emits `subtree=<id>` for "everything under X / X's
// work"; a plain `parent=<id>` (direct children only) stays a normal bd query.

// subtreeRe matches a standalone `subtree=<id>` query (the whole string, so a
// combined `subtree=X AND …` is NOT treated as a subtree — it falls through to
// bd, which rejects it, and the guard surfaces that cleanly).
var subtreeRe = regexp.MustCompile(`^\s*subtree\s*=\s*(\S+)\s*$`)

// subtreeRoot returns the root id of a `subtree=<id>` query, else ("", false).
func subtreeRoot(q string) (string, bool) {
	m := subtreeRe.FindStringSubmatch(q)
	if m == nil {
		return "", false
	}
	return m[1], true
}

// subtreeIssues returns root plus every transitive descendant present in the
// board, preserving the board's order. Resolving against the FULL board (the
// caller passes bd's --all list) keeps closed descendants in view and gives
// applied shares the same id-filter behavior.
func subtreeIssues(board []bd.Issue, root string) []bd.Issue {
	children := map[string][]string{}
	for _, is := range board {
		if is.Parent != "" {
			children[is.Parent] = append(children[is.Parent], is.ID)
		}
	}
	want := map[string]bool{}
	queue := []string{root}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		if want[id] {
			continue
		}
		want[id] = true
		queue = append(queue, children[id]...)
	}
	out := make([]bd.Issue, 0, len(want))
	for _, is := range board {
		if want[is.ID] {
			out = append(out, is)
		}
	}
	return out
}

// boardQuery runs a compiled/typed query against bd, transparently resolving
// the `subtree=<id>` pseudo-query in-process. It returns the matched issues and
// (for the subtree case) the full board it resolved against, so loadIssues
// reuses that one List instead of fetching the graph a second time. For a
// normal query graph is nil and the caller loads the graph as usual.
func boardQuery(client *bd.Client, q string) (issues, graph []bd.Issue, err error) {
	if root, ok := subtreeRoot(q); ok {
		full, ferr := client.List(true)
		if ferr != nil {
			return nil, nil, ferr
		}
		return subtreeIssues(full, root), full, nil
	}
	issues, err = client.Query(q)
	return issues, nil, err
}
