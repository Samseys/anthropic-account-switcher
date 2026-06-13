package install

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Samseys/anthropic-account-switcher/internal/paths"
)

// This file wires acc-claude's usage status line into ~/.claude/settings.json —
// the same family of per-user setup as PATH and shell completion, which is why it
// lives here and is attached to register/unregister. The status-line sensor
// itself (reading stdin, recording usage) lives in internal/profile.

// settingsStatusLine is the shape of the `statusLine` entry in settings.json.
type settingsStatusLine struct {
	Type    string `json:"type"`
	Command string `json:"command"`
	Padding int    `json:"padding,omitempty"`
}

// statuslineCommand is the command string written into settings.json. We use the
// bare binary name (resolved via PATH, as register sets up) so the entry is
// stable across upgrades and easy to recognize as ours.
func statuslineCommand() string { return paths.Bin + " statusline" }

// isOurStatusLine reports whether an existing statusLine entry is one we wrote, so
// we update/remove ours but never clobber a user's custom status line.
func isOurStatusLine(raw json.RawMessage) bool {
	var sl settingsStatusLine
	if err := json.Unmarshal(raw, &sl); err != nil {
		return false
	}
	cmd := strings.ToLower(sl.Command)
	return strings.Contains(cmd, "statusline") && strings.Contains(cmd, paths.Bin)
}

func readSettings(path string) (map[string]json.RawMessage, error) {
	raw := map[string]json.RawMessage{}
	b, ok := paths.ReadFileOpt(path)
	if !ok || strings.TrimSpace(b) == "" {
		return raw, nil
	}
	if err := json.Unmarshal([]byte(b), &raw); err != nil {
		return nil, fmt.Errorf("parsing %s: %w\nfix or remove that file, then retry", path, err)
	}
	return raw, nil
}

func writeSettings(path string, raw map[string]json.RawMessage) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return err
	}
	return paths.WriteFileAtomic(path, append(out, '\n'), 0o644)
}

// InstallStatusLine wires the statusline sensor into settings.json, preserving
// every other setting. It returns a human-readable summary and refuses (error) to
// overwrite a different, user-defined status line.
func InstallStatusLine() (string, error) {
	path := paths.SettingsFile
	raw, err := readSettings(path)
	if err != nil {
		return "", err
	}
	if existing, ok := raw["statusLine"]; ok && !isOurStatusLine(existing) {
		return "", fmt.Errorf("a different statusLine is already set in %s; leaving it untouched\n  %s\nmerge %q into it yourself to keep both",
			path, strings.TrimSpace(string(existing)), statuslineCommand())
	}
	already := false
	if existing, ok := raw["statusLine"]; ok && isOurStatusLine(existing) {
		already = true
	}

	entry, _ := json.Marshal(settingsStatusLine{Type: "command", Command: statuslineCommand(), Padding: 2})
	raw["statusLine"] = entry
	if err := writeSettings(path, raw); err != nil {
		return "", err
	}
	if already {
		return fmt.Sprintf("Usage status line already set up in %s.\n", path), nil
	}
	return fmt.Sprintf("Added the usage status line to %s (restart Claude Code to see it).\n", path), nil
}

// UninstallStatusLine removes the statusline entry we added, leaving a custom one
// (and all other settings) untouched. It returns "" when there was nothing of
// ours to remove.
func UninstallStatusLine() (string, error) {
	path := paths.SettingsFile
	raw, err := readSettings(path)
	if err != nil {
		return "", err
	}
	existing, ok := raw["statusLine"]
	if !ok {
		return "", nil
	}
	if !isOurStatusLine(existing) {
		return "", fmt.Errorf("the statusLine in %s is not acc-claude's; leaving it untouched", path)
	}
	delete(raw, "statusLine")
	if err := writeSettings(path, raw); err != nil {
		return "", err
	}
	return fmt.Sprintf("Removed the usage status line from %s.\n", path), nil
}
