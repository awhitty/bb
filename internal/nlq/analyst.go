package nlq

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/awhitty/bb/internal/bd"
)

// The analyst answers semantic whole-board questions with a BIG local model
// (MoE — fast decode), complementing the small compiler behind `?`. The
// request is a STABLE PREFIX (instructions + a compact rendering of the
// whole board) with the question appended at the very end: byte-identical
// prefixes let the server's prefix/KV cache turn every ask after the first
// into a short prefill.

// Analyst is a streaming client for the analyst model.
type Analyst struct {
	Model string
	URL   string
	Label string
	Key   string // optional bearer token (BB_ANALYST_KEY)

	client *http.Client
}

func NewAnalyst() *Analyst {
	model := os.Getenv("BB_ANALYST_MODEL")
	if model == "" {
		model = "Qwen3.6-35B-A3B-UD-MLX-4bit"
	}
	url := os.Getenv("BB_ANALYST_URL")
	if url == "" {
		url = os.Getenv("BB_NLQ_URL")
	}
	if url == "" {
		url = "http://127.0.0.1:8000/v1"
	}
	return &Analyst{
		Model:  model,
		URL:    url,
		Label:  LabelFor(model),
		Key:    firstNonEmpty(os.Getenv("BB_ANALYST_KEY"), os.Getenv("BB_NLQ_KEY")),
		client: &http.Client{Timeout: 10 * time.Minute},
	}
}

// descLimit is how much of each description survives into the board context.
const descLimit = 120

// compactLine renders one issue as a single stable line:
// id | type | P# | status | labels | parent=x | dep→a,b | title | desc…
func compactLine(is bd.Issue) string {
	parts := []string{is.ID, is.IssueType, fmt.Sprintf("P%d", is.Priority), is.Status}
	if len(is.Labels) > 0 {
		parts = append(parts, strings.Join(is.Labels, ","))
	}
	if is.Parent != "" {
		parts = append(parts, "parent="+is.Parent)
	}
	if deps := bd.BlockerIDs(is); len(deps) > 0 {
		parts = append(parts, "dep→"+strings.Join(deps, ","))
	}
	parts = append(parts, is.Title)
	if desc := flattenText(is.Description); desc != "" {
		r := []rune(desc)
		if len(r) > descLimit {
			desc = string(r[:descLimit]) + "…"
		}
		parts = append(parts, desc)
	}
	return strings.Join(parts, " | ")
}

func flattenText(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// BoardContext renders the whole board compactly, sorted by id — the
// ordering is what keeps the prefix byte-identical between asks so the
// server's KV cache can reuse it. Rebuild only on board refresh.
func BoardContext(issues []bd.Issue) string {
	sorted := append([]bd.Issue(nil), issues...)
	sort.Slice(sorted, func(a, b int) bool { return sorted[a].ID < sorted[b].ID })
	lines := make([]string, 0, len(sorted))
	for _, is := range sorted {
		lines = append(lines, compactLine(is))
	}
	return strings.Join(lines, "\n")
}

const analystInstructions = `You are the analyst for a bd issue board. Answer the question briefly and concretely, grounding every claim in bead ids from the board below. Then END your answer with a fenced block of the matching bead ids, exactly like:

` + "```ids\ndemo-xxx demo-yyy\n```" + `

If nothing matches, end with an empty ids block.

BOARD — one line per bead: id | type | priority | status | labels | parent | deps | title | description
`

// AnalystPrefix is the stable part of every ask: instructions + board.
func AnalystPrefix(board string) string {
	return analystInstructions + board
}

// StreamEvent is one step of a streamed answer.
type StreamEvent struct {
	Chunk string
	Done  bool
	Err   error
}

// Ask streams the answer for one question over the stable prefix. The
// returned channel closes after a Done or Err event.
func (a *Analyst) Ask(prefix, question string) <-chan StreamEvent {
	if a.client == nil {
		a.client = &http.Client{Timeout: 10 * time.Minute}
	}
	ch := make(chan StreamEvent, 16)
	prompt := prefix + "\n\nQuestion: " + question + "\nAnswer:"
	go func() {
		defer close(ch)
		payload, err := json.Marshal(map[string]any{
			"model":       a.Model,
			"messages":    []map[string]string{{"role": "user", "content": prompt}},
			"temperature": 0.2,
			"max_tokens":  900,
			"stream":      true,
			// Best-effort thinking suppression (see Provider.chat).
			"chat_template_kwargs": map[string]any{"enable_thinking": false},
		})
		if err != nil {
			ch <- StreamEvent{Err: err}
			return
		}
		req, err := http.NewRequest("POST", a.URL+"/chat/completions", bytes.NewReader(payload))
		if err != nil {
			ch <- StreamEvent{Err: err}
			return
		}
		req.Header.Set("Content-Type", "application/json")
		if a.Key != "" {
			req.Header.Set("Authorization", "Bearer "+a.Key)
		}
		res, err := a.client.Do(req)
		if err != nil {
			ch <- StreamEvent{Err: fmt.Errorf(
				"analyst server unreachable at %s — start one: omlx serve --port 8001 (models in ~/.omlx/models)", a.URL)}
			return
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusOK {
			body := make([]byte, 1024)
			n, _ := res.Body.Read(body)
			ch <- StreamEvent{Err: serverError("analyst", res.StatusCode, body[:n])}
			return
		}
		sc := bufio.NewScanner(res.Body)
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if data == "[DONE]" {
				break
			}
			var ev struct {
				Choices []struct {
					Delta struct {
						Content string `json:"content"`
					} `json:"delta"`
				} `json:"choices"`
			}
			if json.Unmarshal([]byte(data), &ev) != nil {
				continue
			}
			if len(ev.Choices) > 0 && ev.Choices[0].Delta.Content != "" {
				ch <- StreamEvent{Chunk: ev.Choices[0].Delta.Content}
			}
		}
		if err := sc.Err(); err != nil {
			ch <- StreamEvent{Err: err}
			return
		}
		ch <- StreamEvent{Done: true}
	}()
	return ch
}

var idsBlockRe = regexp.MustCompile("(?s)```ids[^\n]*\n(.*?)```")

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// ParseIDs extracts the final fenced ids block from a finished answer.
func ParseIDs(answer string) []string {
	matches := idsBlockRe.FindAllStringSubmatch(answer, -1)
	if len(matches) == 0 {
		return nil
	}
	fields := strings.Fields(matches[len(matches)-1][1])
	seen := map[string]bool{}
	var out []string
	for _, f := range fields {
		f = strings.Trim(f, ",")
		if f == "" || seen[f] {
			continue
		}
		seen[f] = true
		out = append(out, f)
	}
	return out
}
