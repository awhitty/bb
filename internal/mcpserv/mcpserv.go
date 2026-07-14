// Package mcpserv exposes the running TUI as an MCP server (Streamable HTTP
// on localhost) so a connected agent can drive the visible interface. Reads
// (issues/issue) never touch the screen; arranging verbs (show/select/reset/
// refresh) mutate the live UI as if the user did it and answer with an XML
// serialization of what is literally on screen.
//
// Security posture: localhost only, ever; a per-run bearer token written to
// ~/.config/bb/mcp.json (0600). v1 has no data writes — status
// changes over MCP are future work behind a consent flag.
package mcpserv

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/charmbracelet/log"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/awhitty/bb/internal/agentapi"
	"github.com/awhitty/bb/internal/bd"
	"github.com/awhitty/bb/internal/buildinfo"
	"github.com/awhitty/bb/internal/discover"
	"github.com/awhitty/bb/internal/rollup"
)

// Sender delivers an action to the UI loop and waits for its reply.
// Implemented in main via tea.Program.Send.
type Sender func(agentapi.Action) (agentapi.Response, error)

// Endpoint is what agents discover in mcp.json.
type Endpoint struct {
	URL   string `json:"url"`
	Port  int    `json:"port"`
	PID   int    `json:"pid"`
	Token string `json:"token"`
}

func endpointPath() string { return filepath.Join(discover.ConfigDir(), "mcp.json") }

// ReadEndpoint loads mcp.json (for `bb status`).
func ReadEndpoint() (Endpoint, bool) {
	var e Endpoint
	raw, err := os.ReadFile(endpointPath())
	if err != nil || json.Unmarshal(raw, &e) != nil {
		return e, false
	}
	return e, true
}

// Server is the running MCP endpoint.
type Server struct {
	Endpoint Endpoint
	send     Sender
	client   *bd.Client
	logger   *log.Logger
	httpSrv  *http.Server
	// yielded is set when a newer instance took over the port via /handoff.
	// Once yielded, mcp.json belongs to that newer instance and Stop() must
	// leave it intact.
	yielded atomic.Bool
}

// defaultMCPPort is the stable localhost port for the drive-the-board server.
const (
	defaultMCPPort = 7317
	maxHookBody    = 1 << 20
)

// boardServerInstructions is read by a connecting model every session. It
// teaches the model to OPERATE the panel (levers grouped by how much they
// disturb the screen) and to be CONSIDERATE on a screen a human is watching.
// Keep it dense and directive.
const boardServerInstructions = `bb is a live kanban / tree / relationship TUI a human is looking at RIGHT NOW. You do NOT seize their screen: view() reads what they see, and show/set_view/emphasize PUBLISH named views into the agent-shares stream — a persistent list the human pulls on their own schedule with @ (or follows, so your newest share applies live). This makes "the human outranks you" structural: you leave views; they choose when to look. issues/issue/graph/search/schema are silent reads. Start with schema() to learn the data model.

OPERATING GUIDE — the levers, grouped by how much they disturb the screen. Compose the LEAST invasive one that answers the request:
- READ (silent, never moves the screen): schema (orient here first), search (fuzzy full-text recall when you lack exact values), issues (exact bd-query field match), issue (one issue in full + its grouped neighbors), graph (transitive blockers|dependents|hierarchy). Do your research here before you touch anything.
- PUBLISH A VIEW (share a named arrangement into the stream; the human pulls it with @): show / set_view publish a view spec — mode, filter (query/ids), sort {key,dir}, collapse (roots|expand|ids), thresholds (min_subtree) — under a human-readable title so it reads in their list ("report blockers", "what to do next"). set_view(mode:"relationship", root:<id>) publishes the relationship swimlane board (one bead's sub-issues/blockers/dependents/siblings × status); set_view(mode:"columns"[, root:<id>]) publishes the Miller-columns navigator (Finder-style drill). show/set_view also take remarks:{<bead-id>:"why it's here"} — ephemeral per-bead notes that mark the card (✎) and show atop the preview/detail panels while the view is active, so a reviewer sees WHY each bead was selected; they are never written to the issue. emphasize({targets,style,label?}) publishes a decoration (highlight | marker | spotlight | outline a section). ALWAYS name your view, and pass your session id as the 'session' argument so your views land on your own channel (the human browses per-session; the newest replaces your channel's face while prior ones stay browsable). The human sees a quiet "agent shared 'X'" footer and reaches it with @ when ready.
- ACT (writes data): reprioritize(id, 0-4) is the ONE mutation you may make. Status, claim, close, assign, and edits are the human's to do in language with bd — never here.
- The human owns the timing: @ applies your latest share, @ again browses the full list; or they follow you (live apply). reset() / esc undoes an applied share.

ETIQUETTE — you are a guest on a shared screen:
- Look before you touch: call view() first to see where they are.
- Publish, don't hijack: you can only leave named views in the stream — you never seize the screen. Name each clearly so it reads in their list, and prefer a single well-named view over a flurry.
- Say what you did and why in chat — the footer shows only a terse one-line notice.
- Everything you do is reversible; say so (esc, or reset()).
- Don't thrash: one deliberate arrangement beats a flurry of them.
- The human outranks you: never fight their keystrokes. If the screen changed under you, re-read view() before acting again.
- Restore when you're done. Least surprise: make a big rearrangement only when asked; otherwise point and explain.

REVIEW REGISTER — when you ask the human to review or decide on beads:
- Ids are for agents. In agent↔human chat, speak in words and meaning — a plain topic label ("calendar bug", "report export"), never a bare bead id as the handle. The id may trail in parentheses for lookup.
- The board is context; the ask lives in chat. Publish ONE named view holding the beads, then put every question in the conversation itself: one short section per item — what it is, what you need (a decision between named options, a concrete question, or numbered drive steps they can run verbatim).
- Most important LAST. Chat surfaces the latest lines first, so order sections least-important → most-important and close with the item that matters most.
- Whitespace delineates. Blank lines between sections, a bold or italic topic label opening each — never a wall of prose or a question buried mid-paragraph.
- Ask only what the human can uniquely answer. Trivially-evidenced calls you make yourself and report; surface only genuine forks with your recommendation first.`

// StableEndpoint returns the persistent port + bearer token, generating and
// saving the token to config (0600) on first use so connecting is one-time.
// BB_MCP_PORT overrides the port at runtime (not persisted).
func StableEndpoint() (port int, token string) {
	cfg, _ := discover.LoadConfig()
	changed := false
	if cfg.MCP.Port == 0 {
		cfg.MCP.Port = defaultMCPPort
		changed = true
	}
	if cfg.MCP.Token == "" {
		b := make([]byte, 24)
		if _, err := rand.Read(b); err == nil {
			cfg.MCP.Token = hex.EncodeToString(b)
			changed = true
		}
	}
	if changed {
		_ = discover.SaveConfig(cfg) // config.toml is written 0600 by SaveConfig
	}
	port, token = cfg.MCP.Port, cfg.MCP.Token
	if p := os.Getenv("BB_MCP_PORT"); p != "" {
		if v, err := strconv.Atoi(p); err == nil && v > 0 {
			port = v
		}
	}
	return port, token
}

// StableURL is the constant endpoint clients connect to.
func StableURL() string {
	port, _ := StableEndpoint()
	return fmt.Sprintf("http://127.0.0.1:%d/mcp", port)
}

// Start serves /mcp on the STABLE localhost port with the persistent token.
// The stable port is always ended up served by THIS (the newest, focused)
// instance: if the bind fails, listenAdoptOrReplace reaps a dead incumbent's
// stale mcp.json (adopt) or asks a live incumbent to yield its listener
// (replace). Because the port is fixed and the token persistent, the static
// registration URL stays correct once this invariant holds.
func Start(send Sender, client *bd.Client, logger *log.Logger) (*Server, error) {
	port, token := StableEndpoint()
	ln, err := listenAdoptOrReplace(port, logger)
	if err != nil {
		return nil, err
	}
	s := &Server{
		Endpoint: Endpoint{
			URL:   fmt.Sprintf("http://127.0.0.1:%d/mcp", port),
			Port:  port,
			PID:   os.Getpid(),
			Token: token,
		},
		send:   send,
		client: client,
		logger: logger,
	}

	mcpServer := server.NewMCPServer("bb", buildinfo.Version(),
		server.WithToolCapabilities(false),
		server.WithInstructions(boardServerInstructions))
	s.addReadTools(mcpServer)
	s.addUITools(mcpServer)

	streamable := server.NewStreamableHTTPServer(mcpServer,
		server.WithEndpointPath("/mcp"),
		server.WithStateLess(true),
		// Request-scoped attribution: a client MAY carry its Claude Code
		// SessionID in the X-Beads-Session header, which the arranging tools
		// read as a fallback when the explicit `session` argument is absent.
		server.WithHTTPContextFunc(sessionContextFunc),
	)

	mux := http.NewServeMux()
	mux.Handle("/mcp", s.auth(streamable))
	// /ingest receives ambient name-drop batches from the Claude Code Stop hook
	// (bb hook-ingest) — the SAME localhost port + bearer token.
	mux.Handle("/ingest", s.auth(http.HandlerFunc(s.ingestHandler)))
	// /session-end archives a session's channel when a Claude Code conversation
	// ends (bb hook-end) — the SAME localhost port + bearer token as
	// /ingest, a distinct once-per-conversation trigger from the per-turn Stop hook.
	mux.Handle("/session-end", s.auth(http.HandlerFunc(s.sessionEndHandler)))
	// /handoff lets a newer instance ask this one to yield the stable port
	// (same localhost port + bearer token). We release the listener and keep
	// mcp.json for the newcomer to rewrite.
	mux.Handle("/handoff", s.auth(http.HandlerFunc(s.handoffHandler)))
	s.httpSrv = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       30 * time.Second,
	}

	if err := writeEndpoint(s.Endpoint); err != nil {
		ln.Close()
		return nil, err
	}
	go func() {
		if err := s.httpSrv.Serve(ln); err != nil && err != http.ErrServerClosed {
			logger.Error("mcp serve", "err", err)
		}
	}()
	logger.Info("mcp listening", "url", s.Endpoint.URL)
	return s, nil
}

// ServeStdio runs a HEADLESS read-only MCP server over stdio — no TUI, no
// port, no token (it inherits the spawning process's trust). It exposes only
// the silent read tools (schema, issues, issue, graph, search); there is no
// visible board to arrange. Reads BB_WORKSPACE / cwd via the client.
// Blocks until stdin closes.
func ServeStdio(client *bd.Client, logger *log.Logger) error {
	s := &Server{client: client, logger: logger}
	m := server.NewMCPServer("bb", buildinfo.Version(),
		server.WithToolCapabilities(false),
		server.WithInstructions("bb (headless): read-only access to a beads issue tracker — no visible board to drive. "+
			"Start with schema() to learn the data model (fields, the hierarchy + dependency relationships, the bd query language), then: "+
			"search(text) for fuzzy recall when you lack exact values; issues(query|ids) for exact bd-query field match; "+
			"issue(id) for one issue in full plus its grouped neighbors (add comments:true / history:true); "+
			"graph(id, relation) to walk blockers|dependents|hierarchy transitively — it answers 'what blocks X' directly. All silent, no side effects."))
	s.addReadTools(m)
	return server.ServeStdio(m)
}

// Stop shuts the endpoint down and removes mcp.json — unless a newer instance
// already took the port via /handoff, in which case mcp.json belongs to that
// instance and we leave it (and the already-shut-down listener) alone.
func (s *Server) Stop() {
	if s.yielded.Load() {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = s.httpSrv.Shutdown(ctx)
	_ = os.Remove(endpointPath())
}

// handoffHandler yields the stable port to a newer instance: it acknowledges,
// then releases the listener asynchronously so this response flushes first.
// mcp.json is deliberately left intact — the newcomer rewrites it with its own
// pid once it binds.
func (s *Server) handoffHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	s.yielded.Store(true)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"yielded":true}`))
	go func() {
		s.logger.Info("yielding MCP port to a newer instance")
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = s.httpSrv.Shutdown(ctx)
	}()
}

// pidAlive reports whether a process is running — the same liveness check
// status() uses: FindProcess never fails on Unix, so signal 0 is the probe.
func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

// retryListen polls the bind until it succeeds or the window elapses, giving a
// yielding incumbent time to release the listener.
func retryListen(addr string, within time.Duration) (net.Listener, error) {
	deadline := time.Now().Add(within)
	for {
		ln, err := net.Listen("tcp", addr)
		if err == nil {
			return ln, nil
		}
		if time.Now().After(deadline) {
			return nil, err
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// requestHandoff asks a live incumbent to release the port. It authenticates
// with the incumbent's own token (from its mcp.json), which equals ours since
// the token is persistent across runs.
func requestHandoff(ep Endpoint) error {
	url := fmt.Sprintf("http://127.0.0.1:%d/handoff", ep.Port)
	req, err := http.NewRequest(http.MethodPost, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+ep.Token)
	client := &http.Client{Timeout: 2 * time.Second}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("handoff status %d", res.StatusCode)
	}
	return nil
}

// listenAdoptOrReplace binds the stable port so that THIS instance always ends
// up serving it. A plain bind wins outright. On a busy port it inspects the
// incumbent recorded in mcp.json: a dead pid means a stale record — reap it and
// adopt the port; a live pid means a running bb — ask it to yield, then
// bind. A busy port with no bb record (some foreign process) is an
// honest error.
func listenAdoptOrReplace(port int, logger *log.Logger) (net.Listener, error) {
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	ln, err := net.Listen("tcp", addr)
	if err == nil {
		return ln, nil
	}
	ep, ok := ReadEndpoint()
	if !ok {
		return nil, fmt.Errorf("MCP port %d busy and no bb endpoint on record (foreign process?) — set BB_MCP_PORT: %w", port, err)
	}
	if !pidAlive(ep.PID) {
		logger.Info("reaping stale MCP endpoint, adopting port", "dead_pid", ep.PID, "port", port)
		_ = os.Remove(endpointPath())
		ln, rerr := retryListen(addr, 2*time.Second)
		if rerr != nil {
			return nil, fmt.Errorf("MCP port %d still busy after reaping stale endpoint (pid %d) — set BB_MCP_PORT: %w", port, ep.PID, rerr)
		}
		return ln, nil
	}
	logger.Info("MCP port held by a live bb — requesting handoff", "incumbent_pid", ep.PID, "port", port)
	if herr := requestHandoff(ep); herr != nil {
		return nil, fmt.Errorf("MCP port %d held by pid %d and handoff failed (%v) — set BB_MCP_PORT", port, ep.PID, herr)
	}
	ln, rerr := retryListen(addr, 3*time.Second)
	if rerr != nil {
		return nil, fmt.Errorf("incumbent pid %d did not release MCP port %d: %w", ep.PID, port, rerr)
	}
	logger.Info("adopted MCP port from yielding incumbent", "former_pid", ep.PID, "port", port)
	return ln, nil
}

func writeEndpoint(e Endpoint) error {
	if err := os.MkdirAll(discover.ConfigDir(), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(e, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(endpointPath(), raw, 0o600); err != nil {
		return err
	}
	return os.Chmod(endpointPath(), 0o600)
}

// ingestHandler receives one name-drop batch and marshals it onto the UI loop
// (same Program.Send path as MCP tools). Always answers fast; a stuck UI can't
// hang the hook (the send has its own timeout). Best-effort — the hook exits 0
// regardless.
func (s *Server) ingestHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxHookBody)
	var a agentapi.NameDropAction
	if err := json.NewDecoder(r.Body).Decode(&a); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"bad ingest body"}`))
		return
	}
	if len(a.IDs) == 0 {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if s.send != nil {
		if _, err := s.send(a); err != nil {
			s.logger.Debug("ingest send", "err", err)
		}
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"ok":true}`))
}

// sessionEndHandler receives one SessionEnd notice (the ended SessionID) and
// marshals it onto the UI loop, which archives that session's channel. Same
// best-effort contract as ingestHandler — the hook exits 0 regardless.
func (s *Server) sessionEndHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxHookBody)
	var a agentapi.SessionEndAction
	if err := json.NewDecoder(r.Body).Decode(&a); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"bad session-end body"}`))
		return
	}
	if a.SessionID == "" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if s.send != nil {
		if _, err := s.send(a); err != nil {
			s.logger.Debug("session-end send", "err", err)
		}
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"ok":true}`))
}

func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+s.Endpoint.Token {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"bearer token required — read it from ~/.config/bb/mcp.json"}`))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// --- per-session push attribution ---

// sessionCtxKey keys the request-scoped Claude Code SessionID in the context.
type sessionCtxKey struct{}

// sessionContextFunc lifts the SessionID from the X-Beads-Session header (if a
// client sends one) into the request context, so an arranging tool can attribute
// the push even when the explicit `session` argument is omitted. The explicit
// argument is the primary, trusted source — the header is only a fallback.
func sessionContextFunc(ctx context.Context, r *http.Request) context.Context {
	if sid := r.Header.Get("X-Beads-Session"); sid != "" {
		return context.WithValue(ctx, sessionCtxKey{}, sid)
	}
	return ctx
}

// sessionFromContext returns the request-scoped SessionID, or "" when none was
// carried on the request.
func sessionFromContext(ctx context.Context) string {
	if sid, ok := ctx.Value(sessionCtxKey{}).(string); ok {
		return sid
	}
	return ""
}

// resolveSession picks the SessionID to attribute a push to: the explicit
// `session` argument first (the same key the Stop hook registers a channel
// under, so the join is by identity), else the request-scoped header. Empty
// routes the push to the shared "unattributed" channel.
func resolveSession(ctx context.Context, args map[string]any) string {
	if sid, _ := args["session"].(string); sid != "" {
		return sid
	}
	return sessionFromContext(ctx)
}

// --- tools ---

const (
	defaultLimit   = 100 // issues() default page
	issuesMaxLimit = 500 // issues() hard cap
	maxLimit       = 200 // search() hard cap
)

// addReadTools registers the SILENT data reads — schema, issues, issue, graph,
// search. They use only the bd client, so both the HTTP (drive-the-board) and
// the headless stdio server expose them.
func (s *Server) addReadTools(m *server.MCPServer) {
	m.AddTool(mcp.NewTool("issues",
		mcp.WithDescription("SILENT read of board data — runs a bd-language query DIRECTLY (the same query language as bd, e.g. status=open AND type=bug), optionally narrowed to an explicit id list. Never touches the screen. The result ALWAYS carries total_count and truncated, so a page is never mistaken for the whole set; when truncated it also returns next_cursor and a plain-language hint — pass the cursor back to continue, or narrow the query."),
		mcp.WithString("query", mcp.Description("bd query language filter, run directly, e.g. status=open AND type=bug")),
		mcp.WithArray("ids", mcp.Description("exact issue ids to fetch"), mcp.WithStringItems()),
		mcp.WithArray("fields", mcp.Description("extra fields to include: description, notes"), mcp.WithStringItems()),
		mcp.WithNumber("limit", mcp.Description("max results (default 100, cap 500)")),
		mcp.WithString("cursor", mcp.Description("continuation cursor (next_cursor) from a previous truncated result")),
	), s.issuesTool)

	m.AddTool(mcp.NewTool("issue",
		mcp.WithDescription("SILENT read of one issue's full detail: description, notes, labels, and BOTH dependency directions (depends-on and blocked-by). Set comments/history true to include the discussion and the change trajectory. Never touches the screen."),
		mcp.WithString("id", mcp.Required(), mcp.Description("issue id")),
		mcp.WithBoolean("comments", mcp.Description("include the issue's comments")),
		mcp.WithBoolean("history", mcp.Description("include the change history (each version's date/committer/status/priority)")),
	), s.issueTool)

	m.AddTool(mcp.NewTool("schema",
		mcp.WithDescription("Orient here first. The tracker's data model with no arguments: issue fields, enum values, the two relationship kinds (hierarchy parent/child; dependency depends-on/blocked-by), the bd query language, and how these tools compose. No side effects."),
	), s.schemaTool)

	m.AddTool(mcp.NewTool("graph",
		mcp.WithDescription("Traverse an issue's graph transitively. relation=blockers (default) returns the FULL chain of what must finish before this issue, each node flagged if still open (actively blocking); dependents = what this issue blocks; hierarchy = ancestors + descendants. Silent read — answers \"what is blocking X\" directly."),
		mcp.WithString("id", mcp.Required(), mcp.Description("issue id to traverse from")),
		mcp.WithString("relation", mcp.Description("blockers (default) | dependents | hierarchy")),
		mcp.WithNumber("depth", mcp.Description("max hops out (default 20)")),
	), s.graphTool)

	m.AddTool(mcp.NewTool("search",
		mcp.WithDescription("Lexical full-text search over title, description, notes, and labels — fuzzy recall (\"anything about the door keys\") for when you don't know exact field values. Ranked, with snippets. Complements issues() (exact bd-query field match). Silent read."),
		mcp.WithString("text", mcp.Required(), mcp.Description("words to search for")),
		mcp.WithNumber("limit", mcp.Description("max results (default 20, cap 200)")),
	), s.searchTool)
}

// addUITools registers the drive-the-visible-board tools — view + the arranging
// verbs. They require the running TUI (Program.Send), so only the HTTP server
// exposes them; the headless stdio server does not (no board to arrange).
func (s *Server) addUITools(m *server.MCPServer) {
	m.AddTool(mcp.NewTool("view",
		mcp.WithDescription("Read the current screen: an XML serialization mirroring exactly what the human sees (visible cards only; hidden issues appear as counts). No side effects."),
	), s.uiTool(func(req mcp.CallToolRequest) (agentapi.Action, error) {
		return agentapi.ViewAction{}, nil
	}))

	m.AddTool(mcp.NewTool("show",
		mcp.WithDescription("PUBLISH a named view into the agent-shares stream (you do NOT seize the screen — the human pulls it with @): a bd query (validated first) and/or an explicit id list, optional mode (status|type|root|blockers|tree), and a human-readable title that labels it in their list. Optional remarks attach a short per-bead note (WHY it's here) that shows on the card and in the panels while the view is active — ephemeral, never written to the issue. Returns the published entry. If the human is following, it applies live."),
		mcp.WithString("query", mcp.Description("bd query to apply as the live filter")),
		mcp.WithArray("ids", mcp.Description("display exactly these issues"), mcp.WithStringItems()),
		mcp.WithString("title", mcp.Description("your short name for this arrangement (shown to the human)")),
		mcp.WithString("mode", mcp.Description("board mode: status|type|root|blockers|tree")),
		mcp.WithObject("remarks", mcp.Description("ephemeral per-bead notes for THIS view — a JSON object of bead id → short text (why the bead was selected). Renders as MARKDOWN in the card and panels, same styling as the issue body, so author it that way: short and structured — bold sentence-case anchors (**Blocks the release.**), numbered steps one per line, commands and identifiers in `backticks`, fenced blocks for code. Never (1)…(2) run-on enumerations; never all-caps for emphasis. Shows a ✎ on the card and the note atop the preview/detail panels while the view is active; never written to the issue."),
			func(schema map[string]any) { schema["additionalProperties"] = map[string]any{"type": "string"} }),
		mcp.WithString("session", mcp.Description("your Claude Code session id — attributes this view to your own channel so the human can browse it per-session; omit and it lands on the shared 'unattributed' channel")),
	), s.showTool)

	m.AddTool(mcp.NewTool("select",
		mcp.WithDescription("Move the focused card to an issue in the CURRENT view (errors with a hint if it isn't on screen — use show() first). Optionally open/close the preview panel. Returns the new screen XML."),
		mcp.WithString("id", mcp.Required(), mcp.Description("issue id to focus")),
		mcp.WithBoolean("panel", mcp.Description("true opens the preview panel on it, false closes the panel")),
	), s.uiTool(func(req mcp.CallToolRequest) (agentapi.Action, error) {
		id, err := req.RequireString("id")
		if err != nil {
			return nil, err
		}
		act := agentapi.SelectAction{ID: id}
		if p, ok := req.GetArguments()["panel"].(bool); ok {
			act.Panel = &p
		}
		return act, nil
	}))

	m.AddTool(mcp.NewTool("reset",
		mcp.WithDescription("Drop your arrangements and restore the human's prior view. Returns the restored screen XML."),
	), s.uiTool(func(req mcp.CallToolRequest) (agentapi.Action, error) {
		return agentapi.ResetAction{}, nil
	}))

	m.AddTool(mcp.NewTool("refresh",
		mcp.WithDescription("Reload the board from bd and return the refreshed screen XML."),
	), s.refreshTool)

	m.AddTool(mcp.NewTool("set_view",
		mcp.WithDescription("PUBLISH a named view into the agent-shares stream (you do NOT seize the screen — the human pulls it with @, or follows for live apply). The view-spec is one algebra: mode (the VIEW), traverse (how a relation view nests), group (the facet a view groups by), sort {key,dir}, plus filter (query/ids), collapse, and thresholds — under a human-readable title. mode: list | kanban | tree | relationship | columns. traverse: hierarchy | deps (the tree's and the columns navigator's nesting relation). group: "+facetVocabulary+" — the facet the active view groups by (the board's columns, the tree's per-level sections, the relationship board's column axis). Sort keys — flat views: priority|updated|created|title|id; tree/ancestor: subtree-size|aggregate-priority|open-count|max-depth|recent-activity. mode=relationship opens the relationship swimlane board rooted at `root` (default: the focused bead): a 2D matrix of one bead's sub-issues/blockers/dependents/siblings (lanes) × the group facet (columns). mode=columns opens the Miller-columns navigator (Finder-style): column 0 is the top-level beads, drilling a bead appends a column of its children (or, with traverse=deps, its dependency neighborhood) to the right; `root` pre-drills to that bead. lane adds a SECOND facet axis to the kanban, splitting each column into break-out lane bands (a columns×lanes cross-tab). scope narrows every mode to one bead's relationship neighborhood. Backward-compatible: the legacy mode values status|type|root|blockers still open a grouped board, sort_key/sort_dir still work, and lane/scope default to today's flat, unscoped board when absent. Always pass a title so the view reads in the human's shares list."),
		mcp.WithString("mode", mcp.Description("the view: list|kanban|tree|relationship|columns (legacy aliases status|type|root|blockers open a grouped board)")),
		mcp.WithString("traverse", mcp.Description("the nesting relation for the tree + columns navigator: hierarchy | deps")),
		mcp.WithString("group", mcp.Description("the facet the active view groups by: "+facetVocabulary)),
		mcp.WithString("lane", mcp.Description("kanban break-out lane facet — a SECOND board axis that splits each column into lane bands: "+facetVocabulary+" (none = the flat 1D board)")),
		mcp.WithString("root", mcp.Description("root issue id for mode=relationship (swimlane root) or mode=columns (pre-drills the Finder columns to this bead); defaults to the focused bead")),
		mcp.WithString("scope", mcp.Description("relationship-focus scope root: an issue id that narrows every mode to that bead's neighborhood (its children, blockers, dependents, siblings)")),
		mcp.WithString("query", mcp.Description("bd query filter; empty string clears it")),
		mcp.WithArray("ids", mcp.Description("show exactly these issues"), mcp.WithStringItems()),
		mcp.WithString("title", mcp.Description("your short name for this arrangement")),
		mcp.WithObject("sort", mcp.Description("two-level sort {key, dir}; dir asc|desc (defaults to the key's natural direction). Equivalent to sort_key/sort_dir."),
			mcp.Properties(map[string]any{
				"key": map[string]any{"type": "string"},
				"dir": map[string]any{"type": "string"},
			})),
		mcp.WithString("sort_key", mcp.Description("sort key (backward-compat alias for sort.key)")),
		mcp.WithString("sort_dir", mcp.Description("asc | desc (backward-compat alias for sort.dir)")),
		mcp.WithString("collapse", mcp.Description("tree collapse: 'roots' rolls up to top-level roots, 'expand' unfolds all")),
		mcp.WithArray("collapse_ids", mcp.Description("collapse exactly these node ids' subtrees"), mcp.WithStringItems()),
		mcp.WithNumber("min_subtree", mcp.Description("tree: hide trees with fewer than N descendants (0 = show all)")),
		mcp.WithObject("remarks", mcp.Description("ephemeral per-bead notes for THIS view — a JSON object of bead id → short text (why the bead was selected). Renders as MARKDOWN in the card and panels, same styling as the issue body, so author it that way: short and structured — bold sentence-case anchors (**Blocks the release.**), numbered steps one per line, commands and identifiers in `backticks`, fenced blocks for code. Never (1)…(2) run-on enumerations; never all-caps for emphasis. Shows a ✎ on the card and the note atop the preview/detail panels while the view is active; never written to the issue and gone when the human switches away."),
			func(schema map[string]any) { schema["additionalProperties"] = map[string]any{"type": "string"} }),
		mcp.WithString("session", mcp.Description("your Claude Code session id — attributes this view to your own channel so the human can browse it per-session; omit and it lands on the shared 'unattributed' channel")),
	), s.setViewTool)

	m.AddTool(mcp.NewTool("emphasize",
		mcp.WithDescription("PUBLISH an emphasis view into the agent-shares stream (the human pulls it with @; you do NOT seize the screen): decorate targets so they pop wherever they appear. targets are {kind:\"issue\",ref:<id>} or {kind:\"section\",ref:\"description|notes|related\"}. style: highlight (gutter dot) | marker (★) | outline (box a section) | spotlight (dim everything except the targets). Optional label is a short callout. Applies live only if the human is following."),
		mcp.WithArray("targets", mcp.Required(), mcp.Description("array of {kind, ref} — kind issue (ref=id) or section (ref=description|notes|related)"),
			func(s map[string]any) { s["items"] = map[string]any{"type": "object"} }),
		mcp.WithString("style", mcp.Description("highlight (default) | marker | outline | spotlight")),
		mcp.WithString("label", mcp.Description("optional short callout shown to the human")),
		mcp.WithString("session", mcp.Description("your Claude Code session id — attributes this view to your own channel so the human can browse it per-session; omit and it lands on the shared 'unattributed' channel")),
	), s.emphasizeTool)

	m.AddTool(mcp.NewTool("clear_emphasis",
		mcp.WithDescription("Drop the emphasis decoration layer (same as the human's esc). Leaves filter/sort/arrangement untouched."),
	), s.uiTool(func(req mcp.CallToolRequest) (agentapi.Action, error) {
		return agentapi.ClearEmphasisAction{}, nil
	}))

	m.AddTool(mcp.NewTool("reprioritize",
		mcp.WithDescription("Set an issue's priority (0 highest … 4 lowest) — the ONE data mutation you can make. Applied optimistically on the human's board with a footer notice, then synced via bd. Status/claim/close/assign stay in bd-in-language, NOT here."),
		mcp.WithString("id", mcp.Required(), mcp.Description("issue id")),
		mcp.WithNumber("priority", mcp.Required(), mcp.Description("0-4 (0 = highest/critical, 4 = lowest/backlog)")),
	), s.uiTool(func(req mcp.CallToolRequest) (agentapi.Action, error) {
		id, err := req.RequireString("id")
		if err != nil {
			return nil, err
		}
		pv, ok := req.GetArguments()["priority"].(float64)
		if !ok {
			return nil, fmt.Errorf("priority (0-4) is required")
		}
		p := int(pv)
		if p < 0 || p > 4 {
			return nil, fmt.Errorf("priority must be 0-4, got %d", p)
		}
		return agentapi.ReprioritizeAction{ID: id, Priority: p}, nil
	}))
}

// uiTool marshals an action onto the UI loop and shapes the reply.
func (s *Server) uiTool(build func(mcp.CallToolRequest) (agentapi.Action, error)) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		action, err := build(req)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return s.dispatch(action)
	}
}

func (s *Server) dispatch(action agentapi.Action) (*mcp.CallToolResult, error) {
	resp, err := s.send(action)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if resp.Err != "" {
		return mcp.NewToolResultError(resp.Err), nil
	}
	res := mcp.NewToolResultText(resp.Text)
	res.StructuredContent = resp.Data
	return res, nil
}

func (s *Server) showTool(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	query, _ := args["query"].(string)
	ids := stringSlice(args["ids"])
	title, _ := args["title"].(string)
	mode, _ := args["mode"].(string)
	if query == "" && len(ids) == 0 && mode == "" {
		return mcp.NewToolResultError("pass a query, ids, or a mode to arrange"), nil
	}
	switch mode {
	case "", "status", "type", "root", "blockers", "tree":
	default:
		return mcp.NewToolResultError("mode must be one of status|type|root|blockers|tree"), nil
	}
	act := agentapi.ShowAction{Query: query, IDs: ids, Title: title, Mode: mode, Remarks: stringMap(args["remarks"]), Session: resolveSession(ctx, args)}
	if query != "" {
		// Validate against bd BEFORE touching the screen; parse errors come
		// back verbatim.
		issues, err := s.client.Query(query)
		if err != nil {
			return mcp.NewToolResultError("bd rejected the query: " + err.Error()), nil
		}
		act.Issues = issues
	}
	s.logger.Info("agent show", "query", query, "ids", len(ids), "title", title, "mode", mode)
	return s.dispatch(act)
}

// validSortKeys is the union of flat + hierarchical keys accepted by set_view.
var validSortKeys = map[string]bool{
	"priority": true, "updated": true, "created": true, "title": true, "id": true,
	"subtree-size": true, "aggregate-priority": true, "open-count": true,
	"max-depth": true, "recent-activity": true,
}

// validModes is the converged VIEW vocabulary plus the legacy grouped-board
// aliases. relationship/columns additionally read `root`.
var validModes = map[string]bool{
	"list": true, "kanban": true, "tree": true, "relationship": true, "columns": true,
	// legacy aliases — a grouped board.
	"status": true, "type": true, "root": true, "blockers": true,
}

// facetVocabulary is the pipe-joined canonical facet token list shared with the
// UI (rollup.FacetBindings), for schema descriptions and validation errors. It is
// derived, not hand-listed, so the MCP's advertised facets track the UI's.
var facetVocabulary = strings.Join(rollup.FacetNames(), "|")

func (s *Server) setViewTool(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	act := agentapi.SpecAction{Session: resolveSession(ctx, args)}
	if mode, _ := args["mode"].(string); mode != "" {
		if !validModes[mode] {
			return mcp.NewToolResultError("mode must be one of list|kanban|tree|relationship|columns (or the legacy status|type|root|blockers)"), nil
		}
		act.Mode = mode
		if mode == "relationship" || mode == "columns" {
			act.Root, _ = args["root"].(string)
		}
	}
	if tr, _ := args["traverse"].(string); tr != "" {
		switch tr {
		case "hierarchy", "deps", "dependencies":
			act.Traverse = tr
		default:
			return mcp.NewToolResultError("traverse must be hierarchy or deps"), nil
		}
	}
	if g, _ := args["group"].(string); g != "" {
		if _, ok := rollup.FacetFromName(g); !ok {
			return mcp.NewToolResultError("group must be one of " + facetVocabulary), nil
		}
		act.Group = g
	}
	if ln, _ := args["lane"].(string); ln != "" {
		if _, ok := rollup.FacetFromName(ln); !ok {
			return mcp.NewToolResultError("lane must be one of " + facetVocabulary), nil
		}
		act.Lane = ln
	}
	if sc, _ := args["scope"].(string); sc != "" {
		act.Scope = sc
	}
	if q, ok := args["query"].(string); ok {
		act.Query = &q
		if q != "" {
			issues, err := s.client.Query(q)
			if err != nil {
				return mcp.NewToolResultError("bd rejected the query: " + err.Error()), nil
			}
			act.Issues = issues
		}
	}
	if ids := stringSlice(args["ids"]); len(ids) > 0 {
		act.IDs = ids
	}
	act.Title, _ = args["title"].(string)
	// sort {key,dir} is the converged shape; sort_key/sort_dir are the aliases.
	sortKey, sortDir := args["sort_key"], args["sort_dir"]
	if so, ok := args["sort"].(map[string]any); ok {
		if k, ok := so["key"]; ok {
			sortKey = k
		}
		if d, ok := so["dir"]; ok {
			sortDir = d
		}
	}
	if k, _ := sortKey.(string); k != "" {
		if !validSortKeys[k] {
			return mcp.NewToolResultError("unknown sort key " + k), nil
		}
		act.SortKey = k
		act.SortDir, _ = sortDir.(string)
	}
	if c, _ := args["collapse"].(string); c != "" || len(stringSlice(args["collapse_ids"])) > 0 {
		cs := &agentapi.CollapseSpec{NodeIDs: stringSlice(args["collapse_ids"])}
		switch c {
		case "roots":
			zero := 0
			cs.Level = &zero
		case "expand":
			cs.ExpandAll = true
		}
		act.Collapse = cs
	}
	if v, ok := args["min_subtree"].(float64); ok {
		n := int(v)
		act.MinSubtree = &n
	}
	// Remarks are additive annotations ON the view, not a view knob — they don't
	// satisfy the "pass at least one" guard below (a remark needs a view to sit on).
	act.Remarks = stringMap(args["remarks"])
	if act.Mode == "" && act.Traverse == "" && act.Group == "" && act.Lane == "" &&
		act.Scope == "" && act.Query == nil && act.IDs == nil && act.SortKey == "" &&
		act.Collapse == nil && act.MinSubtree == nil {
		return mcp.NewToolResultError("pass at least one of mode/traverse/group/lane/scope/query/ids/sort/collapse/min_subtree"), nil
	}
	s.logger.Info("agent set_view", "mode", act.Mode, "traverse", act.Traverse, "group", act.Group, "lane", act.Lane, "scope", act.Scope, "sort", act.SortKey, "min_subtree", act.MinSubtree)
	return s.dispatch(act)
}

func (s *Server) emphasizeTool(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	style, _ := args["style"].(string)
	if style == "" {
		style = "highlight"
	}
	switch style {
	case "highlight", "marker", "outline", "spotlight":
	default:
		return mcp.NewToolResultError("style must be highlight|marker|outline|spotlight"), nil
	}
	label, _ := args["label"].(string)
	raw, ok := args["targets"].([]any)
	if !ok || len(raw) == 0 {
		return mcp.NewToolResultError("targets must be a non-empty array of {kind, ref}"), nil
	}
	var targets []agentapi.Emphasis
	for _, t := range raw {
		obj, ok := t.(map[string]any)
		if !ok {
			continue
		}
		kind, _ := obj["kind"].(string)
		ref, _ := obj["ref"].(string)
		if kind == "" {
			kind = "issue"
		}
		if kind != "issue" && kind != "section" {
			return mcp.NewToolResultError("target kind must be issue or section"), nil
		}
		if ref == "" {
			return mcp.NewToolResultError("each target needs a ref (issue id, or section name)"), nil
		}
		targets = append(targets, agentapi.Emphasis{Kind: kind, Ref: ref, Style: style, Label: label})
	}
	if len(targets) == 0 {
		return mcp.NewToolResultError("no valid targets — each is {kind, ref}"), nil
	}
	s.logger.Info("agent emphasize", "style", style, "targets", len(targets))
	return s.dispatch(agentapi.EmphasizeAction{Targets: targets, Session: resolveSession(ctx, args)})
}

func (s *Server) refreshTool(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	// The fetch happens here, off the UI thread, so the returned view is
	// genuinely post-refresh.
	issues, err := s.client.List(true)
	if err != nil {
		return mcp.NewToolResultError("bd list failed: " + err.Error()), nil
	}
	return s.dispatch(agentapi.RefreshAction{Issues: issues})
}

func (s *Server) issuesTool(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	query, _ := args["query"].(string)
	ids := stringSlice(args["ids"])
	fields := stringSlice(args["fields"])
	limit := defaultLimit
	if l, ok := args["limit"].(float64); ok && l > 0 {
		limit = int(l)
	}
	if limit > issuesMaxLimit {
		limit = issuesMaxLimit
	}
	offset := 0
	if c, ok := args["cursor"].(string); ok && c != "" {
		if v, err := strconv.Atoi(c); err == nil && v >= 0 {
			offset = v
		}
	}

	var list []bd.Issue
	var err error
	if query != "" {
		list, err = s.client.Query(query)
	} else {
		list, err = s.client.List(true)
	}
	if err != nil {
		return mcp.NewToolResultError("bd: " + err.Error()), nil
	}
	if len(ids) > 0 {
		want := map[string]bool{}
		for _, id := range ids {
			want[id] = true
		}
		var filtered []bd.Issue
		for _, is := range list {
			if want[is.ID] {
				filtered = append(filtered, is)
			}
		}
		list = filtered
	}
	sort.Slice(list, func(a, b int) bool { return list[a].ID < list[b].ID })

	out := paginateIssues(list, offset, limit, has(fields, "description"), has(fields, "notes"))
	raw, _ := json.MarshalIndent(out, "", " ")
	res := mcp.NewToolResultText(string(raw))
	res.StructuredContent = out
	return res, nil
}

// paginateIssues slices the sorted list to the [offset, offset+limit) page and
// builds the issues() payload. total_count and truncated are ALWAYS present so an
// agent can never read a page as the whole set; when more remains it adds
// next_cursor and a plain-language hint to continue or narrow. Pure (no client),
// so the pagination contract is unit-testable.
func paginateIssues(list []bd.Issue, offset, limit int, withDesc, withNotes bool) map[string]any {
	total := len(list)
	if offset < 0 {
		offset = 0
	}
	end := offset + limit
	if end > total {
		end = total
	}
	page := []bd.Issue{}
	if offset < total {
		page = list[offset:end]
	}
	items := make([]map[string]any, 0, len(page))
	for _, is := range page {
		items = append(items, issueJSON(is, withDesc, withNotes, nil))
	}
	out := map[string]any{
		"issues":      items,
		"total_count": total,
		"truncated":   end < total,
	}
	if end < total {
		out["next_cursor"] = strconv.Itoa(end)
		out["hint"] = fmt.Sprintf("%d of %d — pass cursor to continue, or narrow the query", end, total)
	}
	return out
}

func (s *Server) issueTool(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id, err := req.RequireString("id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	is, err := s.client.Show(id)
	if err != nil {
		return mcp.NewToolResultError("bd: " + err.Error()), nil
	}
	// Dependents (blocked-by) need the reverse index over the whole board.
	all, err := s.client.List(true)
	if err != nil {
		return mcp.NewToolResultError("bd: " + err.Error()), nil
	}
	var dependents []string
	for _, other := range all {
		for _, dep := range bd.BlockerIDs(other) {
			if dep == id {
				dependents = append(dependents, other.ID)
			}
		}
	}
	out := issueJSON(is, true, true, dependents)
	if is.CommentCount > 0 {
		out["comment_count"] = is.CommentCount
	}
	// Enriched neighborhood so the agent can reason about neighbors without a
	// second round-trip: each relation carries id + title + status (blockers
	// flag whether they're still open, i.e. actively blocking).
	if nb := neighbors(is, all, dependents); len(nb) > 0 {
		out["neighbors"] = nb
	}

	args := req.GetArguments()
	if b, _ := args["comments"].(bool); b {
		comments, err := s.client.Comments(id)
		if err != nil {
			return mcp.NewToolResultError("bd comments: " + err.Error()), nil
		}
		out["comments"] = comments
	}
	if b, _ := args["history"].(bool); b {
		history, err := s.client.History(id)
		if err != nil {
			return mcp.NewToolResultError("bd history: " + err.Error()), nil
		}
		out["history"] = history
	}

	raw, _ := json.MarshalIndent(out, "", " ")
	res := mcp.NewToolResultText(string(raw))
	res.StructuredContent = out
	return res, nil
}

func issueJSON(is bd.Issue, withDesc, withNotes bool, dependents []string) map[string]any {
	out := map[string]any{
		"id":       is.ID,
		"title":    is.Title,
		"type":     is.IssueType,
		"priority": is.Priority,
		"status":   is.Status,
	}
	if len(is.Labels) > 0 {
		out["labels"] = is.Labels
	}
	if is.Parent != "" {
		out["parent"] = is.Parent
	}
	if deps := bd.BlockerIDs(is); len(deps) > 0 {
		out["depends_on"] = deps
	}
	if dependents != nil {
		out["blocked_by_this"] = dependents
	}
	if is.Assignee != "" {
		out["assignee"] = is.Assignee
	}
	if is.UpdatedAt != "" {
		out["updated_at"] = is.UpdatedAt
	}
	if withDesc && strings.TrimSpace(is.Description) != "" {
		out["description"] = is.Description
	}
	if withNotes && strings.TrimSpace(is.Notes) != "" {
		out["notes"] = is.Notes
	}
	return out
}

// stringMap decodes a JSON object of string→string (e.g. the remarks map, bead id
// → note text), dropping non-string values and empty keys/values. nil for a
// non-object or an empty result, so an omitted `remarks` leaves the action's field
// nil (additive — old callers are unaffected).
func stringMap(v any) map[string]string {
	obj, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	out := make(map[string]string, len(obj))
	for k, x := range obj {
		s, ok := x.(string)
		if !ok || k == "" || strings.TrimSpace(s) == "" {
			continue
		}
		out[k] = s
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func stringSlice(v any) []string {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	var out []string
	for _, x := range arr {
		if s, ok := x.(string); ok && s != "" {
			out = append(out, s)
		}
	}
	return out
}

func has(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
