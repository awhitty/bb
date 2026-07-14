package nlq

import (
	"fmt"
	"strings"
)

// Mapping is a resolved likely value for a word in the ask — a lexical guess
// that grounds the model toward the workspace's REAL categorical values
// before it compiles. Term is the ask word that matched; Hint is the query
// fragment it likely means.
type Mapping struct {
	Term string
	Hint string
}

// Grounder resolves entity-like words in an ask to the workspace's real
// categorical values, returned as likely mappings injected into the compile
// prompt. This is the single biggest lever in the closest published analog
// (Jackal: categorical accuracy 48.7→71.7; see docs/nlq-sota-2026.md §1).
// Lexical today; an embedding resolver (for example Qwen3-Embedding-0.6B)
// can replace it behind this interface with no caller change.
type Grounder interface {
	Ground(nl string, v Vocab) []Mapping
}

// LexicalGrounder fuzzy-matches ask tokens against the value space by exact,
// prefix, and singular/plural token overlap — no model, no download. At our
// tiny value space (dozens of labels/epics/assignees) this is the no-cost v1
// the research ranks second only after the repair loop.
type LexicalGrounder struct{}

const maxMappings = 6

// Ground returns the likely value mappings for one ask.
func (LexicalGrounder) Ground(nl string, v Vocab) []Mapping {
	toks := contentTokens(nl)
	if len(toks) == 0 {
		return nil
	}
	var out []Mapping
	seen := map[string]bool{}
	add := func(term, hint string) {
		if hint == "" || seen[hint] {
			return
		}
		seen[hint] = true
		out = append(out, Mapping{Term: term, Hint: hint})
	}

	// Types: bug/bugs → type=bug.
	for _, t := range v.Types {
		for _, tk := range toks {
			if tk == t || singular(tk) == t {
				add(tk, "type="+t)
				break
			}
		}
	}
	// Assignees: any name-part match → the EXACT stored full name.
	for _, a := range v.Assignees {
		parts := strings.Fields(strings.ToLower(a))
		for _, tk := range toks {
			for _, pt := range parts {
				if exactOrPrefix(tk, pt) {
					add(tk, "assignee="+quoteIfSpaced(a))
					break
				}
			}
		}
	}
	// Labels: token vs label name (labels render as "name(count)").
	for _, lc := range v.Labels {
		name := strings.SplitN(lc, "(", 2)[0]
		for _, tk := range toks {
			if labelMatch(tk, strings.ToLower(name)) {
				add(tk, "label="+name)
				break
			}
		}
	}
	// Epics: a title-word match → parent=<id> (direct children).
	for _, e := range v.Epics {
		id, title, ok := strings.Cut(e, " ")
		if !ok {
			continue
		}
		titleToks := contentTokens(title)
	scan:
		for _, tk := range toks {
			if len(tk) < 4 {
				continue
			}
			for _, wt := range titleToks {
				if exactOrPrefix(tk, wt) {
					add(tk, fmt.Sprintf("parent=%s (epic %q)", id, title))
					break scan
				}
			}
		}
	}

	if len(out) > maxMappings {
		out = out[:maxMappings]
	}
	return out
}

// renderMappings is the prompt block; the vocabulary stays authoritative.
func renderMappings(ms []Mapping) string {
	if len(ms) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("likely value mappings for THIS ask (use if they fit; the vocabulary above is authoritative):")
	for _, m := range ms {
		fmt.Fprintf(&b, "\n- %q → %s", m.Term, m.Hint)
	}
	return b.String()
}

// contentTokens splits an ask into meaningful lowercase words: punctuation
// stripped, stopwords and short tokens dropped. Stopwords keep generic words
// ("stuff", "work", "problems") from false-matching a label or epic title.
func contentTokens(s string) []string {
	var out []string
	for _, raw := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9')
	}) {
		if len(raw) < 3 || stopwords[raw] {
			continue
		}
		out = append(out, raw)
	}
	return out
}

// exactOrPrefix matches identical words, or a shared prefix once both are
// long enough to make a prefix meaningful (avoids "in" ~ "inference").
func exactOrPrefix(a, b string) bool {
	if a == b {
		return true
	}
	if len(a) < 4 || len(b) < 4 {
		return false
	}
	return strings.HasPrefix(a, b) || strings.HasPrefix(b, a)
}

// labelMatch handles hyphenated/spaced label names (e.g. "critical-path").
func labelMatch(tk, name string) bool {
	if exactOrPrefix(tk, name) {
		return true
	}
	for _, part := range strings.FieldsFunc(name, func(r rune) bool { return r == '-' || r == ' ' || r == '_' }) {
		if exactOrPrefix(tk, part) {
			return true
		}
	}
	return false
}

// singular strips a trailing plural "s" (bugs→bug, features→feature).
func singular(w string) string {
	if len(w) > 3 && strings.HasSuffix(w, "s") {
		return w[:len(w)-1]
	}
	return w
}

func quoteIfSpaced(s string) string {
	if strings.ContainsRune(s, ' ') {
		return `"` + s + `"`
	}
	return s
}

// stopwords drops filler + generic issue-domain words that carry no value
// signal, so grounding only fires on real entity words.
var stopwords = map[string]bool{
	"the": true, "and": true, "for": true, "are": true, "was": true, "were": true,
	"not": true, "any": true, "all": true, "some": true, "every": true, "everything": true,
	"anything": true, "something": true, "stuff": true, "thing": true, "things": true,
	"about": true, "around": true, "regarding": true, "related": true, "with": true,
	"without": true, "show": true, "find": true, "list": true, "get": true, "give": true,
	"tell": true, "what": true, "which": true, "who": true, "whose": true, "when": true,
	"where": true, "why": true, "how": true, "this": true, "that": true, "these": true,
	"those": true, "work": true, "working": true, "done": true, "shipped": true,
	"ship": true, "has": true, "have": true, "had": true, "does": true, "did": true,
	"can": true, "could": true, "should": true, "would": true, "will": true,
	"now": true, "today": true, "week": true, "month": true, "year": true,
	"recent": true, "recently": true, "latest": true, "new": true, "old": true,
	"out": true, "over": true, "under": true, "into": true, "from": true, "off": true,
	"problems": true, "problem": true, "issues": true, "issue": true, "items": true,
	"item": true, "also": true, "just": true, "really": true, "very": true, "more": true,
	"most": true, "less": true, "few": true, "many": true, "much": true, "our": true,
	"their": true, "your": true, "his": true, "her": true, "its": true,
}
