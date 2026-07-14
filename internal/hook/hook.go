// Package hook implements the Claude Code Stop-hook integration: it reads the
// hook payload, pulls the latest assistant turn out of the transcript, extracts
// the workspace bead ids the agent name-dropped, and POSTs them to a running
// bb so the human sees what the agent is attending to. Dependency-light
// and fast — the hook must never slow or fail an agent's turn.
package hook

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"regexp"
	"sort"
	"strings"
)

// Payload is the subset of the Claude Code Stop-hook stdin JSON we use.
type Payload struct {
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
}

// transcriptLine is one JSONL event. Claude Code writes assistant turns as
// {"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"…"}]}}.
// We parse defensively (type OR nested role; content as array of text blocks or
// a bare string) so a format tweak degrades to "no ids", never a crash.
type transcriptLine struct {
	Type    string          `json:"type"`
	Message json.RawMessage `json:"message"`
}

type message struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

// LatestAssistantText returns the concatenated text of the LAST assistant turn
// in a JSONL transcript. Empty when there is none.
func LatestAssistantText(transcript []byte) string {
	last := ""
	sc := bufio.NewScanner(bytes.NewReader(transcript))
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024) // turns can be large
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var tl transcriptLine
		if json.Unmarshal(line, &tl) != nil {
			continue
		}
		var msg message
		if len(tl.Message) > 0 {
			_ = json.Unmarshal(tl.Message, &msg)
		}
		role := msg.Role
		if role == "" {
			role = tl.Type
		}
		if role != "assistant" {
			continue
		}
		if txt := textOf(msg.Content); txt != "" {
			last = txt
		}
	}
	return last
}

// textOf pulls plain text from a content field that is either a JSON string or
// an array of {type,text} blocks.
func textOf(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) == nil {
		var parts []string
		for _, b := range blocks {
			if b.Text != "" {
				parts = append(parts, b.Text)
			}
		}
		return strings.Join(parts, " ")
	}
	return ""
}

// idBody is the SHAPE of a bead id's core: a 2+ char alphanumeric stem plus
// optional dotted numeric segments (rst, pqr.4, mnp, xyz.6.4). It deliberately
// does NOT match a 1-char or purely-numeric stem, so a bare "3.5" version string
// or "v1.2" is excluded by the shape. The shape is intentionally loose beyond
// that — the false-positive guard is the EXISTENCE CHECK against the live board,
// not the regex, so a real all-alpha core like "mnp" still counts.
var idBody = `[a-z0-9]{2,}(?:\.[0-9]+)*`

// ExtractBeadIDs finds the workspace bead ids the agent mentioned, GROUNDED
// against the real board so "demo-based" or a stray word never counts. It
// accepts both the full form (prefix + body, e.g. demo-pqr.4) and the bare
// short form (pqr.4, mnp) when prefix+short is a real id. Matching is
// case-insensitive (board ids are lowercase). Returns FULL ids, deduped and
// sorted. prefix is the board's common "<prefix>-"; boardIDs is every real id on
// the board.
func ExtractBeadIDs(text string, boardIDs []string, prefix string) []string {
	real := make(map[string]bool, len(boardIDs))
	for _, id := range boardIDs {
		real[strings.ToLower(id)] = true
	}
	found := map[string]bool{}

	// Full ids: <prefix><body>. Only keep ones that are real.
	if prefix != "" {
		full := regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(prefix) + `(?:` + idBody + `)`)
		for _, m := range full.FindAllString(text, -1) {
			m = strings.ToLower(strings.TrimRight(m, "."))
			if real[m] {
				found[m] = true
			}
		}
	}
	// Bare short ids (prefix stripped): a candidate counts iff prefix+candidate is
	// a real board id. Existence is the SOLE false-positive guard — a plain word
	// like "work" resolves to a non-existent id and is dropped, while a real
	// all-alpha core like "mnp" is kept.
	if prefix != "" {
		short := regexp.MustCompile(`(?i)\b(` + idBody + `)\b`)
		for _, m := range short.FindAllStringSubmatch(text, -1) {
			body := strings.ToLower(strings.TrimRight(m[1], "."))
			if cand := prefix + body; real[cand] {
				found[cand] = true
			}
		}
	}

	out := make([]string, 0, len(found))
	for id := range found {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// CommonPrefix returns the "<prefix>-" shared by EVERY id (e.g. "demo-"), or
// "" if ids are mixed/unprefixed — the same rule the display uses.
func CommonPrefix(ids []string) string {
	if len(ids) == 0 {
		return ""
	}
	dash := strings.IndexByte(ids[0], '-')
	if dash < 0 {
		return ""
	}
	p := ids[0][:dash+1]
	for _, id := range ids {
		if !strings.HasPrefix(id, p) {
			return ""
		}
	}
	return p
}

// Snippet is a compact one-line excerpt of the assistant text for the batch
// notice — whitespace-collapsed, clipped.
func Snippet(text string, max int) string {
	s := strings.Join(strings.Fields(text), " ")
	if len(s) > max {
		r := []rune(s)
		if len(r) > max {
			s = string(r[:max]) + "…"
		}
	}
	return s
}

// ReadPayload parses the Stop-hook JSON from an io stream (stdin).
func ReadPayload(raw []byte) (Payload, error) {
	var p Payload
	err := json.Unmarshal(raw, &p)
	return p, err
}

// ReadTranscript loads the transcript file named in the payload (best-effort).
func ReadTranscript(path string) ([]byte, error) {
	if path == "" {
		return nil, os.ErrNotExist
	}
	return os.ReadFile(path)
}
