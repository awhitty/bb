package hook

import (
	"bufio"
	"bytes"
	"encoding/json"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/awhitty/bb/internal/agentapi"
)

// splitLines yields the JSONL lines of a transcript, trimmed (the same buffered
// scan LatestAssistantText uses, so a big turn never overflows).
func splitLines(transcript []byte) [][]byte {
	var out [][]byte
	sc := bufio.NewScanner(bytes.NewReader(transcript))
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		out = append(out, append([]byte(nil), bytes.TrimSpace(sc.Bytes())...))
	}
	return out
}

// excerpt.go turns a raw assistant turn into the CONTEXT a batch of name-drops
// carries: the prose around each mentioned bead id (so the human sees WHY the
// beads came up), and a human-readable name for the CONVERSATION they came from.
// Bounded and dependency-light — the hook must stay fast.

const (
	// excerptContext is how many bytes of prose each side of a mention (or
	// mention cluster) an excerpt carries.
	excerptContext = 120
	// maxExcerpts caps how many excerpts one batch sends.
	maxExcerpts = 6
	// convoNameLen is the truncation for a derived conversation name.
	convoNameLen = 60
)

// occ is one grounded mention occurrence: its full id and byte span in the text.
type occ struct {
	id         string
	start, end int
}

// findMentions returns every occurrence of a real board id in text (full form
// <prefix><body> or the short body when prefix+body is real), with byte spans,
// sorted by position and de-overlapped (an id nested in a longer one — pqr
// inside pqr.4 — yields only the longer match).
func findMentions(text string, boardIDs []string, prefix string) []occ {
	real := make(map[string]bool, len(boardIDs))
	for _, id := range boardIDs {
		real[strings.ToLower(id)] = true
	}
	var occs []occ
	if prefix != "" {
		full := regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(prefix) + `(?:` + idBody + `)`)
		for _, loc := range full.FindAllStringIndex(text, -1) {
			s, e := loc[0], loc[1]
			for e > s && text[e-1] == '.' { // a trailing dot isn't part of the id
				e--
			}
			if id := strings.ToLower(text[s:e]); real[id] {
				occs = append(occs, occ{id: id, start: s, end: e})
			}
		}
		// Bare short ids: existence against the board is the sole guard (a plain
		// word resolves to a non-existent id and is dropped; a real all-alpha core
		// like "mnp" is kept).
		short := regexp.MustCompile(`(?i)\b(?:` + idBody + `)\b`)
		for _, loc := range short.FindAllStringIndex(text, -1) {
			s, e := loc[0], loc[1]
			for e > s && text[e-1] == '.' {
				e--
			}
			body := strings.ToLower(text[s:e])
			if cand := prefix + body; real[cand] {
				occs = append(occs, occ{id: cand, start: s, end: e})
			}
		}
	}
	sort.Slice(occs, func(i, j int) bool {
		if occs[i].start != occs[j].start {
			return occs[i].start < occs[j].start
		}
		return occs[i].end > occs[j].end // longer first at the same start
	})
	// Drop occurrences that overlap one already kept (the short match nested in a
	// full one, or a duplicate at the same span).
	kept := occs[:0]
	var lastEnd int
	for _, o := range occs {
		if len(kept) > 0 && o.start < lastEnd {
			continue
		}
		kept = append(kept, o)
		lastEnd = o.end
	}
	return kept
}

// ExtractExcerpts returns the bounded set of excerpts for a turn: mentions whose
// context windows touch are merged into one excerpt, each capped near
// excerptContext on either side and its Mentions marked with byte spans into the
// excerpt's (whitespace-collapsed) Text. Returns at most maxExcerpts, in reading
// order. Empty when the turn names no real bead.
func ExtractExcerpts(text string, boardIDs []string, prefix string) []agentapi.Excerpt {
	occs := findMentions(text, boardIDs, prefix)
	if len(occs) == 0 {
		return nil
	}
	// Cluster occurrences whose context windows overlap into one excerpt.
	type cluster struct{ occs []occ }
	var clusters []cluster
	for _, o := range occs {
		if n := len(clusters); n > 0 {
			prev := clusters[n-1].occs[len(clusters[n-1].occs)-1]
			if o.start-prev.end <= 2*excerptContext {
				clusters[n-1].occs = append(clusters[n-1].occs, o)
				continue
			}
		}
		clusters = append(clusters, cluster{occs: []occ{o}})
	}
	if len(clusters) > maxExcerpts {
		clusters = clusters[:maxExcerpts]
	}

	out := make([]agentapi.Excerpt, 0, len(clusters))
	for _, cl := range clusters {
		first, last := cl.occs[0], cl.occs[len(cl.occs)-1]
		winStart := snapStart(text, first.start-excerptContext, first.start)
		winEnd := snapEnd(text, last.end+excerptContext, last.end)
		clean, mapIdx := normalizeExcerpt(text[winStart:winEnd])
		prefixEllipsis := ""
		if winStart > 0 {
			prefixEllipsis = "… "
		}
		ex := agentapi.Excerpt{}
		off := len(prefixEllipsis)
		for _, o := range cl.occs {
			ns := mapIdx[o.start-winStart] + off
			ne := mapIdx[o.end-winStart] + off
			ex.Mentions = append(ex.Mentions, agentapi.Mention{ID: o.id, Start: ns, End: ne})
		}
		ex.Text = prefixEllipsis + clean
		if winEnd < len(text) {
			ex.Text += " …"
		}
		out = append(out, ex)
	}
	return out
}

// snapStart moves a window's left edge forward to a word boundary (past a
// partial leading word), never past limit (the first mention's start).
func snapStart(text string, at, limit int) int {
	if at <= 0 {
		return 0
	}
	if at >= limit {
		return limit
	}
	// Advance to just after the next space, so the excerpt begins at a word.
	for i := at; i < limit; i++ {
		if isSpace(text[i]) {
			return i + 1
		}
	}
	return limit
}

// snapEnd moves a window's right edge back to a word boundary (before a partial
// trailing word), never before limit (the last mention's end).
func snapEnd(text string, at, limit int) int {
	if at >= len(text) {
		return len(text)
	}
	if at <= limit {
		return limit
	}
	for i := at; i > limit; i-- {
		if isSpace(text[i]) {
			return i
		}
	}
	return limit
}

func isSpace(b byte) bool { return b == ' ' || b == '\t' || b == '\n' || b == '\r' }

// normalizeExcerpt collapses runs of whitespace in sub to single spaces (and
// trims the ends), returning the clean text plus a map from each byte index in
// sub to its index in the clean text — so a mention's span survives the
// collapse. mapIdx has len(sub)+1 entries.
func normalizeExcerpt(sub string) (clean string, mapIdx []int) {
	var b strings.Builder
	mapIdx = make([]int, len(sub)+1)
	prevSpace := true // true so leading whitespace is trimmed
	for i := 0; i < len(sub); i++ {
		mapIdx[i] = b.Len()
		c := sub[i]
		if isSpace(c) {
			if !prevSpace {
				b.WriteByte(' ')
			}
			prevSpace = true
			continue
		}
		b.WriteByte(c)
		prevSpace = false
	}
	mapIdx[len(sub)] = b.Len()
	clean = strings.TrimRight(b.String(), " ")
	// A trailing space we just trimmed must not leave a mapped index past the end.
	for i := range mapIdx {
		if mapIdx[i] > len(clean) {
			mapIdx[i] = len(clean)
		}
	}
	return clean, mapIdx
}

// --- conversation name ---

// ConvoName derives the best human-readable name for the conversation a batch
// came from. Claude Code (current) writes no summary/title into the transcript,
// so it uses the first real user prompt (cleaned + truncated); failing that, the
// project basename + a short session id; failing that, a short session id alone.
func ConvoName(transcript []byte, sessionID string) string {
	if name := firstUserPrompt(transcript); name != "" {
		return name
	}
	base := filepath.Base(strings.TrimRight(scanCwd(transcript), "/"))
	short := shortSession(sessionID)
	switch {
	case base != "" && base != "." && base != "/" && short != "":
		return base + " · " + short
	case base != "" && base != "." && base != "/":
		return base
	case short != "":
		return "session " + short
	}
	return "agent session"
}

func shortSession(id string) string {
	if i := strings.IndexByte(id, '-'); i > 0 {
		return id[:i] // the first uuid group
	}
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// firstUserPrompt returns the first genuine user prose in the transcript,
// cleaned and truncated. It skips the noise a real transcript opens with: slash
// command wrappers, local-command caveats, compaction preambles, system
// reminders, and meta/tool messages.
func firstUserPrompt(transcript []byte) string {
	for _, raw := range splitLines(transcript) {
		var tl transcriptLine
		if len(raw) == 0 || raw[0] != '{' || json.Unmarshal(raw, &tl) != nil {
			continue
		}
		if tl.Type != "user" {
			continue
		}
		var msg message
		if len(tl.Message) > 0 {
			_ = json.Unmarshal(tl.Message, &msg)
		}
		if msg.Role != "" && msg.Role != "user" {
			continue
		}
		txt := textOf(msg.Content)
		if name := cleanPrompt(txt); name != "" {
			return name
		}
	}
	return ""
}

// cleanPrompt rejects the wrapper/meta forms a first user message can take and
// otherwise collapses whitespace + truncates to a name. Returns "" to reject.
func cleanPrompt(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	rejectPrefixes := []string{
		"<command-name>", "<command-message>", "<local-command-",
		"<system-reminder>", "Caveat:",
		"This session is being continued from a previous conversation",
	}
	for _, p := range rejectPrefixes {
		if strings.HasPrefix(s, p) {
			return ""
		}
	}
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) > convoNameLen {
		return strings.TrimSpace(string(r[:convoNameLen])) + "…"
	}
	return s
}

// scanCwd returns the first cwd recorded in the transcript ("" if none).
func scanCwd(transcript []byte) string {
	for _, raw := range splitLines(transcript) {
		if len(raw) == 0 || raw[0] != '{' {
			continue
		}
		var line struct {
			Cwd string `json:"cwd"`
		}
		if json.Unmarshal(raw, &line) == nil && line.Cwd != "" {
			return line.Cwd
		}
	}
	return ""
}
