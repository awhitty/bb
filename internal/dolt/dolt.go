// Package dolt is the ONE place this app talks to Dolt directly. Everything
// else goes through the bd CLI (internal/bd); board time-travel is the sole
// exception, because bd exposes per-issue history and timestamp filters but no
// whole-board "as of T" query — and Dolt does, via `AS OF <commit>`.
//
// bd runs Dolt in one of two modes; this package supports both, chosen at
// Connect: SERVER mode (a sql-server on a port → MySQL protocol) and EMBEDDED
// mode (in-process; queried through the `dolt` CLI against the data dir). Both
// return whole-board snapshots at commit boundaries, including since-closed and
// since-deleted beads. The coupling is isolated here and read-only.
package dolt

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"

	"github.com/awhitty/bb/internal/bd"
)

// Boundary is one Dolt commit — a state-change boundary. bd auto-commits per
// mutation ("bd: close demo-mno"), so boundaries are dense.
type Boundary struct {
	Hash    string
	Date    time.Time
	Message string
}

var hashRe = regexp.MustCompile(`^[0-9a-v]{32}$`) // dolt base-32 commit hash

// querier is the mode-specific transport; the assembly on top is shared.
type querier interface {
	query(sql string) ([]map[string]any, error)
	Close() error
}

// Source is a read-only handle to a workspace's Dolt history.
type Source struct{ q querier }

// Connect opens the workspace's Dolt history, preferring the running sql-server
// (server mode) and falling back to the embedded data dir via the dolt CLI.
// Env overrides BEADS_DOLT_HOST/PORT/USER/PASSWORD match bd's precedence.
func Connect(workspace string) (*Source, error) {
	beads := filepath.Join(workspace, ".beads")
	// Server mode: a reachable sql-server on the recorded port.
	port := envOr("BEADS_DOLT_PORT", strings.TrimSpace(readFile(filepath.Join(beads, "dolt-server.port"))))
	if port != "" {
		if q, err := dialServer(beads, port); err == nil {
			return &Source{q: q}, nil
		} else {
			lastServerErr = err
		}
	}
	// Embedded mode: query the on-disk data dir with the dolt CLI.
	for _, dir := range []string{filepath.Join(beads, "embeddeddolt"), filepath.Join(beads, "dolt")} {
		if doltData(dir) {
			if _, err := exec.LookPath("dolt"); err != nil {
				return nil, fmt.Errorf("embedded Dolt needs the `dolt` CLI on PATH for time-travel")
			}
			return &Source{q: &cliQuerier{dir: dir}}, nil
		}
	}
	if lastServerErr != nil {
		return nil, lastServerErr
	}
	return nil, fmt.Errorf("no Dolt server or embedded data found under %s", beads)
}

var lastServerErr error

// doltData reports whether dir is a Dolt repo the CLI can query — either a repo
// itself (.dolt inside) or a multi-db root (a subdir with .dolt, as bd's
// embedded mode lays out embeddeddolt/<database>/.dolt).
func doltData(dir string) bool {
	if _, err := os.Stat(filepath.Join(dir, ".dolt")); err == nil {
		return true
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.IsDir() {
			if _, err := os.Stat(filepath.Join(dir, e.Name(), ".dolt")); err == nil {
				return true
			}
		}
	}
	return false
}

func (s *Source) Close() error { return s.q.Close() }

// Boundaries returns commit boundaries newest-first.
func (s *Source) Boundaries() ([]Boundary, error) {
	rows, err := s.q.query("SELECT commit_hash, date, message FROM dolt_log ORDER BY date DESC")
	if err != nil {
		return nil, err
	}
	out := make([]Boundary, 0, len(rows))
	for _, r := range rows {
		out = append(out, Boundary{
			Hash:    str(r["commit_hash"]),
			Date:    timeOf(r["date"]),
			Message: str(r["message"]),
		})
	}
	return out, nil
}

// IssuesAsOf builds the whole board as it stood at commit hash, in the shape bd
// list --flat returns. Includes closed beads — the caller filters like live.
func (s *Source) IssuesAsOf(hash string) ([]bd.Issue, error) {
	if !hashRe.MatchString(hash) {
		return nil, fmt.Errorf("invalid commit hash %q", hash)
	}
	// hash is validated base-32 from dolt_log (never user input), so it is safe
	// to interpolate into the AS OF clause (not a value position).
	irows, err := s.q.query(fmt.Sprintf(`SELECT id, title, status, priority, issue_type,
		description, notes, assignee, owner, created_by, created_at, updated_at
		FROM issues AS OF '%s'`, hash))
	if err != nil {
		if missingTable(err) {
			return nil, nil // pre-schema commit: the tracker didn't exist yet
		}
		return nil, err
	}
	issues := make([]bd.Issue, 0, len(irows))
	byID := make(map[string]*bd.Issue, len(irows))
	for _, r := range irows {
		issues = append(issues, bd.Issue{
			ID: str(r["id"]), Title: str(r["title"]), Status: str(r["status"]),
			Priority: intOf(r["priority"]), IssueType: str(r["issue_type"]),
			Description: str(r["description"]), Notes: str(r["notes"]),
			Assignee: str(r["assignee"]), Owner: str(r["owner"]), CreatedBy: str(r["created_by"]),
			CreatedAt: rfc(r["created_at"]), UpdatedAt: rfc(r["updated_at"]),
		})
	}
	for i := range issues {
		byID[issues[i].ID] = &issues[i]
	}
	if drows, err := s.q.query(fmt.Sprintf("SELECT issue_id, depends_on_issue_id, type FROM dependencies AS OF '%s'", hash)); err == nil {
		for _, r := range drows {
			iid, dep, typ := str(r["issue_id"]), str(r["depends_on_issue_id"]), str(r["type"])
			if dep == "" {
				continue
			}
			if is, ok := byID[iid]; ok {
				is.Dependencies = append(is.Dependencies, bd.Dependency{IssueID: iid, DependsOnID: dep, Type: typ})
				if typ == "parent-child" {
					is.Parent = dep
				}
			}
		}
	} else if !missingTable(err) {
		return nil, err
	}
	if lrows, err := s.q.query(fmt.Sprintf("SELECT issue_id, label FROM labels AS OF '%s'", hash)); err == nil {
		for _, r := range lrows {
			if is, ok := byID[str(r["issue_id"])]; ok {
				is.Labels = append(is.Labels, str(r["label"]))
			}
		}
	} else if !missingTable(err) {
		return nil, err
	}
	return issues, nil
}

// Event is one row of the audit trail (the `events` table) — the source for
// the activity feed. Old/New carry the changed fields for a before→after.
type Event struct {
	IssueID string
	Title   string
	Type    string // created | updated | closed | …
	Actor   string
	When    time.Time
	Old     map[string]any
	New     map[string]any
	Comment string
}

// Events returns the newest `limit` change events across the whole board,
// newest-first, joined to the current title (available even for closed beads).
func (s *Source) Events(limit int) ([]Event, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := s.q.query(fmt.Sprintf(`SELECT e.issue_id, e.event_type, e.actor,
		e.old_value, e.new_value, e.comment, e.created_at, i.title
		FROM events e LEFT JOIN issues i ON e.issue_id = i.id
		ORDER BY e.created_at DESC LIMIT %d`, limit))
	if err != nil {
		if missingTable(err) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]Event, 0, len(rows))
	for _, r := range rows {
		out = append(out, Event{
			IssueID: str(r["issue_id"]),
			Title:   str(r["title"]),
			Type:    str(r["event_type"]),
			Actor:   str(r["actor"]),
			When:    timeOf(r["created_at"]),
			Old:     jsonMap(r["old_value"]),
			New:     jsonMap(r["new_value"]),
			Comment: str(r["comment"]),
		})
	}
	return out, nil
}

func jsonMap(v any) map[string]any {
	s := strings.TrimSpace(str(v))
	if s == "" || s == "null" {
		return nil
	}
	var m map[string]any
	if json.Unmarshal([]byte(s), &m) != nil {
		return nil
	}
	return m
}

// --- server-mode querier (MySQL) ---

type serverQuerier struct{ db *sql.DB }

func dialServer(beads, port string) (*serverQuerier, error) {
	dbName := metaField(filepath.Join(beads, "metadata.json"), "dolt_database")
	if dbName == "" {
		dbName = "beads"
	}
	host := envOr("BEADS_DOLT_HOST", "127.0.0.1")
	user := envOr("BEADS_DOLT_USER", "root")
	cred := user
	if p := os.Getenv("BEADS_DOLT_PASSWORD"); p != "" {
		cred = user + ":" + p
	}
	dsn := fmt.Sprintf("%s@tcp(%s:%s)/%s?parseTime=true&timeout=4s&readTimeout=10s", cred, host, port, dbName)
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(2)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("Dolt server unreachable at %s:%s: %w", host, port, err)
	}
	return &serverQuerier{db: db}, nil
}

func (s *serverQuerier) Close() error { return s.db.Close() }

func (s *serverQuerier) query(q string) ([]map[string]any, error) {
	rows, err := s.db.Query(q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	var out []map[string]any
	for rows.Next() {
		vals := make([]any, len(cols))
		ptr := make([]any, len(cols))
		for i := range vals {
			ptr[i] = &vals[i]
		}
		if err := rows.Scan(ptr...); err != nil {
			return nil, err
		}
		m := make(map[string]any, len(cols))
		for i, c := range cols {
			m[c] = vals[i]
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// --- embedded-mode querier (dolt CLI) ---

type cliQuerier struct{ dir string }

func (c *cliQuerier) Close() error { return nil }

func (c *cliQuerier) query(q string) ([]map[string]any, error) {
	cmd := exec.Command("dolt", "sql", "-q", q, "--result-format", "json")
	cmd.Dir = c.dir
	out, err := cmd.Output()
	if err != nil {
		msg := err.Error()
		if ee, ok := err.(*exec.ExitError); ok {
			msg = strings.TrimSpace(string(ee.Stderr))
		}
		return nil, fmt.Errorf("%s", msg)
	}
	var res struct {
		Rows []map[string]any `json:"rows"`
	}
	if len(out) == 0 {
		return nil, nil
	}
	if err := json.Unmarshal(out, &res); err != nil {
		return nil, fmt.Errorf("dolt cli: bad json: %w", err)
	}
	return res.Rows, nil
}

// --- value coercion (server gives []byte/int64/time.Time; cli gives string/float64) ---

func str(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case []byte:
		return string(t)
	default:
		return fmt.Sprint(t)
	}
}

func intOf(v any) int {
	switch t := v.(type) {
	case int64:
		return int(t)
	case float64:
		return int(t)
	case []byte:
		var n int
		fmt.Sscan(string(t), &n)
		return n
	case string:
		var n int
		fmt.Sscan(t, &n)
		return n
	}
	return 0
}

var timeFormats = []string{time.RFC3339, "2006-01-02 15:04:05.999999999 -0700 MST", "2006-01-02 15:04:05.999999", "2006-01-02 15:04:05"}

func timeOf(v any) time.Time {
	if t, ok := v.(time.Time); ok {
		return t
	}
	s := str(v)
	for _, f := range timeFormats {
		if t, err := time.Parse(f, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

// rfc renders a timestamp value as RFC3339 (compactAge parses that).
func rfc(v any) string {
	t := timeOf(v)
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func missingTable(err error) bool {
	return err != nil && strings.Contains(err.Error(), "table not found")
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func readFile(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(b)
}

func metaField(path, field string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return ""
	}
	if v, ok := m[field].(string); ok {
		return v
	}
	return ""
}
