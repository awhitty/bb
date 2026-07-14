package discover

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/log"
	"io"
)

// Alex's actual omlx model list — the ranking must handle the real thing.
var alexModels = []string{
	"DeepSeek-V4-Flash-2bit-DQ",
	"Qwen3.5-4B-MLX-4bit",
	"Qwen3.5-9B-Uncensored-HauhauCS-Aggressive-MLX-mxfp4",
	"Qwen3.6-35B-A3B-UD-MLX-4bit",
	"gemma-4-31b-it-4bit",
	"gpt-oss-120b-MXFP4-Q8",
	"mlx-community--LocateAnything-3B-8bit",
	"mlx-community--Qwen3-1.7B-4bit",
	"mlx-community--chatterbox-turbo-8bit",
	"mlx-community--parakeet-tdt-0.6b-v3",
	"Qwen3.5-35B-A3B-Uncensored-HauhauCS-Aggressive-MLX-mxfp4",
	"Qwen3.6-35B-A3B-Uncensored-HauhauCS-Aggressive",
}

func TestPickCompiler(t *testing.T) {
	if got := PickCompiler(alexModels); got != "Qwen3.5-4B-MLX-4bit" {
		t.Fatalf("compiler = %q", got)
	}
	// Without a 4B, the 1.7B-class instruct model wins over a 9B uncensored.
	if got := PickCompiler([]string{
		"mlx-community--Qwen3-1.7B-4bit",
		"Qwen3.5-9B-Uncensored-HauhauCS-Aggressive-MLX-mxfp4",
		"mlx-community--parakeet-tdt-0.6b-v3",
	}); got != "mlx-community--Qwen3-1.7B-4bit" {
		t.Fatalf("compiler fallback = %q", got)
	}
	// Sub-1.5B and non-text models never qualify.
	if got := PickCompiler([]string{"tiny-0.6b", "mlx-community--parakeet-tdt-0.6b-v3", "whisper-large-v3"}); got != "" {
		t.Fatalf("compiler from junk = %q", got)
	}
}

func TestPickAnalyst(t *testing.T) {
	// MoE (A3B) beats the bigger dense 120B; standard beats Uncensored.
	if got := PickAnalyst(alexModels); got != "Qwen3.6-35B-A3B-UD-MLX-4bit" {
		t.Fatalf("analyst = %q", got)
	}
	// All-uncensored world: still picks the biggest MoE rather than nothing.
	if got := PickAnalyst([]string{
		"Qwen3.5-35B-A3B-Uncensored-HauhauCS-Aggressive-MLX-mxfp4",
		"Qwen3.5-9B-Uncensored-HauhauCS-Aggressive-MLX-mxfp4",
	}); got != "Qwen3.5-35B-A3B-Uncensored-HauhauCS-Aggressive-MLX-mxfp4" {
		t.Fatalf("analyst all-uncensored = %q", got)
	}
	// No MoE: largest dense text model.
	if got := PickAnalyst([]string{"gemma-4-31b-it-4bit", "Qwen3.5-4B-MLX-4bit"}); got != "gemma-4-31b-it-4bit" {
		t.Fatalf("analyst dense = %q", got)
	}
}

func TestSizeOf(t *testing.T) {
	cases := map[string]float64{
		"Qwen3.5-4B-MLX-4bit":                 4,   // 4bit must not read as 4B… it does via "4B-"; the quant suffix "4bit" must not
		"mlx-community--Qwen3-1.7B-4bit":      1.7, // ditto
		"Qwen3.6-35B-A3B-UD-MLX-4bit":         35,  // total params, not the A3B active count
		"gpt-oss-120b-MXFP4-Q8":               120,
		"llama3.2:3b":                         3,
		"gemma-4-31b-it-4bit":                 31,
		"DeepSeek-V4-Flash-2bit-DQ":           0, // no size in name
		"mlx-community--parakeet-tdt-0.6b-v3": 0.6,
	}
	for in, want := range cases {
		if got := sizeOf(in); got != want {
			t.Fatalf("sizeOf(%q) = %v, want %v", in, got, want)
		}
	}
}

func fakeServer(t *testing.T, models []string, wantKey string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if wantKey != "" && r.Header.Get("Authorization") != "Bearer "+wantKey {
			w.WriteHeader(401)
			_, _ = w.Write([]byte(`{"error":{"message":"API key required"}}`))
			return
		}
		var data []map[string]string
		for _, m := range models {
			data = append(data, map[string]string{"id": m})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
}

func TestReadKeyFromOmlxSettings(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(path, []byte(`{"server":{"port":8123},"auth":{"api_key":"sekrit"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := ReadKey(path); got != "sekrit" {
		t.Fatalf("ReadKey = %q", got)
	}
	raw := filepath.Join(dir, "token")
	_ = os.WriteFile(raw, []byte("  plain-key\n"), 0o600)
	if got := ReadKey(raw); got != "plain-key" {
		t.Fatalf("ReadKey raw = %q", got)
	}
	if got := ReadKey(filepath.Join(dir, "missing")); got != "" {
		t.Fatalf("ReadKey missing = %q", got)
	}
}

func TestConfigRoundTripAndKeyNeverPersisted(t *testing.T) {
	t.Setenv("BB_CONFIG_DIR", t.TempDir())
	var c Config
	c.Server.URL = "http://127.0.0.1:8001/v1"
	c.Server.KeyFile = "/home/x/.omlx/settings.json"
	c.Server.Via = "omlx:8001"
	c.Models.Compiler = "Qwen3.5-4B-MLX-4bit"
	c.Models.Analyst = "Qwen3.6-35B-A3B-UD-MLX-4bit"
	c.Autostart.Enabled = true
	if err := SaveConfig(c); err != nil {
		t.Fatal(err)
	}
	got, ok := LoadConfig()
	if !ok || got != c {
		t.Fatalf("round trip: ok=%v got=%+v", ok, got)
	}
	raw, _ := os.ReadFile(filepath.Join(ConfigDir(), "config.toml"))
	if strings.Contains(string(raw), "sekrit") || strings.Contains(string(raw), "api_key =") {
		t.Fatalf("config must never carry a key:\n%s", raw)
	}
	if !strings.Contains(string(raw), "key_file") {
		t.Fatalf("config must record the key PATH:\n%s", raw)
	}
}

func TestResolveEnvOverridesWinWithoutDiscovery(t *testing.T) {
	t.Setenv("BB_CONFIG_DIR", t.TempDir())
	t.Setenv("BB_NLQ_URL", "http://example.test/v1")
	t.Setenv("BB_NLQ_MODEL", "my-model")
	t.Setenv("BB_NLQ_KEY", "k")
	t.Setenv("BB_ANALYST_MODEL", "big-model")
	r := Resolve(log.New(io.Discard))
	if r.Err != "" {
		t.Fatalf("err = %s", r.Err)
	}
	if r.Compiler.URL != "http://example.test/v1" || r.Compiler.Model != "my-model" || r.Compiler.Via != "env" {
		t.Fatalf("compiler = %+v", r.Compiler)
	}
	if r.Analyst.Model != "big-model" || r.Analyst.URL != "http://example.test/v1" || r.Analyst.Key != "k" {
		t.Fatalf("analyst = %+v", r.Analyst)
	}
}

func TestResolveDiscoversAndPersists(t *testing.T) {
	srv := fakeServer(t, alexModels, "")
	t.Setenv("BB_CONFIG_DIR", t.TempDir())
	for _, k := range []string{"BB_NLQ_URL", "BB_NLQ_MODEL", "BB_NLQ_KEY", "BB_ANALYST_URL", "BB_ANALYST_MODEL", "BB_ANALYST_KEY"} {
		t.Setenv(k, "")
	}
	// Point a config at the fake server so resolution has a candidate that
	// exists in the test environment (real ports aren't probed here).
	var c Config
	c.Server.URL = srv.URL + "/v1"
	c.Server.Via = "omlx:test"
	c.Models.Compiler = "Qwen3.5-4B-MLX-4bit"
	c.Models.Analyst = "Qwen3.6-35B-A3B-UD-MLX-4bit"
	c.Autostart.Enabled = false
	if err := SaveConfig(c); err != nil {
		t.Fatal(err)
	}
	r := Resolve(log.New(io.Discard))
	if r.Err != "" {
		t.Fatalf("err = %s", r.Err)
	}
	if r.Compiler.Model != "Qwen3.5-4B-MLX-4bit" || r.Analyst.Model != "Qwen3.6-35B-A3B-UD-MLX-4bit" {
		t.Fatalf("resolved = %+v / %+v", r.Compiler, r.Analyst)
	}
	if r.Summary() == "" || !strings.Contains(r.Summary(), "4B via omlx:test · analyst 35B") {
		t.Fatalf("summary = %q", r.Summary())
	}
	// A model env override still wins on top of a valid config.
	t.Setenv("BB_NLQ_MODEL", "mlx-community--Qwen3-1.7B-4bit")
	r = Resolve(log.New(io.Discard))
	if r.Compiler.Model != "mlx-community--Qwen3-1.7B-4bit" {
		t.Fatalf("env model override lost: %+v", r.Compiler)
	}
}
