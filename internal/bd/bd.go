// Package bd is a client for the beads (`bd`) CLI. All board access goes
// through `bd ... --json`; there is no direct store access.
package bd

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// QueryGrammar is the bd query language, for self-documenting surfaces (the
// MCP schema tool). Kept terse and enum-accurate.
const QueryGrammar = `field=value | field!=value | field>value | field>=value | field<value | field<=value; ` +
	`combine with AND OR NOT ( ) (case-insensitive). ` +
	`Fields: status(open|in_progress|blocked|deferred|closed), priority(0-4), ` +
	`type(bug|feature|task|epic|chore|decision), assignee, owner, label, title, description, notes, ` +
	`created, updated, started, closed, id, parent, pinned. ` +
	`title/description/notes match by substring; assignee/owner match EXACTLY (quote if spaced). ` +
	`Dates take YYYY-MM-DD or quoted relative forms like "-7d". parent=<id> matches DIRECT children only. ` +
	`Examples: status=open AND type=bug · parent=demo-pqr · assignee="Alex Rivera" AND updated>"-7d"`

// Status values bd uses.
const (
	StatusOpen       = "open"
	StatusInProgress = "in_progress"
	StatusBlocked    = "blocked"
	StatusDeferred   = "deferred"
	StatusClosed     = "closed"
)

// Dependency is one entry in bd's dependency list — which comes in TWO
// shapes: `bd list` emits edge records (issue_id/depends_on_id/type, where
// type "parent-child" is hierarchy and anything else a real blocker), while
// `bd show` emits the depended-on issues as full objects (id/title, no type;
// the parent link is included and only recognizable by id).
type Dependency struct {
	// Edge shape (bd list).
	IssueID     string `json:"issue_id"`
	DependsOnID string `json:"depends_on_id"`
	Type        string `json:"type"`
	// Issue-object shape (bd show).
	ID    string `json:"id"`
	Title string `json:"title"`
}

// BlockerIDs returns the ids of real (non-hierarchy) dependencies of is,
// handling both dependency shapes.
func BlockerIDs(is Issue) []string {
	var out []string
	for _, d := range is.Dependencies {
		switch {
		case d.DependsOnID != "": // edge shape: type distinguishes hierarchy
			if d.Type != "parent-child" {
				out = append(out, d.DependsOnID)
			}
		case d.ID != "": // object shape: the parent link is hierarchy
			if d.ID != is.Parent {
				out = append(out, d.ID)
			}
		}
	}
	return out
}

// Issue mirrors the fields of `bd list --json --flat`.
type Issue struct {
	ID              string       `json:"id"`
	Title           string       `json:"title"`
	Status          string       `json:"status"`
	Priority        int          `json:"priority"`
	IssueType       string       `json:"issue_type"`
	Parent          string       `json:"parent"`
	Assignee        string       `json:"assignee"`
	Owner           string       `json:"owner"`
	Labels          []string     `json:"labels"`
	Dependencies    []Dependency `json:"dependencies"`
	DependencyCount int          `json:"dependency_count"`
	DependentCount  int          `json:"dependent_count"`
	Description     string       `json:"description"`
	Notes           string       `json:"notes"`
	CreatedAt       string       `json:"created_at"`
	CreatedBy       string       `json:"created_by"`
	UpdatedAt       string       `json:"updated_at"`
	CommentCount    int          `json:"comment_count"`

	// DupCount is stamped by rollup.Group: when a multi-valued facet (e.g.
	// labels) fans one issue into N columns, each copy carries N-1 here so
	// renderers can mark it a duplicate. Not part of bd's wire format.
	DupCount int `json:"-"`
}

// Runner executes one bd invocation and returns stdout.
// Injectable for tests.
type Runner func(args ...string) (string, error)

// Workspace resolves where bd runs: $BB_WORKSPACE if set, else the
// inherited cwd (bd resolves its own workspace from cwd).
func Workspace() string {
	if ws := os.Getenv("BB_WORKSPACE"); ws != "" {
		return ws
	}
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return wd
}

// SpawnRunner returns a Runner that execs `bd` with the given working dir.
func SpawnRunner(cwd string) Runner {
	return func(args ...string) (string, error) {
		cmd := exec.Command("bd", args...)
		cmd.Dir = cwd
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		runErr := cmd.Run()
		if runErr != nil {
			return "", extractError(stdout.Bytes(), stderr.Bytes(), args, runErr)
		}
		return stdout.String(), nil
	}
}

// extractError surfaces bd's real message: bd reports query/parse errors as
// {"error": ...} on STDOUT with an empty stderr, so a generic exit-code error
// would hide the actual problem.
func extractError(stdout, stderr []byte, args []string, runErr error) error {
	if msg := strings.TrimSpace(string(stderr)); msg != "" {
		return errors.New(msg)
	}
	var body struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(bytes.TrimSpace(stdout), &body) == nil && body.Error != "" {
		return errors.New(body.Error)
	}
	verb := "bd"
	if len(args) > 0 {
		verb = "bd " + args[0]
	}
	return fmt.Errorf("%s failed: %v", verb, runErr)
}

// Client wraps a Runner with typed bd operations.
type Client struct {
	run Runner
}

func NewClient(run Runner) *Client {
	return &Client{run: run}
}

func parseIssues(raw string) ([]Issue, error) {
	trimmed := strings.TrimSpace(raw)
	var issues []Issue
	if err := json.Unmarshal([]byte(trimmed), &issues); err == nil {
		return issues, nil
	}
	var one Issue
	if err := json.Unmarshal([]byte(trimmed), &one); err != nil {
		return nil, fmt.Errorf("unexpected bd output: %w", err)
	}
	return []Issue{one}, nil
}

// List returns the full board. `bd list --json --flat` carries deps, labels,
// parent — one call feeds every rollup. all=true includes closed issues.
func (c *Client) List(all bool) ([]Issue, error) {
	args := []string{"list", "--json", "--flat"}
	if all {
		args = append(args, "--all")
	}
	out, err := c.run(args...)
	if err != nil {
		return nil, err
	}
	return parseIssues(out)
}

// Query filters the board via bd's query language.
func (c *Client) Query(q string) ([]Issue, error) {
	out, err := c.run("query", q, "--json")
	if err != nil {
		return nil, err
	}
	return parseIssues(out)
}

// Show fetches one issue (bd returns a one-element array).
func (c *Client) Show(id string) (Issue, error) {
	out, err := c.run("show", id, "--json")
	if err != nil {
		return Issue{}, err
	}
	issues, err := parseIssues(out)
	if err != nil {
		return Issue{}, err
	}
	if len(issues) == 0 {
		return Issue{}, fmt.Errorf("no issue %s", id)
	}
	return issues[0], nil
}

// AddComment appends a comment to an issue — the human↔agent annotation /
// decision channel. Runs `bd comment <id> <text>`.
func (c *Client) AddComment(id, text string) error {
	_, err := c.run("comment", id, text)
	return err
}

// Comments returns an issue's comments as bd emits them (opaque objects —
// the TUI passes them through). Empty slice when there are none.
func (c *Client) Comments(id string) ([]map[string]any, error) {
	out, err := c.run("comments", id, "--json")
	if err != nil {
		return nil, err
	}
	trimmed := strings.TrimSpace(out)
	if trimmed == "" || trimmed == "null" {
		return nil, nil
	}
	var comments []map[string]any
	if err := json.Unmarshal([]byte(trimmed), &comments); err != nil {
		return nil, fmt.Errorf("unexpected bd comments output: %w", err)
	}
	return comments, nil
}

// HistoryEntry is one committed version of an issue — bd tracks every change
// as a Dolt commit. The compact form (date/committer/status/priority/title)
// shows the trajectory without the full per-commit snapshot.
type HistoryEntry struct {
	Date      string `json:"date"`
	Committer string `json:"committer"`
	Status    string `json:"status"`
	Priority  int    `json:"priority"`
	Title     string `json:"title"`
}

// History returns an issue's version history, newest first as bd emits it.
func (c *Client) History(id string) ([]HistoryEntry, error) {
	out, err := c.run("history", id, "--json")
	if err != nil {
		return nil, err
	}
	trimmed := strings.TrimSpace(out)
	if trimmed == "" || trimmed == "null" {
		return nil, nil
	}
	var raw []struct {
		Committer  string `json:"Committer"`
		CommitDate string `json:"CommitDate"`
		Issue      Issue  `json:"Issue"`
	}
	if err := json.Unmarshal([]byte(trimmed), &raw); err != nil {
		return nil, fmt.Errorf("unexpected bd history output: %w", err)
	}
	// bd records a version per Dolt commit — the whole board commits together,
	// so an unchanged issue accrues hundreds of identical snapshots. Collapse
	// runs of the same (status, priority, title) to the CHANGE trajectory.
	// bd emits newest-first; keeping each state boundary yields "closed since
	// X, was open before, priority bumped at Y".
	var entries []HistoryEntry
	for _, r := range raw {
		e := HistoryEntry{
			Date:      r.CommitDate,
			Committer: r.Committer,
			Status:    r.Issue.Status,
			Priority:  r.Issue.Priority,
			Title:     r.Issue.Title,
		}
		if n := len(entries); n > 0 {
			p := entries[n-1]
			if p.Status == e.Status && p.Priority == e.Priority && p.Title == e.Title {
				continue // same state as the last kept — collapse
			}
		}
		entries = append(entries, e)
	}
	return entries, nil
}

// SetPriority sets an issue's priority (0 highest … 4 lowest) via the
// `bd priority` subcommand — the one mutation the TUI performs. Status and
// every other edit are deliberately out of scope: agents mutate the tracker
// through bd in natural-language sessions, and the board reflects it.
func (c *Client) SetPriority(id string, priority int) error {
	_, err := c.run("priority", id, strconv.Itoa(priority))
	return err
}
