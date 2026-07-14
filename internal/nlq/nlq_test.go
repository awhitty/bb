package nlq

import (
	"bufio"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtractQuery(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"plain", "status=open AND type=bug", "status=open AND type=bug"},
		{"code fences", "```\nstatus=open\n```", "status=open"},
		{"language fence", "```sql\nstatus=open\n```", "status=open"},
		{"query prefix", "Query: status=open", "status=open"},
		{"query prefix case-insensitive", "QUERY:   priority<=1", "priority<=1"},
		{"first non-empty line", "\n\npriority<=1 AND status!=closed\nsome trailing prose", "priority<=1 AND status!=closed"},
		{"empty", "   \n  ", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ExtractQuery(tc.in); got != tc.want {
				t.Fatalf("ExtractQuery(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestCleanReplyStripsThinkBlock(t *testing.T) {
	raw := "<think>\nthe user wants open bugs so status=open and type=bug\n</think>\nstatus=open AND type=bug"
	if got := CleanReply(raw); got != "status=open AND type=bug" {
		t.Fatalf("got %q", got)
	}
	// Think block + fences + prefix all at once.
	raw = "<think>hmm</think>\n```\nQuery: type=epic\n```"
	if got := CleanReply(raw); got != "type=epic" {
		t.Fatalf("got %q", got)
	}
}

func TestBuildPromptCarriesGrammarAndFewShot(t *testing.T) {
	p := BuildPrompt("open bugs about caching", Vocab{}, nil)
	for _, must := range []string{
		"bd query language:",
		"map topic words to title=<word>, never invent a field or type",
		"mapping rules",
		"subtree=<id> (the epic node AND its whole subtree)",
		"subtree=<id> is a bb form (NOT a bd field)",
		"NL: open bugs\nQuery: status=open AND type=bug",
		`NL: stale in-progress items
Query: status=in_progress AND updated<"-14d"`,
		`NL: what is sam working on right now
Query: assignee="Sam Vimes" AND status=in_progress`,
		"NL: open bugs about caching\nQuery:",
	} {
		if !strings.Contains(p, must) {
			t.Fatalf("prompt missing %q", must)
		}
	}
	if strings.Contains(p, "/no_think") {
		t.Fatal("/no_think belongs to the POST body, not the prompt builder")
	}
	if strings.Contains(p, "workspace vocabulary (real values from this board") {
		t.Fatal("empty vocab must not render a vocabulary section")
	}
	if strings.Contains(p, "Previous attempts") {
		t.Fatal("no prior rolls → no re-roll section")
	}
}

func TestBuildPromptVocabAndPriorSections(t *testing.T) {
	v := Vocab{
		Assignees: []string{"Alex Rivera"},
		Epics:     []string{"demo-abc Inference onboarding"},
		Labels:    []string{"critical-path(8)"},
	}
	p := BuildPrompt("reporting stuff", v, []string{"title=reporting", "parent=demo-abc"})
	vocabAt := strings.Index(p, "workspace vocabulary (real values from this board")
	shotsAt := strings.Index(p, "NL: open bugs\n")
	priorAt := strings.Index(p, "The user rejected: 1) title=reporting 2) parent=demo-abc")
	askAt := strings.Index(p, "NL: reporting stuff\nQuery:")
	if vocabAt < 0 || priorAt < 0 || askAt < 0 {
		t.Fatalf("missing sections: vocab=%d prior=%d ask=%d\n%s", vocabAt, priorAt, askAt, p)
	}
	// Order: few-shots, then vocab, then the rejected list + field ban,
	// then the ask.
	if !(shotsAt < vocabAt && vocabAt < priorAt && priorAt < askAt) {
		t.Fatalf("section order wrong: shots=%d vocab=%d prior=%d ask=%d", shotsAt, vocabAt, priorAt, askAt)
	}
	for _, must := range []string{
		"The user rejected: 1) title=reporting 2) parent=demo-abc",
		"it must NOT use the fields title, parent.",
		"NL: reporting stuff\nQuery:",
		`"Alex Rivera"`,
	} {
		if !strings.Contains(p, must) {
			t.Fatalf("prompt missing %q", must)
		}
	}
	// Dynamic few-shots appear on FIRST compiles only — on a re-roll they
	// would demonstrate exactly the rejected first-choice mapping.
	if strings.Contains(p, "NL: critical-path items\nQuery: label=critical-path") {
		t.Fatal("re-roll prompt must not carry dynamic shots")
	}
	first := BuildPrompt("reporting stuff", v, nil)
	for _, must := range []string{
		"NL: what is alex working on\nQuery: assignee=\"Alex Rivera\" AND status=in_progress",
		"NL: inference work\nQuery: subtree=demo-abc",
		"NL: critical-path items\nQuery: label=critical-path",
	} {
		if !strings.Contains(first, must) {
			t.Fatalf("first-compile prompt missing dynamic shot %q", must)
		}
	}
	// The invalid OR-parent form bd rejects must never appear in any prompt.
	if strings.Contains(first, " OR id=") || strings.Contains(first, "parent=demo-abc OR") {
		t.Fatalf("prompt still teaches the bd-rejected parent-OR form:\n%s", first)
	}
}

func TestCompileAgainstFakeServer(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("path = %s", r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"<think>x</think>\nQuery: status=open"}}]}`))
	}))
	defer srv.Close()

	p := &Provider{Model: "test/model", URL: srv.URL + "/v1", Label: "model", client: srv.Client()}
	res, err := p.Compile("whats open", Vocab{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.NL != "whats open" || res.Query != "status=open" {
		t.Fatalf("res = %+v", res)
	}
	if gotBody["temperature"].(float64) != 0 || gotBody["max_tokens"].(float64) != 800 {
		t.Fatalf("params = %v", gotBody)
	}
	// Thinking suppression rides every request — verified live on omlx to
	// cut Qwen3.5/3.6 replies from 800 narration tokens to ~13.
	kwargs, _ := gotBody["chat_template_kwargs"].(map[string]any)
	if kwargs == nil || kwargs["enable_thinking"] != false {
		t.Fatalf("chat_template_kwargs must disable thinking: %v", gotBody["chat_template_kwargs"])
	}
	msg := gotBody["messages"].([]any)[0].(map[string]any)["content"].(string)
	if strings.Contains(msg, "/no_think") {
		t.Fatal("/no_think is dead weight on Qwen3.5+ and polluted completions — gone")
	}
}

func TestCompileUnreachableServerNamesTheFix(t *testing.T) {
	p := &Provider{Model: "mlx-community/Qwen3-1.7B-4bit", URL: "http://127.0.0.1:1",
		Label: "Qwen3-1.7B-4bit", client: http.DefaultClient}
	_, err := p.Compile("x", Vocab{}, nil)
	if err == nil {
		t.Fatal("want error")
	}
	if !strings.Contains(err.Error(), "rapid-mlx serve mlx-community/Qwen3-1.7B-4bit --port 8000") {
		t.Fatalf("error must carry the exact fix, got: %v", err)
	}
}

func TestFeedbackLogAppendsJSONL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "log.jsonl")
	l := &FeedbackLog{Path: path}
	l.Append(FeedbackRecord{Provider: "m", NL: "open bugs", Compiled: "status=open AND type=bug", Action: Accepted, Final: "status=open AND type=bug"})
	l.Append(FeedbackRecord{Provider: "m", NL: "nope", Compiled: "bad=1", Action: Rejected})

	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var lines []map[string]any
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var rec map[string]any
		if err := json.Unmarshal(sc.Bytes(), &rec); err != nil {
			t.Fatalf("bad JSONL line: %v", err)
		}
		lines = append(lines, rec)
	}
	if len(lines) != 2 {
		t.Fatalf("lines = %d", len(lines))
	}
	// TS-format parity: exact key set, final omitted on rejected.
	first := lines[0]
	for _, k := range []string{"ts", "provider", "nl", "compiled", "action", "final"} {
		if _, ok := first[k]; !ok {
			t.Fatalf("accepted record missing %q: %v", k, first)
		}
	}
	if _, ok := lines[1]["final"]; ok {
		t.Fatalf("rejected record must omit final: %v", lines[1])
	}
	if lines[1]["action"] != "rejected" || first["action"] != "accepted" {
		t.Fatalf("actions wrong: %v %v", first["action"], lines[1]["action"])
	}
	if ts, _ := first["ts"].(string); ts == "" {
		t.Fatal("ts must be auto-filled")
	}
}

// Re-rolls ban the rejected queries' fields and expect a single query; an
// echo triggers exactly one retry with the echo added to the ban list, then
// the echo is surfaced rather than erroring. First compiles stay temp 0.
func TestCompileRerollEchoRetry(t *testing.T) {
	var temps []float64
	var prompts []string
	reply := "title=reporting"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		temps = append(temps, body["temperature"].(float64))
		prompts = append(prompts, body["messages"].([]any)[0].(map[string]any)["content"].(string))
		b, _ := json.Marshal(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"content": reply}}},
		})
		_, _ = w.Write(b)
	}))
	defer srv.Close()
	p := &Provider{Model: "m", URL: srv.URL + "/v1", Label: "m", client: srv.Client()}

	// First compile: one call, temp 0.
	res, err := p.Compile("reporting stuff", Vocab{}, nil)
	if err != nil || res.Query != "title=reporting" {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if len(temps) != 1 || temps[0] != 0 {
		t.Fatalf("first compile temps = %v", temps)
	}

	// Re-roll that differs immediately: one call.
	temps, prompts = nil, nil
	reply = "label=reporting"
	res, err = p.Compile("reporting stuff", Vocab{}, []string{"title=reporting"})
	if err != nil || res.Query != "label=reporting" {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if len(temps) != 1 || temps[0] != rerollTemp {
		t.Fatalf("reroll temps = %v", temps)
	}
	if !strings.Contains(prompts[0], "it must NOT use the field title.") {
		t.Fatalf("reroll prompt must ban rejected fields:\n%s", prompts[0])
	}

	// Echo: retry once with the echo's fields banned too, then surface it.
	temps, prompts = nil, nil
	reply = "parent=demo-pqr"
	res, err = p.Compile("reporting stuff", Vocab{}, []string{"parent=demo-pqr"})
	if err != nil || res.Query != "parent=demo-pqr" {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if len(temps) != 2 || temps[1] != retryTemp {
		t.Fatalf("echo-retry temps = %v", temps)
	}
	if prompts[0] == prompts[1] {
		t.Fatal("retry must change the prompt (server is deterministic per prompt)")
	}
}

// "everything under X" must compile to the subtree form, never the
// bd-rejected `parent=X OR id=X`. Guards the static + dynamic few-shots that
// teach the model this mapping.
func TestSubtreeShotsReplaceParentOr(t *testing.T) {
	for _, s := range fewShot {
		if strings.Contains(s[1], " OR id=") || strings.Contains(s[1], "OR id ") {
			t.Fatalf("static few-shot still emits the OR-parent form bd rejects: %q -> %q", s[0], s[1])
		}
	}
	sawSubtree := false
	for _, s := range fewShot {
		if strings.HasPrefix(s[1], "subtree=") {
			sawSubtree = true
		}
	}
	if !sawSubtree {
		t.Fatal("no static few-shot demonstrates the subtree form")
	}
	// Dynamic shot for the board's own epic: subtree=<id>, never parent OR id.
	shots := dynamicShots(Vocab{Epics: []string{"demo-pqr Reporting and exports"}})
	var work string
	for _, s := range shots {
		if strings.Contains(s[0], "work") {
			work = s[1]
		}
	}
	if work != "subtree=demo-pqr" {
		t.Fatalf(`dynamic "<topic> work" shot = %q, want "subtree=demo-pqr"`, work)
	}
}

func TestFieldsIn(t *testing.T) {
	got := fieldsIn([]string{`assignee="Alex Rivera" AND status=in_progress`, "updated>\"-7d\" AND status=open", "parent=demo-pqr"})
	want := []string{"assignee", "status", "updated", "parent"}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("field %d = %q, want %q (all %v)", i, got[i], want[i], got)
		}
	}
}

// Extraction must never present prose as a query — thinking-by-default
// models (Qwen3.5+) narrate reasoning as plain text with no <think> tags.
func TestExtractCompiled(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
		ok   bool
	}{
		{"query-first (1.7B shape)", "status=open AND type=bug", "status=open AND type=bug", true},
		{"Query: prefix", "Query: priority<=1 AND status!=closed", "priority<=1 AND status!=closed", true},
		{"tagged think block", "<think>reasoning here</think>\nlabel=critical-path", "label=critical-path", true},
		{"untagged prose then query", "Here's a thinking process:\n1. Analyze the request.\n2. Map fields.\nstatus=open AND type=bug", "status=open AND type=bug", true},
		{"fenced query block", "Let me think about this.\n```query\nparent=demo-pqr\n```", "parent=demo-pqr", true},
		{"fenced block wins over earlier prose match", "maybe status=open?\n```query\nlabel=now\n```", "label=now", true},
		{"NOT and parens", "(NOT type=chore) AND status=open", "(NOT type=chore) AND status=open", true},
		{"prose only", "Here's a thinking process: 1. Analyze the user input. 2. The task is translation.", "", false},
		{"empty", "", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := ExtractCompiled(tc.in)
			if ok != tc.ok || got != tc.want {
				t.Fatalf("ExtractCompiled(%q) = (%q, %v), want (%q, %v)", tc.in, got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestCompileFailsOnProse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"Here is a thinking process: 1. Analyze. 2. Deliberate. 3. Never answer."}}]}`))
	}))
	defer srv.Close()
	p := &Provider{Model: "m", URL: srv.URL + "/v1", Label: "m", client: srv.Client()}
	_, err := p.Compile("open bugs", Vocab{}, nil)
	if err == nil || !strings.Contains(err.Error(), "model did not produce a query (try re-roll)") {
		t.Fatalf("prose must be a compile failure, got err=%v", err)
	}
}

// --- value grounding ---

func TestLexicalGrounderMapsEntities(t *testing.T) {
	v := Vocab{
		Types:     []string{"bug", "task", "epic"},
		Assignees: []string{"Alex Rivera", "Sam Vimes"},
		Labels:    []string{"onboarding(5)", "critical-path(8)"},
		Epics:     []string{"demo-pqr Reporting and exports", "demo-abc Inference onboarding"},
	}
	g := LexicalGrounder{}
	got := func(nl string) map[string]bool {
		m := map[string]bool{}
		for _, x := range g.Ground(nl, v) {
			m[x.Hint] = true
		}
		return m
	}
	// name-part → exact assignee
	if !got("what has alex shipped this week")[`assignee="Alex Rivera"`] {
		t.Fatal("alex should ground to the full assignee")
	}
	// label token
	if !got("stuff about the onboarding flow")["label=onboarding"] {
		t.Fatal("onboarding should ground to the label")
	}
	// epic title word → parent
	if m := got("reporting stuff"); !m[`parent=demo-pqr (epic "Reporting and exports")`] {
		t.Fatalf("reporting should ground to the epic parent, got %v", m)
	}
	// plural type
	if !got("open bugs")["type=bug"] {
		t.Fatal("bugs should ground to type=bug")
	}
	// generic words must NOT false-map
	if m := got("show me all the work"); len(m) != 0 {
		t.Fatalf("generic ask should ground nothing, got %v", m)
	}
}

func TestReadsPlural(t *testing.T) {
	plural := []string{"reporting stuff", "urgent export problems", "what has alex shipped", "all open bugs"}
	singular := []string{"demo-pqr", "the calendar view", "alex rivera"}
	for _, s := range plural {
		if !readsPlural(s) {
			t.Fatalf("%q should read plural/topical", s)
		}
	}
	for _, s := range singular {
		if readsPlural(s) {
			t.Fatalf("%q should NOT read plural", s)
		}
	}
}

// --- execution-feedback repair loop ---

// queuedServer replies with the next canned completion per request.
func queuedServer(t *testing.T, replies ...string) *httptest.Server {
	t.Helper()
	i := 0
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		content := "Query: status=open"
		if i < len(replies) {
			content = replies[i]
		}
		i++
		body := map[string]any{"choices": []map[string]any{{"message": map[string]string{"content": content}}}}
		_ = json.NewEncoder(w).Encode(body)
	}))
}

func TestCompileWithRepairRevisesOnZeroRows(t *testing.T) {
	srv := queuedServer(t,
		"Query: label=reporting", // attempt 1 — wrong value, 0 rows
		"Query: parent=demo-pqr", // revision — real epic, hits
	)
	defer srv.Close()
	p := &Provider{Model: "m", URL: srv.URL + "/v1", Label: "m", client: srv.Client()}

	exec := func(q string) (int, []string, error) {
		if q == "parent=demo-pqr" {
			return 7, []string{"demo-pqr.1 A", "demo-pqr.2 B"}, nil
		}
		return 0, nil, nil // everything else returns zero rows
	}
	res, err := p.CompileWithRepair("reporting stuff", Vocab{Epics: []string{"demo-pqr Reporting and exports"}}, LexicalGrounder{}, exec)
	if err != nil {
		t.Fatal(err)
	}
	if res.Query != "parent=demo-pqr" || res.Count != 7 {
		t.Fatalf("repair should land on the hitting query: %+v", res)
	}
	if res.Repairs() != 1 {
		t.Fatalf("expected 1 repair, got %d (attempts=%+v)", res.Repairs(), res.Attempts)
	}
	if res.Attempts[0].Trigger != "zero-rows" {
		t.Fatalf("first attempt trigger = %q", res.Attempts[0].Trigger)
	}
}

func TestCompileWithRepairStopsWhenSatisfied(t *testing.T) {
	srv := queuedServer(t, "Query: status=open AND type=bug")
	defer srv.Close()
	p := &Provider{Model: "m", URL: srv.URL + "/v1", Label: "m", client: srv.Client()}
	calls := 0
	exec := func(q string) (int, []string, error) { calls++; return 5, nil, nil }
	res, err := p.CompileWithRepair("open bugs", Vocab{}, LexicalGrounder{}, exec)
	if err != nil {
		t.Fatal(err)
	}
	if res.Query != "status=open AND type=bug" || res.Count != 5 || res.Repairs() != 0 {
		t.Fatalf("satisfactory first compile should not repair: %+v", res)
	}
	if calls != 1 {
		t.Fatalf("executed %d times, want 1", calls)
	}
}

func TestCompileWithRepairKeepsBestOnPersistentFailure(t *testing.T) {
	srv := queuedServer(t,
		"Query: title=zzz", // 0 rows
		"Query: title=yyy", // 0 rows
		"Query: title=xxx", // 0 rows
	)
	defer srv.Close()
	p := &Provider{Model: "m", URL: srv.URL + "/v1", Label: "m", client: srv.Client()}
	exec := func(q string) (int, []string, error) { return 0, nil, nil }
	res, err := p.CompileWithRepair("nonexistent thing", Vocab{}, LexicalGrounder{}, exec)
	if err != nil {
		t.Fatal(err)
	}
	if res.Repairs() != 2 { // maxRepairs
		t.Fatalf("expected 2 revisions, got %d", res.Repairs())
	}
	if res.Query == "" || res.Count != 0 {
		t.Fatalf("should surface a runnable (0-row) query for the user: %+v", res)
	}
}

func TestBuildPromptCarriesGroundingSection(t *testing.T) {
	p := buildPrompt("reporting stuff", Vocab{}, promptOpts{
		mappings: []Mapping{{Term: "reporting", Hint: "parent=demo-pqr"}},
	})
	if !strings.Contains(p, "likely value mappings for THIS ask") {
		t.Fatal("grounding section missing")
	}
	if !strings.Contains(p, `"reporting" → parent=demo-pqr`) {
		t.Fatalf("mapping line missing:\n%s", p)
	}
	// A plain BuildPrompt (no mappings) must stay byte-clean of the section.
	if strings.Contains(BuildPrompt("reporting stuff", Vocab{}, nil), "likely value mappings") {
		t.Fatal("mappings must not leak into an ungrounded prompt")
	}
}

func TestAndSplitQuoteAware(t *testing.T) {
	cases := map[string][]string{
		"parent=demo-pqr AND title=exports AND priority<=1": {"parent=demo-pqr", "title=exports", "priority<=1"},
		`title="Reporting and exports" AND status=open`:     {`title="Reporting and exports"`, "status=open"},
		"status=open": {"status=open"},
	}
	for q, want := range cases {
		got := andSplit(q)
		if strings.Join(got, "|") != strings.Join(want, "|") {
			t.Fatalf("andSplit(%q) = %v, want %v", q, got, want)
		}
	}
}

func TestZeroRowDiagnosisNamesTheEmptyClause(t *testing.T) {
	exec := func(q string) (int, []string, error) {
		switch q {
		case "parent=demo-pqr":
			return 12, nil, nil
		case "priority<=1":
			return 30, nil, nil
		case "title=exports":
			return 0, nil, nil
		}
		return 0, nil, nil
	}
	d := zeroRowDiagnosis("parent=demo-pqr AND title=exports AND priority<=1", exec)
	if !strings.Contains(d, "`title=exports`") || !strings.Contains(d, "0 rows alone") {
		t.Fatalf("diagnosis should name the empty clause: %q", d)
	}
	if !strings.Contains(d, "`parent=demo-pqr` (12)") {
		t.Fatalf("diagnosis should list surviving clauses: %q", d)
	}
	// Parenthesized queries are skipped.
	if zeroRowDiagnosis("(a=1 OR b=2) AND c=3", exec) != "" {
		t.Fatal("parenthesized query must be skipped")
	}
}

// A model still emits `parent=<id> OR id=<id>` for "everything under X" (bd
// rejects that — parent is =-only, can't be OR-ed). ExtractCompiled rewrites it
// to the valid in-process `subtree=<id>` form, both orderings and parenthesized,
// so what the user reviews and bd never sees is un-applyable.
func TestExtractCompiledRewritesParentIdUnion(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"parent then id", "```query\nparent=demo-1jy OR id=demo-1jy\n```", "subtree=demo-1jy"},
		{"id then parent", "```query\nid=demo-1jy OR parent=demo-1jy\n```", "subtree=demo-1jy"},
		{"parenthesized", "```query\n(parent=app-3xz OR id=app-3xz)\n```", "subtree=app-3xz"},
		{"case-insensitive or", "```query\nparent=demo-x or id=demo-x\n```", "subtree=demo-x"},
		{"unfenced line", "Query: id=demo-9 OR parent=demo-9", "subtree=demo-9"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := ExtractCompiled(c.in)
			if !ok {
				t.Fatalf("did not extract a query from %q", c.in)
			}
			if got != c.want {
				t.Fatalf("ExtractCompiled(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
	// A genuine two-id union is NOT a subtree — left alone for bd to handle.
	got, ok := ExtractCompiled("```query\nparent=demo-1jy OR id=demo-9zz\n```")
	if !ok || got != "parent=demo-1jy OR id=demo-9zz" {
		t.Fatalf("mismatched-id union wrongly rewritten: %q (ok=%v)", got, ok)
	}
}
