package hook

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// settings.go installs/removes the bb hooks in the Claude Code user
// settings (~/.claude/settings.json), preserving everything else in the file.
// Two hooks are installed:
//   - a Stop hook (`<binary> hook-ingest`) that fires every turn and means
//     register/refresh — it pushes the beads the agent name-dropped;
//   - a SessionEnd hook (`<binary> hook-end`) that fires ONCE, at the end of a
//     conversation, and archives that session's channel.
//
// The Stop hook fires every turn, so it cannot also mean "ended" — SessionEnd is
// the distinct, once-per-conversation archive trigger, and no signal is
// overloaded. Each hook command is identified by its own marker so install never
// double-adds and uninstall removes exactly ours.

// HookMarker identifies OUR Stop-hook command (register/refresh). SessionEndMarker
// identifies OUR SessionEnd-hook command (archive). Neither is a substring of the
// other, so the two are told apart exactly.
const (
	HookMarker       = "hook-ingest"
	SessionEndMarker = "hook-end"
)

// hookSpec is one Claude Code hook we manage: the event it hangs off, the CLI
// subcommand the binary runs, and the marker that identifies it.
type hookSpec struct {
	event  string
	sub    string
	marker string
}

// managedHooks lists every hook bb installs. Install/Uninstall iterate it,
// so adding a hook is a one-line change here.
var managedHooks = []hookSpec{
	{event: "Stop", sub: HookMarker, marker: HookMarker},
	{event: "SessionEnd", sub: SessionEndMarker, marker: SessionEndMarker},
}

// SettingsPath is ~/.claude/settings.json (CLAUDE_CONFIG_DIR overrides the dir).
func SettingsPath() string {
	dir := os.Getenv("CLAUDE_CONFIG_DIR")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".claude")
	}
	return filepath.Join(dir, "settings.json")
}

// command is the hook command line: the bb binary + the subcommand.
func (h hookSpec) command(binary string) string { return binary + " " + h.sub }

// Install adds the Stop and SessionEnd hooks to the settings file, creating the
// file if needed. Idempotent per hook (a hook already present is skipped, so an
// older install missing SessionEnd gains only SessionEnd). Returns whether it
// changed anything.
func Install(binary string) (changed bool, err error) {
	settings, err := loadSettings()
	if err != nil {
		return false, err
	}
	hooks := mapChild(settings, "hooks")
	for _, h := range managedHooks {
		entries := sliceChild(hooks, h.event)
		if sliceHasMarker(entries, h.marker) {
			continue // already installed
		}
		entry := map[string]any{
			"hooks": []any{
				map[string]any{"type": "command", "command": h.command(binary)},
			},
		}
		hooks[h.event] = append(entries, entry)
		changed = true
	}
	if !changed {
		return false, nil
	}
	settings["hooks"] = hooks
	return true, saveSettings(settings)
}

// Uninstall removes every hook entry whose command carries one of our markers,
// across both events.
func Uninstall() (changed bool, err error) {
	settings, err := loadSettings()
	if err != nil {
		return false, err
	}
	hooks, ok := settings["hooks"].(map[string]any)
	if !ok {
		return false, nil
	}
	for _, h := range managedHooks {
		entries, ok := hooks[h.event].([]any)
		if !ok {
			continue
		}
		var kept []any
		for _, e := range entries {
			if entryHasMarker(e, h.marker) {
				changed = true
				continue
			}
			kept = append(kept, e)
		}
		if len(kept) == 0 {
			delete(hooks, h.event)
		} else {
			hooks[h.event] = kept
		}
	}
	if !changed {
		return false, nil
	}
	if len(hooks) == 0 {
		delete(settings, "hooks")
	}
	return true, saveSettings(settings)
}

// Installed reports whether our Stop hook (the register/refresh anchor) is
// present. Install is idempotent, so running it again backfills a missing
// SessionEnd hook even when this already returns true.
func Installed() bool {
	settings, err := loadSettings()
	if err != nil {
		return false
	}
	hooks, _ := settings["hooks"].(map[string]any)
	stop, _ := hooks["Stop"].([]any)
	return sliceHasMarker(stop, HookMarker)
}

func sliceHasMarker(entries []any, marker string) bool {
	for _, e := range entries {
		if entryHasMarker(e, marker) {
			return true
		}
	}
	return false
}

func entryHasMarker(e any, marker string) bool {
	m, ok := e.(map[string]any)
	if !ok {
		return false
	}
	inner, ok := m["hooks"].([]any)
	if !ok {
		return false
	}
	for _, h := range inner {
		hm, ok := h.(map[string]any)
		if !ok {
			continue
		}
		if cmd, _ := hm["command"].(string); strings.Contains(cmd, marker) {
			return true
		}
	}
	return false
}

func loadSettings() (map[string]any, error) {
	raw, err := os.ReadFile(SettingsPath())
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]any{}, nil
		}
		return nil, err
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return map[string]any{}, nil
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("%s is not valid JSON: %w", SettingsPath(), err)
	}
	return m, nil
}

func saveSettings(m map[string]any) error {
	if err := os.MkdirAll(filepath.Dir(SettingsPath()), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(SettingsPath(), append(raw, '\n'), 0o644)
}

func mapChild(m map[string]any, key string) map[string]any {
	if c, ok := m[key].(map[string]any); ok {
		return c
	}
	return map[string]any{}
}

func sliceChild(m map[string]any, key string) []any {
	if c, ok := m[key].([]any); ok {
		return c
	}
	return nil
}
