package mcpserv

import (
	"bytes"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/log"

	"github.com/awhitty/bb/internal/agentapi"
	"github.com/awhitty/bb/internal/discover"
)

// freePort grabs a currently-unused localhost port for the stable endpoint under
// test (BB_MCP_PORT), so the test never collides with a real running TUI
// on the default 7317.
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve a free port: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return port
}

// postIngest sends one name-drop batch to the stable port with the persistent
// bearer token — the SAME localhost seam the Claude Code Stop hook uses — and
// returns the HTTP status. It is how the test drives "which instance is actually
// serving the stable port" without an MCP handshake.
func postIngest(t *testing.T, port int, token, session string) int {
	t.Helper()
	body := fmt.Sprintf(`{"session_id":%q,"ts":"2026-07-08T10:00:00Z","ids":["a"]}`, session)
	req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("http://127.0.0.1:%d/ingest", port), bytes.NewReader([]byte(body)))
	if err != nil {
		t.Fatalf("build ingest request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := (&http.Client{Timeout: 2 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("post ingest: %v", err)
	}
	defer res.Body.Close()
	return res.StatusCode
}

// TestStableEndpointTokenPersistsAcrossRestarts proves the bearer token survives
// a restart: the token is generated once, written to config.toml (0600), and the
// next process reads the SAME token from the same fixed port — so a client that
// connected once with the static URL + token stays authorized across restarts.
func TestStableEndpointTokenPersistsAcrossRestarts(t *testing.T) {
	t.Setenv("BB_CONFIG_DIR", t.TempDir())
	t.Setenv("BB_MCP_PORT", "") // exercise the persisted port, not an override

	// First run: generates + persists the port and token.
	port1, tok1 := StableEndpoint()
	if tok1 == "" {
		t.Fatal("first run produced no token")
	}
	if port1 != defaultMCPPort {
		t.Fatalf("first run port = %d, want the stable default %d", port1, defaultMCPPort)
	}

	// A second, independent read (the restart) must return the SAME token + port —
	// read back from config.toml, not freshly minted.
	port2, tok2 := StableEndpoint()
	if tok2 != tok1 {
		t.Fatalf("token changed across restarts: %q -> %q — a reconnecting client would 401", tok1, tok2)
	}
	if port2 != port1 {
		t.Fatalf("port changed across restarts: %d -> %d", port1, port2)
	}

	// The persistence is genuinely on disk: a fresh LoadConfig sees the same token.
	cfg, ok := discover.LoadConfig()
	if !ok || cfg.MCP.Token != tok1 || cfg.MCP.Port != port1 {
		t.Fatalf("config.toml did not persist the endpoint: ok=%v cfg.MCP=%+v", ok, cfg.MCP)
	}
}

// TestAdoptOrReplaceLiveIncumbentServesNewest drives the adopt-or-replace
// mechanism end-to-end with two real servers on the stable port: the first binds
// it; the second (the newest/focused instance) can't bind, so it asks the
// incumbent to yield over /handoff, then takes the listener. The stable port ends
// up serving the LIVE (second) instance — proven by a name-drop landing on the
// second server's sender, not the first — and mcp.json is rewritten so the static
// registration never goes stale.
func TestAdoptOrReplaceLiveIncumbentServesNewest(t *testing.T) {
	t.Setenv("BB_CONFIG_DIR", t.TempDir())
	port := freePort(t)
	t.Setenv("BB_MCP_PORT", strconv.Itoa(port))

	var mu sync.Mutex
	var served1, served2 []string
	sender := func(dst *[]string) Sender {
		return func(a agentapi.Action) (agentapi.Response, error) {
			if nd, ok := a.(agentapi.NameDropAction); ok {
				mu.Lock()
				*dst = append(*dst, nd.SessionID)
				mu.Unlock()
			}
			return agentapi.Response{Text: "ok"}, nil
		}
	}

	// The incumbent binds the stable port first.
	s1, err := Start(sender(&served1), nil, log.New(&bytes.Buffer{}))
	if err != nil {
		t.Fatalf("start the incumbent: %v", err)
	}
	// A request now reaches the incumbent.
	if code := postIngest(t, port, s1.Endpoint.Token, "before-handoff"); code != http.StatusOK {
		t.Fatalf("incumbent ingest status = %d, want 200", code)
	}

	// The newest instance starts on the SAME stable port: it must replace the
	// incumbent via /handoff rather than fail.
	s2, err := Start(sender(&served2), nil, log.New(&bytes.Buffer{}))
	if err != nil {
		t.Fatalf("the newest instance failed to adopt the stable port: %v", err)
	}
	defer s2.Stop()

	// The incumbent yielded (so its Stop() will leave mcp.json to the newcomer).
	if !s1.yielded.Load() {
		t.Fatal("the incumbent did not record that it yielded the port")
	}
	s1.Stop() // must be a no-op on mcp.json now that it yielded

	// The stable port now serves the LIVE (second) instance: a name-drop lands on
	// s2's sender, never s1's.
	if code := postIngest(t, port, s2.Endpoint.Token, "after-handoff"); code != http.StatusOK {
		t.Fatalf("post-handoff ingest status = %d, want 200", code)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(served2) != 1 || served2[0] != "after-handoff" {
		t.Fatalf("the newest instance is not serving the stable port: s2 saw %v", served2)
	}
	for _, s := range served1 {
		if s == "after-handoff" {
			t.Fatal("the yielded incumbent is still serving the stable port")
		}
	}

	// The registration cannot go stale: mcp.json now records the stable URL + the
	// persistent token, so a client using the static endpoint reaches whoever holds
	// the port (the live s2).
	ep, ok := ReadEndpoint()
	if !ok {
		t.Fatal("mcp.json missing after the handoff")
	}
	if ep.Port != port || ep.URL != StableURL() {
		t.Fatalf("mcp.json endpoint drifted from the stable URL: %+v (want %s)", ep, StableURL())
	}
	if ep.Token != s2.Endpoint.Token {
		t.Fatalf("mcp.json token = %q, want the live instance's %q", ep.Token, s2.Endpoint.Token)
	}
}

// TestAdoptReapsDeadIncumbent drives the OTHER adopt-or-replace branch: the stable
// port is busy but the mcp.json on record names a DEAD pid (a crashed prior
// instance whose port is still lingering). listenAdoptOrReplace must reap the
// stale record and adopt the port once it frees, rather than error — so a crash
// never leaves the stable endpoint permanently unclaimable.
func TestAdoptReapsDeadIncumbent(t *testing.T) {
	t.Setenv("BB_CONFIG_DIR", t.TempDir())
	port := freePort(t)
	t.Setenv("BB_MCP_PORT", strconv.Itoa(port))
	_, token := StableEndpoint()

	// A definitely-dead pid: run a trivial process to completion, then reuse its id.
	dead := exec.Command("true")
	if err := dead.Run(); err != nil {
		t.Fatalf("spawn a throwaway process: %v", err)
	}
	deadPID := dead.Process.Pid
	if pidAlive(deadPID) {
		t.Skipf("pid %d was recycled and is alive — cannot simulate a dead incumbent", deadPID)
	}

	// Record a stale endpoint (dead pid) and hold the port with a raw listener that
	// stands in for the crashed process's lingering socket.
	if err := writeEndpoint(Endpoint{URL: StableURL(), Port: port, PID: deadPID, Token: token}); err != nil {
		t.Fatalf("seed stale mcp.json: %v", err)
	}
	raw, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatalf("hold the stable port: %v", err)
	}
	// The lingering socket frees shortly after — as the OS would release a dead
	// process's port — so the retry inside adopt succeeds.
	go func() {
		time.Sleep(200 * time.Millisecond)
		_ = raw.Close()
	}()

	ln, err := listenAdoptOrReplace(port, log.New(&bytes.Buffer{}))
	if err != nil {
		t.Fatalf("adopt did not reclaim the port from a dead incumbent: %v", err)
	}
	defer ln.Close()
	if got := ln.Addr().(*net.TCPAddr).Port; got != port {
		t.Fatalf("adopted the wrong port: %d, want %d", got, port)
	}
}

// TestForeignHolderIsAnHonestError proves the safety boundary: a busy stable port
// with NO bb record on file (some unrelated process) is not silently
// stolen — listenAdoptOrReplace returns an error pointing at BB_MCP_PORT.
func TestForeignHolderIsAnHonestError(t *testing.T) {
	t.Setenv("BB_CONFIG_DIR", t.TempDir()) // empty: no mcp.json on record
	port := freePort(t)
	t.Setenv("BB_MCP_PORT", strconv.Itoa(port))

	foreign, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatalf("hold the port as a foreign process: %v", err)
	}
	defer foreign.Close()

	if _, err := listenAdoptOrReplace(port, log.New(&bytes.Buffer{})); err == nil {
		t.Fatal("a foreign holder with no endpoint on record must be an honest error, not a silent steal")
	}
}
