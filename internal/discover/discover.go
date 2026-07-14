// Package discover resolves the local model setup with zero configuration:
// find a healthy OpenAI-compatible server (omlx, ollama), read its key from
// where the server keeps it, pick a compiler and an analyst from the models
// it actually serves, persist the result, and autostart omlx when nothing is
// running. Precedence: env overrides > config file > discovery.
package discover

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/charmbracelet/log"
)

const probeTimeout = 300 * time.Millisecond

// Role is one resolved model slot (compiler or analyst).
type Role struct {
	URL       string
	Key       string
	KeySource string // file the key came from ("env" for env keys, "" for none)
	Model     string
	Via       string // e.g. "omlx:8001", "ollama:11434", "env", "config"
}

// Short is the compact size label for the footer ("4B", "35B").
func (r Role) Short() string {
	if s := sizeOf(r.Model); s > 0 {
		if s == float64(int(s)) {
			return fmt.Sprintf("%dB", int(s))
		}
		return fmt.Sprintf("%.1fB", s)
	}
	if r.Model == "" {
		return "?"
	}
	return r.Model
}

// Resolved is the full outcome of a resolution pass.
type Resolved struct {
	Compiler Role
	Analyst  Role
	Models   []string // everything the chosen server listed
	Notice   string   // one-line "something changed" note ("" when quiet)
	Err      string   // non-empty → NL features stay off, board still works
	NoServer bool     // Err because nothing answered (autostart may help)
	Timing   string   // human summary of what took how long
}

// Config is what persists between runs (~/.config/bb/config.toml).
// The key itself is never written — only the path it can be re-read from,
// so rotation Just Works.
type Config struct {
	Server struct {
		URL     string `toml:"url"`
		KeyFile string `toml:"key_file"`
		Via     string `toml:"via"`
	} `toml:"server"`
	Models struct {
		Compiler string `toml:"compiler"`
		Analyst  string `toml:"analyst"`
	} `toml:"models"`
	Autostart struct {
		Enabled bool `toml:"enabled"`
	} `toml:"autostart"`
	// MCP is the drive-the-board server's STABLE endpoint: a fixed port and a
	// persistent bearer token generated once and reused across runs, so a
	// client connects once and survives restarts.
	MCP struct {
		Port  int    `toml:"port"`
		Token string `toml:"token"`
	} `toml:"mcp"`
}

func ConfigDir() string {
	if d := os.Getenv("BB_CONFIG_DIR"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "bb")
}

func configPath() string { return filepath.Join(ConfigDir(), "config.toml") }

// OmlxLogPath is where an autostarted omlx writes its output.
func OmlxLogPath() string { return filepath.Join(ConfigDir(), "omlx.log") }

func LoadConfig() (Config, bool) {
	var c Config
	c.Autostart.Enabled = true
	meta, err := toml.DecodeFile(configPath(), &c)
	_ = meta
	return c, err == nil
}

func SaveConfig(c Config) error {
	if err := os.MkdirAll(ConfigDir(), 0o700); err != nil {
		return err
	}
	// 0600: config.toml carries the MCP bearer token — keep it owner-only.
	f, err := os.OpenFile(configPath(), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_ = f.Chmod(0o600)
	defer f.Close()
	return toml.NewEncoder(f).Encode(c)
}

// --- key sources ---

func omlxSettingsPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".omlx", "settings.json")
}

type omlxSettings struct {
	Server struct {
		Port int `json:"port"`
	} `json:"server"`
	Auth struct {
		APIKey string `json:"api_key"`
	} `json:"auth"`
}

func readOmlxSettings() (omlxSettings, bool) {
	var s omlxSettings
	raw, err := os.ReadFile(omlxSettingsPath())
	if err != nil {
		return s, false
	}
	return s, json.Unmarshal(raw, &s) == nil
}

// ReadKey re-reads a key from its source file at startup. omlx settings.json
// holds it at auth.api_key; any other file is taken as the raw key.
func ReadKey(path string) string {
	if path == "" {
		return ""
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	if strings.HasSuffix(path, "settings.json") {
		var s omlxSettings
		if json.Unmarshal(raw, &s) == nil {
			return s.Auth.APIKey
		}
		return ""
	}
	return strings.TrimSpace(string(raw))
}

// --- probing ---

type candidate struct {
	url     string
	keyFile string
	via     string
}

type probeResult struct {
	candidate
	models []string
	err    error
}

func candidates() []candidate {
	var out []candidate
	seen := map[string]bool{}
	add := func(port int, keyFile, via string) {
		url := fmt.Sprintf("http://127.0.0.1:%d/v1", port)
		if seen[url] {
			return
		}
		seen[url] = true
		out = append(out, candidate{url: url, keyFile: keyFile, via: via})
	}
	if s, ok := readOmlxSettings(); ok && s.Server.Port > 0 {
		add(s.Server.Port, omlxSettingsPath(), fmt.Sprintf("omlx:%d", s.Server.Port))
	}
	keyFile := ""
	if _, err := os.Stat(omlxSettingsPath()); err == nil {
		keyFile = omlxSettingsPath()
	}
	add(8000, keyFile, "omlx:8000")
	add(8001, keyFile, "omlx:8001")
	add(11434, "", "ollama:11434")
	return out
}

// probe GETs /v1/models with a hard timeout.
func probe(c candidate) probeResult {
	client := &http.Client{Timeout: probeTimeout}
	req, err := http.NewRequest("GET", c.url+"/models", nil)
	if err != nil {
		return probeResult{candidate: c, err: err}
	}
	if key := ReadKey(c.keyFile); key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	res, err := client.Do(req)
	if err != nil {
		return probeResult{candidate: c, err: err}
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return probeResult{candidate: c, err: fmt.Errorf("status %d", res.StatusCode)}
	}
	var body struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		return probeResult{candidate: c, err: err}
	}
	models := make([]string, 0, len(body.Data))
	for _, m := range body.Data {
		models = append(models, m.ID)
	}
	if len(models) == 0 {
		return probeResult{candidate: c, err: fmt.Errorf("no models")}
	}
	return probeResult{candidate: c, models: models}
}

// probeAll runs all candidates in parallel; first healthy in candidate order
// wins.
func probeAll(cands []candidate) (probeResult, []probeResult) {
	results := make([]probeResult, len(cands))
	var wg sync.WaitGroup
	for i, c := range cands {
		wg.Add(1)
		go func(i int, c candidate) {
			defer wg.Done()
			results[i] = probe(c)
		}(i, c)
	}
	wg.Wait()
	for _, r := range results {
		if r.err == nil {
			return r, results
		}
	}
	return probeResult{}, results
}

// --- model ranking ---

var (
	nonTextRe = regexp.MustCompile(`(?i)(parakeet|chatterbox|whisper|-tts|-stt|audio|speech|vlm|locateanything|embed|rerank|clip|diffusion|flux|-sd-|vision|image|-3d)`)
	sizeRe    = regexp.MustCompile(`(?i)(\d+(?:\.\d+)?)b(?:$|[^a-z0-9])`)
	moeRe     = regexp.MustCompile(`(?i)(a\d+b|moe)`)
	uncensRe  = regexp.MustCompile(`(?i)(uncensored|abliterated)`)
)

func isText(model string) bool { return !nonTextRe.MatchString(model) }

// sizeOf reads the largest parameter-count token from a model name (35 for
// "Qwen3.6-35B-A3B": total params, not active). 0 = unknown.
func sizeOf(model string) float64 {
	best := 0.0
	for _, m := range sizeRe.FindAllStringSubmatch(model, -1) {
		if v, err := strconv.ParseFloat(m[1], 64); err == nil && v > best {
			best = v
		}
	}
	return best
}

// PickCompiler chooses the smallest instruct-capable text model ≥~1.5B,
// preferring the ~4B class (a Qwen3.5-4B beats both a 1.7B and a 9B) and
// standard variants over "Uncensored" ones.
func PickCompiler(models []string) string {
	type scored struct {
		name  string
		score float64
	}
	var cands []scored
	for _, m := range models {
		if !isText(m) {
			continue
		}
		size := sizeOf(m)
		if size < 1.5 {
			continue
		}
		score := size - 4
		if score < 0 {
			score = -score
		}
		if uncensRe.MatchString(m) {
			score += 100
		}
		if moeRe.MatchString(m) {
			score += 10 // an A3B MoE is not a small-compiler shape
		}
		cands = append(cands, scored{m, score})
	}
	if len(cands) == 0 {
		return ""
	}
	sort.SliceStable(cands, func(a, b int) bool {
		if cands[a].score != cands[b].score {
			return cands[a].score < cands[b].score
		}
		return cands[a].name < cands[b].name
	})
	return cands[0].name
}

// PickAnalyst chooses the largest text model, preferring MoE/A3B name
// patterns (fast decode at big-model quality) and standard variants over
// "Uncensored" ones when both exist.
func PickAnalyst(models []string) string {
	type scored struct {
		name string
		moe  bool
		unc  bool
		size float64
	}
	var cands []scored
	for _, m := range models {
		if !isText(m) {
			continue
		}
		size := sizeOf(m)
		if size < 1.5 {
			continue
		}
		cands = append(cands, scored{m, moeRe.MatchString(m), uncensRe.MatchString(m), size})
	}
	if len(cands) == 0 {
		return ""
	}
	sort.SliceStable(cands, func(a, b int) bool {
		ca, cb := cands[a], cands[b]
		if ca.unc != cb.unc {
			return !ca.unc // standard variants first
		}
		if ca.moe != cb.moe {
			return ca.moe // MoE first
		}
		if ca.size != cb.size {
			return ca.size > cb.size
		}
		return ca.name < cb.name
	})
	return cands[0].name
}

// --- autostart ---

// CanAutostart reports whether spawning omlx is worth attempting: the
// binary exists, config allows it, and no env pin points elsewhere.
func CanAutostart() bool {
	if os.Getenv("BB_NLQ_URL") != "" {
		return false
	}
	cfg, _ := LoadConfig()
	if !cfg.Autostart.Enabled {
		return false
	}
	_, err := exec.LookPath("omlx")
	return err == nil
}

// Autostart spawns a detached `omlx serve`, logs to OmlxLogPath, and waits
// up to 20s for any candidate to become healthy.
func Autostart(logger *log.Logger) error {
	_, err := autostart(logger)
	return err
}

// autostart spawns a detached `omlx serve`, logs to OmlxLogPath, and waits
// up to 20s for any candidate to become healthy.
func autostart(logger *log.Logger) (probeResult, error) {
	bin, err := exec.LookPath("omlx")
	if err != nil {
		return probeResult{}, fmt.Errorf("no model server running and omlx is not installed")
	}
	if err := os.MkdirAll(ConfigDir(), 0o700); err != nil {
		return probeResult{}, err
	}
	logf, err := os.OpenFile(OmlxLogPath(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return probeResult{}, err
	}
	_ = logf.Chmod(0o600)
	fmt.Fprintf(logf, "\n--- bb autostart %s ---\n", time.Now().Format(time.RFC3339))
	cmd := exec.Command(bin, "serve")
	cmd.Stdout = logf
	cmd.Stderr = logf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} // survives our exit
	if err := cmd.Start(); err != nil {
		logf.Close()
		return probeResult{}, fmt.Errorf("starting omlx: %w", err)
	}
	logger.Info("autostart", "pid", cmd.Process.Pid, "log", OmlxLogPath())
	_ = cmd.Process.Release()
	logf.Close()

	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if healthy, _ := probeAll(candidates()); healthy.err == nil {
			return healthy, nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return probeResult{}, fmt.Errorf("omlx did not become healthy within 20s (log: %s)", OmlxLogPath())
}

// --- resolution ---

type env struct {
	nlqURL, nlqModel, nlqKey          string
	analystURL, analystModel, analKey string
}

func readEnv() env {
	return env{
		nlqURL:       os.Getenv("BB_NLQ_URL"),
		nlqModel:     os.Getenv("BB_NLQ_MODEL"),
		nlqKey:       os.Getenv("BB_NLQ_KEY"),
		analystURL:   os.Getenv("BB_ANALYST_URL"),
		analystModel: os.Getenv("BB_ANALYST_MODEL"),
		analKey:      os.Getenv("BB_ANALYST_KEY"),
	}
}

func (e env) coversEverything() bool {
	return e.nlqURL != "" && e.nlqModel != "" &&
		(e.analystURL != "" || e.nlqURL != "") && e.analystModel != ""
}

// Resolve runs one quick pass: config validation, discovery, ranking,
// persistence, then env overrides on top. It never blocks on autostart —
// callers see NoServer and stage Autostart themselves (with UI feedback).
func Resolve(logger *log.Logger) Resolved {
	t0 := time.Now()
	e := readEnv()
	cfg, hadCfg := LoadConfig()

	var r Resolved
	var timings []string
	var healthy probeResult

	// Fully env-specified setups skip discovery entirely.
	if e.coversEverything() {
		r.Compiler = Role{URL: e.nlqURL, Key: e.nlqKey, KeySource: keySrc(e.nlqKey), Model: e.nlqModel, Via: "env"}
		aurl := e.analystURL
		if aurl == "" {
			aurl = e.nlqURL
		}
		akey := e.analKey
		if akey == "" {
			akey = e.nlqKey
		}
		r.Analyst = Role{URL: aurl, Key: akey, KeySource: keySrc(akey), Model: e.analystModel, Via: "env"}
		r.Timing = "env-specified, no discovery"
		return r
	}

	// 1. A saved config gets validated first (one probe).
	usedConfig := false
	if hadCfg && cfg.Server.URL != "" && e.nlqURL == "" {
		tp := time.Now()
		res := probe(candidate{url: cfg.Server.URL, keyFile: cfg.Server.KeyFile, via: cfg.Server.Via})
		timings = append(timings, fmt.Sprintf("config probe %dms", time.Since(tp).Milliseconds()))
		if res.err == nil && hasModel(res.models, cfg.Models.Compiler) && hasModel(res.models, cfg.Models.Analyst) {
			healthy = res
			usedConfig = true
		} else {
			r.Notice = "saved model server changed — re-discovering"
			logger.Info("config stale", "url", cfg.Server.URL, "err", res.err)
		}
	}

	// 2. Discovery: parallel probes over the candidate list.
	if healthy.err != nil || healthy.url == "" {
		tp := time.Now()
		h, all := probeAll(candidates())
		timings = append(timings, fmt.Sprintf("probes %dms", time.Since(tp).Milliseconds()))
		healthy = h
		if healthy.url == "" {
			r.Err = noServerError(all)
			r.NoServer = true
			r.Timing = strings.Join(timings, " · ")
			return r
		}
	}

	// 4. Rank what the server actually has.
	compiler := cfg.Models.Compiler
	analyst := cfg.Models.Analyst
	if !usedConfig || !hasModel(healthy.models, compiler) || !hasModel(healthy.models, analyst) {
		compiler = PickCompiler(healthy.models)
		analyst = PickAnalyst(healthy.models)
	}
	if compiler == "" {
		r.Err = fmt.Sprintf("%s is healthy but serves no usable text model", healthy.via)
		r.Timing = strings.Join(timings, " · ")
		return r
	}
	if analyst == "" {
		analyst = compiler
	}

	key := ReadKey(healthy.keyFile)
	r.Compiler = Role{URL: healthy.url, Key: key, KeySource: healthy.keyFile, Model: compiler, Via: healthy.via}
	r.Analyst = Role{URL: healthy.url, Key: key, KeySource: healthy.keyFile, Model: analyst, Via: healthy.via}
	r.Models = healthy.models

	// 5. Persist (never env values, never the key itself).
	newCfg := cfg
	newCfg.Server.URL = healthy.url
	newCfg.Server.KeyFile = healthy.keyFile
	newCfg.Server.Via = healthy.via
	newCfg.Models.Compiler = compiler
	newCfg.Models.Analyst = analyst
	if newCfg != cfg || !hadCfg {
		if err := SaveConfig(newCfg); err != nil {
			logger.Error("config save", "err", err)
		} else if hadCfg && !usedConfig {
			if r.Notice == "" {
				r.Notice = "model setup changed — config updated"
			}
		}
	}

	// 6. Env overrides win per field.
	applyEnv(&r.Compiler, e.nlqURL, e.nlqModel, e.nlqKey)
	aurl, akey := e.analystURL, e.analKey
	if aurl == "" {
		aurl = e.nlqURL
	}
	if akey == "" {
		akey = e.nlqKey
	}
	applyEnv(&r.Analyst, aurl, e.analystModel, akey)

	timings = append(timings, fmt.Sprintf("total %dms", time.Since(t0).Milliseconds()))
	r.Timing = strings.Join(timings, " · ")
	logger.Info("resolved", "compiler", r.Compiler.Model, "analyst", r.Analyst.Model, "via", r.Compiler.Via, "timing", r.Timing)
	return r
}

func applyEnv(role *Role, url, model, key string) {
	if url != "" {
		role.URL = url
		role.Via = "env"
	}
	if model != "" {
		role.Model = model
	}
	if key != "" {
		role.Key = key
		role.KeySource = "env"
	}
}

func keySrc(key string) string {
	if key == "" {
		return ""
	}
	return "env"
}

func hasModel(models []string, m string) bool {
	if m == "" {
		return false
	}
	for _, x := range models {
		if x == m {
			return true
		}
	}
	return false
}

func noServerError(all []probeResult) string {
	var parts []string
	for _, r := range all {
		parts = append(parts, fmt.Sprintf("%s: %v", r.via, shortErr(r.err)))
	}
	return "no local model server found (" + strings.Join(parts, " · ") + ")"
}

func shortErr(err error) string {
	if err == nil {
		return "?"
	}
	s := err.Error()
	if strings.Contains(s, "connection refused") {
		return "not running"
	}
	if strings.Contains(s, "Timeout") || strings.Contains(s, "deadline") {
		return "timeout"
	}
	return s
}

// Summary is the footer line: "4B via omlx:8001 · analyst 35B".
func (r Resolved) Summary() string {
	if r.Err != "" {
		return ""
	}
	s := r.Compiler.Short() + " via " + r.Compiler.Via + " · analyst " + r.Analyst.Short()
	if r.Analyst.Via != r.Compiler.Via {
		s += " via " + r.Analyst.Via
	}
	return s
}
