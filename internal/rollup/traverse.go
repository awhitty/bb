package rollup

// This is the relation-traversal engine: a single downward-from-roots forest
// walk that produces the box-drawing connector rows the tree view (tree.go) and
// the octopus panel body (octopus.go) each duplicated by hand. It is pure —
// it takes an edge function over ids, not a Model — so it composes with any
// relation (parent-child, dependency) and any id index.
//
// Cycle handling: a spanning forest built by BuildTree / BuildDepsForest is
// already acyclic, so the cycle stub never fires for the tree port. It exists
// for callers that hand Traverse a raw (possibly cyclic) edge function: an id
// that repeats on the CURRENT root-to-node path renders as a ↻ leaf and the
// walk stops there, so a self-edge or a mutual cycle can't loop forever.

// ForestRow is one emitted row of a Traverse: the id, its box-drawing connector
// prefix (├─ / └─ with the │ / space guides above it), its nesting depth
// (0 = root), and whether it is a cycle stub (a repeat of an ancestor on the
// current path, rendered as a ↻ leaf).
type ForestRow struct {
	ID     string
	Prefix string
	Depth  int
	Cycle  bool
}

// Forest is the flattened pre-order sequence of rows Traverse emits.
type Forest []ForestRow

// TraverseOpts tunes the walk.
type TraverseOpts struct {
	// MaxDepth caps how deep the walk descends. 0 means unlimited. A row at
	// depth == MaxDepth is emitted but not expanded (its children are dropped).
	MaxDepth int
}

// Traverse flattens the downward forest reachable from roots via edgeFn into a
// pre-order row sequence, stamping each row with the same ├─/└─ connector run
// the tree and octopus walks built inline. edgeFn returns a node's children in
// the order they should render; return nil to make a node a leaf (this is how a
// caller folds a collapsed subtree). A node whose id already appears on the
// current path is emitted as a cycle stub (Cycle=true) and not expanded.
func Traverse(roots []string, edgeFn func(id string) []string, opts TraverseOpts) Forest {
	var rows Forest
	var walk func(id, ancestors string, last bool, depth int, path map[string]bool)
	walk = func(id, ancestors string, last bool, depth int, path map[string]bool) {
		prefix := ancestors
		if depth > 0 {
			if last {
				prefix += "└─ "
			} else {
				prefix += "├─ "
			}
		}
		if path[id] {
			rows = append(rows, ForestRow{ID: id, Prefix: prefix, Depth: depth, Cycle: true})
			return
		}
		rows = append(rows, ForestRow{ID: id, Prefix: prefix, Depth: depth})
		if opts.MaxDepth > 0 && depth >= opts.MaxDepth {
			return
		}
		path[id] = true
		childAnc := ancestors
		if depth > 0 {
			if last {
				childAnc += "   "
			} else {
				childAnc += "│  "
			}
		}
		kids := edgeFn(id)
		for i, c := range kids {
			walk(c, childAnc, i == len(kids)-1, depth+1, path)
		}
		delete(path, id)
	}
	for i, r := range roots {
		walk(r, "", i == len(roots)-1, 0, map[string]bool{})
	}
	return rows
}
