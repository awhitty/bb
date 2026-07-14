// Package nlq is the natural-language → bd query compiler, prototype path.
//
// v0 runs a stock local model behind any OpenAI-compatible server —
// rapid-mlx / omlx / vllm-mlx on Apple Silicon (e.g.
// `rapid-mlx serve mlx-community/Qwen3-1.7B-4bit --port 8000`).
// Every compile and the user's verdict is appended to a JSONL feedback log —
// that log IS the future training set for the distilled tiny model
// for a future distilled model.
package nlq

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/awhitty/bb/internal/discover"
)

// Result pairs the user's natural-language ask with the compiled query, plus
// what pre-executing it returned (the repair loop runs every compile before
// the user sees it) and the repair trail.
type Result struct {
	NL    string
	Query string
	// Count is the number of rows the final query returned when pre-executed;
	// -1 means it did not run (parse error) or is unknown.
	Count    int
	Attempts []Attempt
}

// Repairs is how many revision attempts followed the first compile.
func (r Result) Repairs() int {
	if len(r.Attempts) == 0 {
		return 0
	}
	return len(r.Attempts) - 1
}

// Attempt is one compile→execute cycle in the repair loop: the query it
// produced, what executing it returned, and the trigger (if any) that a
// revision followed.
type Attempt struct {
	Query   string
	Count   int
	Err     string // execution error, empty if it ran
	Trigger string // "" | parse-error | zero-rows | too-few | no-query
}

// QueryExecutor runs a compiled query and reports the row count plus 2–3
// sample rows ("id title") — the execution-feedback signal the repair loop
// revises against. Injected by the caller (backed by the bd client) so this
// package stays store-agnostic.
type QueryExecutor func(query string) (count int, sample []string, err error)

// Repair-loop shape (docs/nlq-sota-2026.md §1.1): at most 2 revisions (round
// 3+ is noise at small scale), a small temperature bump to escape the
// pattern-lock, and "few rows" = under 3 for a plural/topical ask.
const (
	maxRepairs = 2
	fewRows    = 3
	repairTemp = 0.3
)

const grammarHelp = `bd query language:
  field=value  field!=value  field>value  field>=value  field<value  field<=value
  AND OR NOT ( )   — case-insensitive booleans
fields: status(open|in_progress|blocked|deferred|closed), priority(0-4), type(bug|feature|task|epic|chore|decision),
  assignee, owner, label, title, description, notes, created, updated, started, closed, id, parent, pinned
title/description/notes match by substring. dates accept YYYY-MM-DD and QUOTED relative forms like "-7d".
parent=<id> matches DIRECT children only; it supports = ONLY (no !=, and parent may not be OR-ed). To ask for
an epic TOGETHER with its work, use subtree=<id> (below).
Only the listed field names, subtree, and enum values exist — map topic words to title=<word>, never invent a field or type.
subtree=<id> is a bb form (NOT a bd field): the issue <id> PLUS its whole descendant subtree. Use it ALONE,
never combined with AND/OR/NOT.

mapping rules (a workspace vocabulary block below lists this board's REAL values — map to them exactly):
- assignee/owner: EXACT match, never substring. Use the full stored string from the vocabulary, double-quoted
  when it contains a space. A first or partial name means the matching full assignee.
- topic word matching an epic/root title in the vocabulary → that epic's id. "everything under X", "X's work",
  "the X epic/area", or a bare topic → subtree=<id> (the epic node AND its whole subtree). Only "children of X" /
  "sub-issues of X" / "direct children" mean parent=<id> ALONE. Always prefer an epic-id mapping over
  title=<word> when an epic title matches.
- topic word matching a label in the vocabulary → label=<that label>.
- no epic/label/assignee match → title=<word> (substring). One concept per clause; AND clauses only when the
  ask names two independent constraints. Never copy ids or names from the examples below — only from the vocabulary.`

// Few-shot examples are board-AGNOSTIC (the vocabulary block supplies the
// board's real ids/names); the mapping rules forbid copying these literals.
var fewShot = [][2]string{
	{"open bugs", "status=open AND type=bug"},
	{"high priority work that isn't closed", "priority<=1 AND status!=closed"},
	{"anything assigned to dana updated this week", `assignee="Dana Barrett" AND updated>"-7d"`},
	{"what is sam working on right now", `assignee="Sam Vimes" AND status=in_progress`},
	{"unlabeled open tasks", "status=open AND type=task AND label=none"},
	{"urgent stuff about migrations (no epic or label matches)", "title=migrations AND priority<=1"},
	{"exports work (an epic named Exports and invoices, id app-3xz, is in the vocabulary)", "subtree=app-3xz"},
	{"everything under the launch epic (id app-9kd is in the vocabulary)", "subtree=app-9kd"},
	{"performance regressions (a label named perf is in the vocabulary)", "label=perf AND type=bug"},
	{"epics or features about pricing", "(type=epic OR type=feature) AND title=pricing"},
	{"stale in-progress items", `status=in_progress AND updated<"-14d"`},
	{"direct children of demo-pqr", "parent=demo-pqr"},
	{"closed last month, not chores", `closed>"-30d" AND NOT type=chore`},
}

// dynamicShots demonstrates the vocabulary mappings with the board's OWN
// values — the code is a board-agnostic template, the vocabulary supplies the
// specifics. A 1.7B model follows a demonstrated mapping far more reliably
// than a described one.
func dynamicShots(v Vocab) [][2]string {
	var shots [][2]string
	if len(v.Assignees) > 0 {
		full := v.Assignees[0]
		first := strings.ToLower(strings.Fields(full)[0])
		shots = append(shots, [2]string{
			"what is " + first + " working on",
			fmt.Sprintf("assignee=%q AND status=in_progress", full),
		})
	}
	if len(v.Epics) > 0 {
		if id, title, ok := strings.Cut(v.Epics[0], " "); ok {
			if topic := topicWord(title); topic != "" {
				// "<topic> work" is the epic node + its whole subtree; children-only stays parent= alone.
				shots = append(shots, [2]string{topic + " work", "subtree=" + id})
			}
		}
	}
	if len(v.Labels) > 0 {
		name := strings.SplitN(v.Labels[0], "(", 2)[0]
		shots = append(shots, [2]string{name + " items", "label=" + name})
	}
	return shots
}

// topicWord picks the first meaningful word of an epic title.
func topicWord(title string) string {
	for _, w := range strings.Fields(strings.ToLower(title)) {
		w = strings.Trim(w, "[]():—-,.&")
		if len(w) > 3 && w != "epic" {
			return w
		}
	}
	return ""
}

// promptPrefix is everything before the ask: task, grammar + mapping rules,
// few-shots (static board-agnostic ones plus mappings demonstrated from the
// workspace vocabulary), and the vocabulary block.
func promptPrefix(v Vocab, includeDynamic bool) string {
	shots := make([]string, 0, len(fewShot)+3)
	for _, s := range fewShot {
		shots = append(shots, fmt.Sprintf("NL: %s\nQuery: %s", s[0], s[1]))
	}
	if includeDynamic {
		for _, s := range dynamicShots(v) {
			shots = append(shots, fmt.Sprintf("NL: %s\nQuery: %s", s[0], s[1]))
		}
	}
	var b strings.Builder
	b.WriteString("You translate natural language into the bd issue-tracker query language. Reply briefly; put the FINAL query alone inside a fenced block, with nothing after it:\n```query\n<the query>\n```\n\n")
	b.WriteString(grammarHelp)
	b.WriteString("\n\n")
	b.WriteString(strings.Join(shots, "\n\n"))
	if vocab := v.Render(); vocab != "" {
		b.WriteString("\n\n")
		b.WriteString(vocab)
	}
	return b.String()
}

var fieldRe = regexp.MustCompile(`(?i)\b(status|priority|type|assignee|owner|label|title|description|notes|created|updated|started|closed|id|parent|subtree|pinned)\s*(?:=|!=|>=|<=|>|<)`)

// fieldsIn extracts the distinct query fields used across queries, in
// first-seen order.
func fieldsIn(queries []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, q := range queries {
		for _, m := range fieldRe.FindAllStringSubmatch(q, -1) {
			f := strings.ToLower(m[1])
			if !seen[f] {
				seen[f] = true
				out = append(out, f)
			}
		}
	}
	return out
}

// promptOpts carries the optional prompt sections: prior (re-roll) attempts,
// grounded value mappings, and an execution-feedback note for a repair.
type promptOpts struct {
	prior      []string
	mappings   []Mapping
	repairNote string
}

// BuildPrompt assembles the first-compile / re-roll prompt for one NL ask.
// (Grounding and repair sections arrive through buildPrompt; this exported
// signature is the stable one callers and tests use.)
func BuildPrompt(nl string, v Vocab, prior []string) string {
	return buildPrompt(nl, v, promptOpts{prior: prior})
}

// buildPrompt is the full builder.
//
// Re-rolls (prior attempts present) fight a measured failure mode: this
// model pattern-locks and echoes its rejected answer through instructions,
// alternative formats, and temperature. Two things help: dropping the
// dynamic shots (they demonstrate exactly the first-choice mapping the user
// just rejected) and a mechanical constraint banning the rejected queries'
// fields. Example shots for the re-roll pattern are deliberately ABSENT —
// the model copies their literal values into unrelated asks.
//
// First compiles carry the grounded value mappings and, on a repair, the
// execution-feedback note — both placed after the vocabulary and before the
// ask so the model reads them last.
func buildPrompt(nl string, v Vocab, o promptOpts) string {
	var b strings.Builder
	if len(o.prior) > 0 {
		b.WriteString(promptPrefix(v, false))
		b.WriteString("\n\nThe user rejected:")
		for i, q := range o.prior {
			fmt.Fprintf(&b, " %d) %s", i+1, q)
		}
		b.WriteString("\nGive one meaningfully different valid query for the same request")
		if fields := fieldsIn(o.prior); len(fields) > 0 {
			b.WriteString(" — it must NOT use the field")
			if len(fields) > 1 {
				b.WriteString("s")
			}
			b.WriteString(" " + strings.Join(fields, ", "))
		}
		fmt.Fprintf(&b, ".\nNL: %s\nQuery:", nl)
		return b.String()
	}
	b.WriteString(promptPrefix(v, true))
	if m := renderMappings(o.mappings); m != "" {
		b.WriteString("\n\n")
		b.WriteString(m)
	}
	if o.repairNote != "" {
		b.WriteString("\n\n")
		b.WriteString(o.repairNote)
	}
	fmt.Fprintf(&b, "\nNL: %s\nQuery:", nl)
	return b.String()
}

var (
	fenceRe = regexp.MustCompile("```[a-z]*\n?")
	thinkRe = regexp.MustCompile(`(?s)<think>.*?</think>`)
	queryRe = regexp.MustCompile(`(?i)^query:\s*`)
)

var (
	// queryShapeRe: a line that BEGINS like a bd query (optional NOT/parens,
	// then a known field and operator). Thinking-by-default models (Qwen3.5+)
	// narrate as plain prose with no <think> tags, so shape is the only
	// reliable signal.
	queryShapeRe = regexp.MustCompile(`(?i)^\(*\s*(?:NOT\s+)?\(*\s*(status|priority|type|assignee|owner|label|title|description|notes|created|updated|started|closed|id|parent|subtree|pinned)\s*(=|!=|>=|<=|>|<)`)
	fencedRe     = regexp.MustCompile("(?s)```[^\n]*\n(.*?)```")
)

// parentIdUnionRe / idParentUnionRe match the invalid `parent=<id> OR id=<id>`
// union (either order, optionally parenthesized) that models still emit for
// "everything under X" despite the prompt teaching subtree=. bd rejects it
// (parent is =-only and may not be OR-ed), so it is rewritten to the valid
// in-process subtree= form BEFORE the query is ever displayed or pre-executed.
var (
	parentIdUnionRe = regexp.MustCompile(`(?i)\(?\s*parent\s*=\s*([^\s()]+)\s+OR\s+id\s*=\s*([^\s()]+)\s*\)?`)
	idParentUnionRe = regexp.MustCompile(`(?i)\(?\s*id\s*=\s*([^\s()]+)\s+OR\s+parent\s*=\s*([^\s()]+)\s*\)?`)
)

// rewriteParentIdUnion converts a `parent=X OR id=X` (or `id=X OR parent=X`)
// union for the SAME id into `subtree=X`, whatever the model emitted. A prompt
// change can't guarantee the model stops producing the pattern, so it is
// normalized here deterministically — the invalid OR-parent form never reaches
// bd, and the valid in-process subtree= form is what the user reviews.
func rewriteParentIdUnion(q string) string {
	repl := func(re *regexp.Regexp, s string) string {
		return re.ReplaceAllStringFunc(s, func(match string) string {
			sub := re.FindStringSubmatch(match)
			if len(sub) == 3 && strings.EqualFold(sub[1], sub[2]) {
				return "subtree=" + sub[1]
			}
			return match // a genuine two-id union is left for bd to handle/reject
		})
	}
	return strings.TrimSpace(repl(idParentUnionRe, repl(parentIdUnionRe, q)))
}

// ExtractCompiled pulls the compiled query out of any model's reply, in
// order: (a) the last fenced block's query-shaped line; (b) the last
// query-shaped line anywhere; else failure — prose is NEVER presented as a
// query. The extracted query is normalized (the invalid parent∪id union →
// subtree=) so what the caller pre-executes and shows is always applyable.
func ExtractCompiled(raw string) (string, bool) {
	cleaned := thinkRe.ReplaceAllString(raw, "")
	if ms := fencedRe.FindAllStringSubmatch(cleaned, -1); len(ms) > 0 {
		for _, line := range strings.Split(ms[len(ms)-1][1], "\n") {
			if q, ok := queryLine(line); ok {
				return rewriteParentIdUnion(q), true
			}
		}
	}
	lines := strings.Split(fenceRe.ReplaceAllString(cleaned, ""), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if q, ok := queryLine(lines[i]); ok {
			return rewriteParentIdUnion(q), true
		}
	}
	return "", false
}

// IsQueryShaped reports whether s begins like a bd query — a known field
// followed by an operator (optionally behind NOT/parens). It is the signal the
// smart `/` prompt uses to route input: query-shaped → run it as a raw filter;
// otherwise → treat it as natural language and compile it.
func IsQueryShaped(s string) bool {
	return queryShapeRe.MatchString(strings.TrimSpace(s))
}

func queryLine(line string) (string, bool) {
	q := strings.TrimSpace(queryRe.ReplaceAllString(strings.TrimSpace(line), ""))
	if q != "" && queryShapeRe.MatchString(q) {
		return q, true
	}
	return "", false
}

// compileFailure formats the never-show-prose error.
func compileFailure(raw string) error {
	snippet := strings.Join(strings.Fields(raw), " ")
	r := []rune(snippet)
	if len(r) > 80 {
		snippet = string(r[:80]) + "…"
	}
	return fmt.Errorf("model did not produce a query (try re-roll): %s", snippet)
}

// ExtractQuery strips fences/prose a chatty model might add and keeps the
// first plausible query line.
func ExtractQuery(raw string) string {
	cleaned := strings.TrimSpace(fenceRe.ReplaceAllString(raw, ""))
	for _, line := range strings.Split(cleaned, "\n") {
		if strings.TrimSpace(line) != "" {
			return strings.TrimSpace(queryRe.ReplaceAllString(strings.TrimSpace(line), ""))
		}
	}
	return ""
}

// CleanReply drops a Qwen3 <think> block (emitted even with /no_think
// sometimes) and then extracts the query line.
func CleanReply(raw string) string {
	return ExtractQuery(thinkRe.ReplaceAllString(raw, ""))
}

// Provider compiles NL against any OpenAI-compatible /v1 server: rapid-mlx,
// omlx, vllm-mlx, llama.cpp server, etc. Point BB_NLQ_URL at the base
// (default localhost:8000/v1).
type Provider struct {
	Model string
	URL   string
	Label string
	Key   string // optional bearer token (BB_NLQ_KEY); omlx requires one

	client *http.Client
}

func NewProvider() *Provider {
	model := os.Getenv("BB_NLQ_MODEL")
	if model == "" {
		model = "mlx-community/Qwen3-1.7B-4bit"
	}
	url := os.Getenv("BB_NLQ_URL")
	if url == "" {
		url = "http://127.0.0.1:8000/v1"
	}
	return &Provider{Model: model, URL: url, Label: LabelFor(model),
		Key:    os.Getenv("BB_NLQ_KEY"),
		client: &http.Client{Timeout: 60 * time.Second}}
}

// LabelFor shortens a model id for display: the basename after "/" or the
// omlx "--" separator.
func LabelFor(model string) string {
	label := model
	if i := strings.LastIndex(label, "/"); i >= 0 {
		label = label[i+1:]
	}
	if i := strings.LastIndex(label, "--"); i >= 0 {
		label = label[i+2:]
	}
	return label
}

// serverError surfaces an OpenAI-shaped error body ({"error":{"message":…}})
// as its message, with a key hint on 401s.
func serverError(who string, status int, body []byte) error {
	msg := strings.TrimSpace(string(body))
	var shaped struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &shaped) == nil && shaped.Error.Message != "" {
		msg = shaped.Error.Message
	} else {
		var flat struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(body, &flat) == nil && flat.Error != "" {
			msg = flat.Error
		}
	}
	if status == http.StatusUnauthorized {
		return fmt.Errorf("%s server 401: %s — set BB_NLQ_KEY (omlx keys live in ~/.omlx/settings.json auth.api_key)", who, msg)
	}
	return fmt.Errorf("%s server %d: %s", who, status, msg)
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

// Re-roll decoding: temp 0 repeats itself even when told not to (measured
// live on Qwen3-1.7B), so re-rolls decode warmer; an echo of a rejected
// query triggers ONE hotter client-side retry.
const (
	rerollTemp = 0.7
	retryTemp  = 1.0
)

func containsQuery(list []string, q string) bool {
	for _, v := range list {
		if v == q {
			return true
		}
	}
	return false
}

// Compile turns one NL ask into a bd query via the model server. v is the
// workspace vocabulary (zero value for none); prior carries earlier compiles
// the user re-rolled away from.
//
// First compiles decode at temperature 0. Re-rolls ask for a numbered list
// of alternatives (see BuildPrompt) at rerollTemp and pick the first
// candidate not already rejected; if every candidate is an echo, ONE hotter
// retry, then the best echo is surfaced rather than erroring.
func (p *Provider) Compile(nl string, v Vocab, prior []string) (Result, error) {
	if len(prior) == 0 {
		raw, err := p.chat(BuildPrompt(nl, v, nil), 0)
		if err != nil {
			return Result{}, err
		}
		q, ok := ExtractCompiled(raw)
		if !ok {
			return Result{}, compileFailure(raw)
		}
		return Result{NL: nl, Query: q}, nil
	}
	// The server decodes deterministically per prompt, so the retry must
	// CHANGE the prompt: an echo joins the rejected list (banning its
	// fields) before the second attempt.
	rejected := append([]string(nil), prior...)
	var fallback string
	for _, temp := range []float64{rerollTemp, retryTemp} {
		raw, err := p.chat(BuildPrompt(nl, v, rejected), temp)
		if err != nil {
			return Result{}, err
		}
		q, ok := ExtractCompiled(raw)
		if !ok {
			continue
		}
		if fallback == "" {
			fallback = q
		}
		if !containsQuery(prior, q) {
			return Result{NL: nl, Query: q}, nil
		}
		if q != "" {
			// Duplicates allowed: even when the echo already sits in the
			// rejected list, re-listing it changes the prompt (the server is
			// deterministic per prompt) and emphasizes the rejection.
			rejected = append(rejected, q)
		}
	}
	if fallback == "" {
		return Result{}, fmt.Errorf("model did not produce a query on re-roll (try e to edit)")
	}
	return Result{NL: nl, Query: fallback}, nil
}

// CompileWithRepair is the first-compile path: ground the ask's entities to
// real values, compile, then PRE-EXECUTE and self-repair. A parse error, zero
// rows, or suspiciously few rows for a plural/topical ask triggers a revision
// (max 2) fed the execution feedback — verbatim error, row count, sample
// rows, and on zero rows the workspace's actual values with a "broaden or
// re-map" instruction. Returns the best attempt's query with its row count
// and the full trail. Best-effort: a mid-loop server error keeps the best so
// far; a first-call error propagates so the caller can re-resolve.
func (p *Provider) CompileWithRepair(nl string, v Vocab, g Grounder, exec QueryExecutor) (Result, error) {
	var mappings []Mapping
	if g != nil {
		mappings = g.Ground(nl, v)
	}
	plural := readsPlural(nl)
	var attempts []Attempt
	var best Attempt
	bestScore := -1
	var repairNote, lastRaw string

	for i := 0; i <= maxRepairs; i++ {
		temp := 0.0
		if i > 0 {
			temp = repairTemp
		}
		raw, err := p.chat(buildPrompt(nl, v, promptOpts{mappings: mappings, repairNote: repairNote}), temp)
		if err != nil {
			if bestScore < 0 {
				return Result{NL: nl}, err
			}
			break // keep the best-effort result rather than erroring
		}
		lastRaw = raw
		q, ok := ExtractCompiled(raw)
		if !ok {
			attempts = append(attempts, Attempt{Trigger: "no-query"})
			if i == maxRepairs {
				break
			}
			repairNote = repairFeedback("", 0, nil, nil, "no-query", v, "")
			continue
		}
		count, sample, xerr := exec(q)
		att := Attempt{Query: q, Count: count}
		score, trigger := 3, ""
		switch {
		case xerr != nil:
			att.Err, score, trigger = xerr.Error(), 0, "parse-error"
		case count == 0:
			score, trigger = 1, "zero-rows"
		case plural && count < fewRows:
			score, trigger = 2, "too-few"
		}
		att.Trigger = trigger
		attempts = append(attempts, att)
		if score > bestScore {
			best, bestScore = att, score
		}
		if score == 3 || i == maxRepairs {
			break
		}
		// On zero rows, isolate WHICH conjunct is empty — the dominant
		// failure is one over-narrow/wrong clause, and naming it beats a
		// generic "broaden" hint at 4B scale.
		diag := ""
		if trigger == "zero-rows" {
			diag = zeroRowDiagnosis(q, exec)
		}
		repairNote = repairFeedback(q, count, sample, xerr, trigger, v, diag)
	}

	if best.Query == "" {
		return Result{NL: nl, Attempts: attempts}, compileFailure(lastRaw)
	}
	if best.Err != "" {
		// Even the best candidate errors on pre-execution — never PRESENT an
		// un-applyable query for the user to hit enter on. Surface it as a failed
		// compile ("rephrase / re-roll"), not a query that errors on apply. The
		// apply-time revert guard stays as a backstop; a normal ask won't reach it.
		return Result{NL: nl, Attempts: attempts},
			fmt.Errorf("couldn't compile a runnable query (try rephrasing or re-roll): %s", best.Err)
	}
	return Result{NL: nl, Query: best.Query, Count: best.Count, Attempts: attempts}, nil
}

// repairFeedback is the execution-feedback note fed to the next revision. The
// zero-row/too-few cases carry the workspace's real values, because "wrong
// value or over-narrow filter" is the dominant silent-failure mode and the
// fix is re-grounding, not syntax (docs/nlq-sota-2026.md §1.1).
func repairFeedback(q string, count int, sample []string, xerr error, trigger string, v Vocab, diag string) string {
	var b strings.Builder
	switch trigger {
	case "parse-error":
		fmt.Fprintf(&b, "The query `%s` did not run: %s\nFix the syntax — only the listed fields and enum values exist. Reply with one corrected query.", q, errText(xerr))
	case "zero-rows":
		fmt.Fprintf(&b, "The query `%s` returned 0 results — most likely a wrong value or an over-narrow filter. ", q)
		b.WriteString("Re-map the topic to one of the workspace's REAL values below, or broaden: match a topic word as title=<word>, use a parent=<epic id>, or drop a narrow AND clause. Reply with one revised query.\n")
		if diag != "" {
			b.WriteString(diag)
			b.WriteString("\n")
		}
		b.WriteString(realValues(v))
	case "too-few":
		fmt.Fprintf(&b, "The query `%s` returned only %d result(s) for what reads like a broad or topical ask", q, count)
		if len(sample) > 0 {
			fmt.Fprintf(&b, " (e.g. %s)", strings.Join(sample, "; "))
		}
		b.WriteString(". Broaden it — OR across title/label/parent, or drop a narrow clause — without drifting off-topic. Reply with one revised query.\n")
		b.WriteString(realValues(v))
	case "no-query":
		b.WriteString("You did not reply with a query. Reply with ONE bd query alone inside a ```query fenced block, nothing after it.")
	}
	return b.String()
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// zeroRowDiagnosis isolates which conjunct of a 0-row AND query is itself
// empty — the surgical signal that turns "broaden somehow" into "drop
// `title=exports`, it matches nothing." Skips parenthesized queries (the flat
// AND case covers the dominant over-narrow failure) and runs each clause once.
func zeroRowDiagnosis(q string, exec QueryExecutor) string {
	if strings.ContainsAny(q, "()") {
		return ""
	}
	parts := andSplit(q)
	if len(parts) < 2 {
		return ""
	}
	var empty, kept []string
	for _, c := range parts {
		n, _, err := exec(c)
		if err != nil {
			return "" // a clause won't parse alone; don't over-claim
		}
		if n == 0 {
			empty = append(empty, "`"+c+"`")
		} else {
			kept = append(kept, fmt.Sprintf("`%s` (%d)", c, n))
		}
	}
	if len(empty) == 0 {
		return "" // no single empty clause — the AND itself is the emptiness
	}
	verb := "matches"
	if len(empty) > 1 {
		verb = "match"
	}
	d := fmt.Sprintf("execution diagnosis: the clause %s %s 0 rows alone — remove or re-map it", strings.Join(empty, ", "), verb)
	if len(kept) > 0 {
		d += ". Clauses that DO match: " + strings.Join(kept, ", ")
	}
	return d + "."
}

// andSplit splits a query on top-level AND, quote-aware so a boolean AND is
// never confused with an "and" inside a quoted value (title="Reports and
// rent"). Caller guarantees no parentheses.
func andSplit(q string) []string {
	var parts []string
	var cur strings.Builder
	inQuote := false
	for i := 0; i < len(q); {
		if q[i] == '"' {
			inQuote = !inQuote
			cur.WriteByte(q[i])
			i++
			continue
		}
		if !inQuote && i+5 <= len(q) && q[i] == ' ' && strings.EqualFold(q[i+1:i+4], "and") && q[i+4] == ' ' {
			if s := strings.TrimSpace(cur.String()); s != "" {
				parts = append(parts, s)
			}
			cur.Reset()
			i += 5
			continue
		}
		cur.WriteByte(q[i])
		i++
	}
	if s := strings.TrimSpace(cur.String()); s != "" {
		parts = append(parts, s)
	}
	return parts
}

// realValues is the compact "here are the actual values" block for a re-ground.
func realValues(v Vocab) string {
	var parts []string
	if len(v.Types) > 0 {
		parts = append(parts, "types: "+strings.Join(v.Types, ", "))
	}
	if len(v.Labels) > 0 {
		names := make([]string, 0, len(v.Labels))
		for _, l := range v.Labels {
			names = append(names, strings.SplitN(l, "(", 2)[0])
		}
		parts = append(parts, "labels: "+strings.Join(names, ", "))
	}
	if len(v.Assignees) > 0 {
		parts = append(parts, "assignees: "+quoteAll(v.Assignees))
	}
	if len(v.Epics) > 0 {
		parts = append(parts, "epics (parent=<id>): "+strings.Join(v.Epics, "; "))
	}
	return "real values — " + strings.Join(parts, " | ")
}

// pluralMarkers are words that make an ask read broad/topical, so a 1–2 row
// result is "suspiciously few" and worth a broaden pass.
var pluralMarkers = map[string]bool{
	"stuff": true, "things": true, "all": true, "every": true, "everything": true,
	"anything": true, "list": true, "about": true, "around": true, "regarding": true,
	"related": true, "problems": true, "issues": true, "items": true, "work": true,
	"bugs": true, "features": true, "tasks": true, "epics": true, "chores": true,
	"decisions": true, "which": true, "what": true, "any": true,
}

func readsPlural(nl string) bool {
	for _, tk := range strings.FieldsFunc(strings.ToLower(nl), func(r rune) bool {
		return !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9')
	}) {
		if pluralMarkers[tk] {
			return true
		}
	}
	return false
}

// chat runs one completion and returns the think-stripped content.
func (p *Provider) chat(prompt string, temperature float64) (string, error) {
	if p.client == nil {
		p.client = &http.Client{Timeout: 60 * time.Second}
	}
	payload, err := json.Marshal(map[string]any{
		"model":       p.Model,
		"messages":    []map[string]string{{"role": "user", "content": prompt}},
		"temperature": temperature,
		"max_tokens":  800, // thinking models burn budget before the answer
		"stream":      false,
		// Thinking suppression — verified live on omlx (see README).
		"chat_template_kwargs": map[string]any{"enable_thinking": false},
	})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequest("POST", p.URL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if p.Key != "" {
		req.Header.Set("Authorization", "Bearer "+p.Key)
	}
	res, err := p.client.Do(req)
	if err != nil {
		return "", fmt.Errorf(
			"model server unreachable at %s — start one: rapid-mlx serve %s --port 8000", p.URL, p.Model)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return "", err
	}
	if res.StatusCode != http.StatusOK {
		return "", serverError("nlq", res.StatusCode, body)
	}
	var parsed chatResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("nlq server: bad response: %w", err)
	}
	raw := ""
	if len(parsed.Choices) > 0 {
		raw = parsed.Choices[0].Message.Content
	}
	return thinkRe.ReplaceAllString(raw, ""), nil
}

// FeedbackAction is the user's verdict on a compiled query.
type FeedbackAction string

const (
	Accepted FeedbackAction = "accepted"
	Edited   FeedbackAction = "edited"
	Rejected FeedbackAction = "rejected"
	// Rerolled: the user asked for a different interpretation of the same NL
	// — not wrong enough to reject outright, but unsatisfying. High-value
	// training signal.
	Rerolled FeedbackAction = "rerolled"
	// AnalystAsked: a whole-board analyst question; Compiled carries the ids
	// the analyst returned.
	AnalystAsked FeedbackAction = "analyst"
	// Repaired: one revision in the execution-feedback loop — Compiled is the
	// attempt's query, Count its row count, Trigger why the revision fired.
	// Logged automatically (not a user verdict); high-value training signal.
	Repaired FeedbackAction = "repaired"
)

// FeedbackRecord is one JSONL line of the training log. Field names and
// shape match the TS version exactly — the log is training data; append,
// never truncate.
type FeedbackRecord struct {
	TS       string         `json:"ts"`
	Provider string         `json:"provider"`
	NL       string         `json:"nl"`
	Compiled string         `json:"compiled"`
	Action   FeedbackAction `json:"action"`
	// Final is what actually ran (differs from Compiled when edited).
	Final string `json:"final,omitempty"`
	// Count is the rows the compiled query returned (repair trail); Trigger is
	// why a repair fired. Both omitempty — older readers ignore unknown keys.
	Count   int    `json:"count,omitempty"`
	Trigger string `json:"trigger,omitempty"`
}

// FeedbackLog appends verdicts to ~/.config/bb/nlq-feedback.jsonl
// (BB_NLQ_LOG overrides the path).
type FeedbackLog struct {
	Path string
}

func NewFeedbackLog() *FeedbackLog {
	if p := os.Getenv("BB_NLQ_LOG"); p != "" {
		return &FeedbackLog{Path: p}
	}
	return &FeedbackLog{Path: filepath.Join(discover.ConfigDir(), "nlq-feedback.jsonl")}
}

// Append writes one record. Best-effort: feedback never breaks the board.
func (l *FeedbackLog) Append(rec FeedbackRecord) {
	if rec.TS == "" {
		rec.TS = time.Now().UTC().Format(time.RFC3339)
	}
	line, err := json.Marshal(rec)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(l.Path), 0o700); err != nil {
		return
	}
	f, err := os.OpenFile(l.Path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	_ = f.Chmod(0o600)
	defer f.Close()
	_, _ = f.Write(append(line, '\n'))
}
