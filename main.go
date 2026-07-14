// bb: keyboard-first kanban TUI for the beads (bd) issue tracker.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/log"

	"github.com/awhitty/bb/internal/agentapi"
	"github.com/awhitty/bb/internal/bd"
	"github.com/awhitty/bb/internal/buildinfo"
	"github.com/awhitty/bb/internal/discover"
	"github.com/awhitty/bb/internal/hook"
	"github.com/awhitty/bb/internal/mcpserv"
	"github.com/awhitty/bb/internal/nlq"
	"github.com/awhitty/bb/internal/ui"
	"github.com/awhitty/bb/internal/watch"
)

// newLogger writes diagnostics to $BB_LOG (a file path) when set.
// Never stdout — that would corrupt the TUI frame.
func newLogger() (*log.Logger, func()) {
	path := os.Getenv("BB_LOG")
	if path == "" {
		return log.New(io.Discard), func() {}
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return log.New(io.Discard), func() {}
	}
	_ = f.Chmod(0o600)
	logger := log.NewWithOptions(f, log.Options{ReportTimestamp: true, Level: log.DebugLevel})
	return logger, func() { _ = f.Close() }
}

// markdownStyle resolves the glamour style name exactly once, HERE, before
// the bubbletea Program takes over stdin. Auto-detection later would emit an
// OSC background query whose reply bubbletea's input reader consumes — the
// known glamour+bubbletea deadlock that froze the detail view.
// BB_THEME=dark|light|notty skips detection entirely.
func markdownStyle() string {
	switch theme := os.Getenv("BB_THEME"); theme {
	case "dark", "light", "notty", "ascii", "auto":
		if theme != "auto" {
			return theme
		}
	}
	if !isatty() {
		return "notty"
	}
	if lipgloss.HasDarkBackground() {
		return "dark"
	}
	return "light"
}

func isatty() bool {
	fi, err := os.Stdout.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

const helpText = `bb — a keyboard-first bead board

Usage:
  bb                         open the board for the current Beads workspace
  bb status                  show resolved model, MCP, and storage settings
  bb hook install            install Claude Code activity hooks
  bb hook uninstall          remove Claude Code activity hooks
  bb mcp install             register the live board with Claude Code
  bb mcp uninstall           remove the live-board MCP registration
  bb mcp-serve               run the read-only MCP server over stdio
  bb version                 print the installed version
  bb --help                  show this help

Common environment variables:
  BB_WORKSPACE               use a Beads workspace other than the current directory
  BB_CONFIG_DIR              move bb's config and private local state
  BB_THEME                   set dark, light, notty, ascii, or auto
  BB_LOG                     write private diagnostics to this file

Run bb status for local model and MCP details. See the README for every
model override and the privacy behavior of optional hooks.
`

func printHelp(w io.Writer) {
	_, _ = io.WriteString(w, helpText)
}

// status prints where everything resolved — the one place to debug.
func status() {
	logger := log.New(io.Discard)
	fmt.Println("bb status")
	fmt.Println()

	cfgPath := discover.ConfigDir() + "/config.toml"
	if _, err := os.Stat(cfgPath); err == nil {
		fmt.Printf("config:    %s\n", cfgPath)
	} else {
		fmt.Printf("config:    %s (not written yet)\n", cfgPath)
	}

	r := discover.Resolve(logger)
	if r.Err != "" {
		fmt.Printf("server:    NONE — %s\n", r.Err)
		fmt.Printf("timing:    %s\n", r.Timing)
	} else {
		fmt.Printf("server:    %s (via %s)\n", r.Compiler.URL, r.Compiler.Via)
		key := "none"
		if r.Compiler.KeySource != "" {
			key = r.Compiler.KeySource
		}
		fmt.Printf("key:       %s\n", key)
		fmt.Printf("compiler:  %s\n", r.Compiler.Model)
		fmt.Printf("analyst:   %s\n", r.Analyst.Model)
		if r.Analyst.URL != r.Compiler.URL {
			fmt.Printf("analyst @: %s (via %s)\n", r.Analyst.URL, r.Analyst.Via)
		}
		fmt.Printf("timing:    %s\n", r.Timing)
		if len(r.Models) > 0 {
			fmt.Printf("models:    %s\n", strings.Join(r.Models, "\n           "))
		}
	}

	fmt.Println()
	running := ""
	if ep, ok := mcpserv.ReadEndpoint(); ok {
		if proc, err := os.FindProcess(ep.PID); err == nil && proc.Signal(syscall.Signal(0)) == nil {
			running = fmt.Sprintf(" (pid %d, running now)", ep.PID)
		} else {
			running = " (not running)"
		}
	} else {
		running = " (not running)"
	}
	fmt.Printf("mcp url:   %s%s\n", mcpserv.StableURL(), running)
	fmt.Printf("           stable port + persistent token in %s — connect once with `bb mcp install`\n", discover.ConfigDir()+"/config.toml")
	fmt.Println("mcp-serve: headless read-only over stdio — `claude mcp add bb -- bb mcp-serve`")
	fmt.Printf("feedback:  %s\n", nlq.NewFeedbackLog().Path)
	if raw, err := os.ReadFile(discover.OmlxLogPath()); err == nil {
		lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
		if n := len(lines); n > 0 {
			start := n - 5
			if start < 0 {
				start = 0
			}
			fmt.Printf("omlx log:  %s (last lines)\n           %s\n",
				discover.OmlxLogPath(), strings.Join(lines[start:], "\n           "))
		}
	} else {
		fmt.Println("omlx log:  no autostarts yet")
	}
	envs := []string{"BB_NLQ_URL", "BB_NLQ_MODEL", "BB_NLQ_KEY",
		"BB_ANALYST_URL", "BB_ANALYST_MODEL", "BB_ANALYST_KEY"}
	var set []string
	for _, e := range envs {
		if os.Getenv(e) != "" {
			v := os.Getenv(e)
			if strings.Contains(e, "KEY") {
				v = "***"
			}
			set = append(set, e+"="+v)
		}
	}
	if len(set) > 0 {
		fmt.Printf("env:       %s (these override config + discovery)\n", strings.Join(set, " "))
	} else {
		fmt.Println("env:       none set (config + discovery in charge)")
	}
}

func mcpServe() {
	logger, closeLog := newLogger()
	defer closeLog()
	client := bd.NewClient(bd.SpawnRunner(bd.Workspace()))
	if err := mcpserv.ServeStdio(client, logger); err != nil {
		fmt.Fprintln(os.Stderr, "bb mcp-serve:", err)
		os.Exit(1)
	}
}

// mcpInstall registers the drive-the-board server with the claude CLI (user
// scope) using the stable url + persistent token, so connecting is one command
// and survives restarts. If claude isn't found, it prints the exact command.
func mcpInstall() {
	url := mcpserv.StableURL()
	_, token := mcpserv.StableEndpoint()
	header := "Authorization: Bearer " + token
	if claude, err := exec.LookPath("claude"); err == nil {
		cmd := exec.Command(claude, "mcp", "add", "--transport", "http", "--scope", "user", "bb", url, "--header", header)
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			fmt.Fprintln(os.Stderr, "claude mcp add failed:", err)
			os.Exit(1)
		}
		fmt.Printf("Connected. Start bb and your agent can drive the board at %s\n", url)
		return
	}
	fmt.Println("claude CLI not found. Run this once to connect your agent:")
	fmt.Printf("  claude mcp add --transport http --scope user bb %s --header %q\n", url, header)
}

func mcpUninstall() {
	if claude, err := exec.LookPath("claude"); err == nil {
		cmd := exec.Command(claude, "mcp", "remove", "--scope", "user", "bb")
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		_ = cmd.Run()
		fmt.Println("Removed bb from your agent's MCP servers.")
		return
	}
	fmt.Println("claude CLI not found. Remove manually:")
	fmt.Println("  claude mcp remove --scope user bb")
}

// hookInstall writes the Claude Code Stop hook that pushes name-dropped bead
// ids to the running TUI, using this binary's absolute path.
func hookInstall() {
	bin, err := os.Executable()
	if err != nil || bin == "" {
		bin = "bb"
	}
	changed, err := hook.Install(bin)
	if err != nil {
		fmt.Fprintln(os.Stderr, "hook install:", err)
		os.Exit(1)
	}
	if changed {
		fmt.Printf("Installed the bb Stop + SessionEnd hooks in %s.\n", hook.SettingsPath())
		fmt.Println("While bb is running, bead ids your agent mentions light up live, and ending a conversation archives its channel. `bb hook uninstall` reverses it.")
	} else {
		fmt.Println("Already installed.")
	}
}

func hookUninstall() {
	changed, err := hook.Uninstall()
	if err != nil {
		fmt.Fprintln(os.Stderr, "hook uninstall:", err)
		os.Exit(1)
	}
	if changed {
		fmt.Println("Removed the bb Stop + SessionEnd hooks.")
	} else {
		fmt.Println("No bb hooks were installed.")
	}
}

// hookIngest is the Stop-hook body: read the payload on stdin, pull the latest
// assistant turn, extract the workspace bead ids it name-dropped, and POST them
// to a running bb. ALWAYS exits 0 — it must never slow or fail a turn,
// and a missing TUI is a silent no-op.
func hookIngest() {
	defer os.Exit(0)
	raw, err := io.ReadAll(os.Stdin)
	if err != nil {
		return
	}
	p, err := hook.ReadPayload(raw)
	if err != nil {
		return
	}
	transcript, err := hook.ReadTranscript(p.TranscriptPath)
	if err != nil {
		return
	}
	text := hook.LatestAssistantText(transcript)
	if text == "" {
		return
	}
	client := bd.NewClient(bd.SpawnRunner(bd.Workspace()))
	all, err := client.List(true)
	if err != nil {
		return
	}
	ids := make([]string, len(all))
	for i, is := range all {
		ids[i] = is.ID
	}
	prefix := hook.CommonPrefix(ids)
	beadIDs := hook.ExtractBeadIDs(text, ids, prefix)
	if len(beadIDs) == 0 {
		return
	}
	port, token := mcpserv.StableEndpoint()
	body, _ := json.Marshal(agentapi.NameDropAction{
		SessionID: p.SessionID,
		ConvoName: hook.ConvoName(transcript, p.SessionID),
		TS:        time.Now().UTC().Format(time.RFC3339),
		IDs:       beadIDs,
		Snippet:   hook.Snippet(text, 140),
		Excerpts:  hook.ExtractExcerpts(text, ids, prefix),
	})
	req, err := http.NewRequest("POST", fmt.Sprintf("http://127.0.0.1:%d/ingest", port), bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	hc := &http.Client{Timeout: 1500 * time.Millisecond}
	if resp, err := hc.Do(req); err == nil {
		_ = resp.Body.Close()
	}
}

// hookEnd is the SessionEnd-hook body: read the payload on stdin and POST the
// ended SessionID to a running bb so it archives that session's channel.
// ALWAYS exits 0 — it must never slow or fail a turn, and a missing TUI is a
// silent no-op. Unlike hook-ingest (the per-turn Stop hook), this fires once, at
// the end of a conversation.
func hookEnd() {
	defer os.Exit(0)
	raw, err := io.ReadAll(os.Stdin)
	if err != nil {
		return
	}
	p, err := hook.ReadPayload(raw)
	if err != nil || p.SessionID == "" {
		return
	}
	port, token := mcpserv.StableEndpoint()
	body, _ := json.Marshal(agentapi.SessionEndAction{SessionID: p.SessionID})
	req, err := http.NewRequest("POST", fmt.Sprintf("http://127.0.0.1:%d/session-end", port), bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	hc := &http.Client{Timeout: 1500 * time.Millisecond}
	if resp, err := hc.Do(req); err == nil {
		_ = resp.Body.Close()
	}
}

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "help", "-h", "--help":
			printHelp(os.Stdout)
			return
		case "version", "--version":
			fmt.Printf("bb %s\n", buildinfo.Version())
			return
		case "status":
			status()
			return
		case "mcp-serve": // headless read-only MCP over stdio
			mcpServe()
			return
		case "hook-ingest": // Claude Code Stop-hook body (stdin → /ingest)
			hookIngest()
			return
		case "hook-end": // Claude Code SessionEnd-hook body (stdin → /session-end)
			hookEnd()
			return
		case "hook":
			switch {
			case len(os.Args) > 2 && os.Args[2] == "install":
				hookInstall()
			case len(os.Args) > 2 && os.Args[2] == "uninstall":
				hookUninstall()
			default:
				fmt.Println("usage: bb hook [install|uninstall]")
			}
			return
		case "mcp":
			switch {
			case len(os.Args) > 2 && os.Args[2] == "install":
				mcpInstall()
			case len(os.Args) > 2 && os.Args[2] == "uninstall":
				mcpUninstall()
			default:
				fmt.Println("usage: bb mcp [install|uninstall]")
			}
			return
		default:
			fmt.Fprintf(os.Stderr, "bb: unknown command %q\n\n", os.Args[1])
			printHelp(os.Stderr)
			os.Exit(2)
		}
	}

	// Resolve the terminal-dependent style BEFORE the Program owns stdin.
	mdStyle := markdownStyle()
	// Fix the lipgloss adaptive palette to the SAME resolved background and mark
	// it EXPLICIT, so no AdaptiveColor render ever probes termenv (that OSC probe
	// is the deadlock). This also covers the BB_THEME override path, which
	// otherwise never tells lipgloss the background. In-app: T toggles both.
	ui.SetInitialTheme(mdStyle)

	logger, closeLog := newLogger()
	defer closeLog()

	ws := bd.Workspace()
	client := bd.NewClient(bd.SpawnRunner(ws))
	// Providers start empty; discovery fills them in from inside the TUI so
	// the board never waits on a model server (autostart can take ~20s).
	model := ui.New(client, &nlq.Provider{}, &nlq.Analyst{}, nlq.NewFeedbackLog(), logger, mdStyle)

	p := tea.NewProgram(model, tea.WithAltScreen())

	// MCP server: agents drive the visible UI through p.Send; replies come
	// back on a channel with a hard timeout so a stuck UI can't hang HTTP.
	send := func(action agentapi.Action) (agentapi.Response, error) {
		reply := make(chan agentapi.Response, 1)
		p.Send(agentapi.Request{Action: action, Reply: reply})
		select {
		case resp := <-reply:
			return resp, nil
		case <-time.After(5 * time.Second):
			return agentapi.Response{}, fmt.Errorf("the TUI did not answer within 5s")
		}
	}
	if srv, err := mcpserv.Start(send, client, logger); err != nil {
		logger.Error("mcp start", "err", err)
	} else {
		defer srv.Stop()
	}

	// Live board watch: an agent mutating the tracker via bd elsewhere shows
	// up on the running board within seconds, no keypress. 500ms debounce
	// (collapses the JSONL export's write burst) + a 15s poll fallback.
	stopWatch := watch.Start(filepath.Join(ws, ".beads"), 500*time.Millisecond, 15*time.Second,
		func() { p.Send(ui.BoardChangedMsg{}) }, logger)
	defer stopWatch()

	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "bb:", err)
		os.Exit(1)
	}
}
