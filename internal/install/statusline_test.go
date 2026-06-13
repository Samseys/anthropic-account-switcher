package install

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Samseys/anthropic-account-switcher/internal/paths"
)

// withSettings points paths.SettingsFile at a scratch file and restores it.
func withSettings(t *testing.T, initial string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "settings.json")
	old := paths.SettingsFile
	paths.SettingsFile = path
	t.Cleanup(func() { paths.SettingsFile = old })
	if initial != "" {
		if err := os.WriteFile(path, []byte(initial), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func settingsMap(t *testing.T, path string) map[string]json.RawMessage {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("settings is not valid JSON: %v\n%s", err, b)
	}
	return m
}

func TestInstallStatusLinePreservesOtherSettings(t *testing.T) {
	path := withSettings(t, `{"theme":"dark","permissions":{"allow":["Bash"]}}`)

	if _, err := InstallStatusLine(); err != nil {
		t.Fatalf("install: %v", err)
	}
	m := settingsMap(t, path)
	if _, ok := m["theme"]; !ok {
		t.Error("theme setting was dropped")
	}
	if _, ok := m["permissions"]; !ok {
		t.Error("permissions setting was dropped")
	}
	if sl, ok := m["statusLine"]; !ok || !isOurStatusLine(sl) {
		t.Errorf("statusLine missing or not ours: %s", sl)
	}

	// Idempotent: a second install succeeds and keeps valid JSON.
	if _, err := InstallStatusLine(); err != nil {
		t.Fatalf("second install: %v", err)
	}
	settingsMap(t, path)
}

func TestInstallStatusLineRefusesForeign(t *testing.T) {
	path := withSettings(t, `{"statusLine":{"type":"command","command":"my-own-bar.sh"}}`)
	if _, err := InstallStatusLine(); err == nil {
		t.Fatal("expected refusal to overwrite a custom status line")
	}
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), "my-own-bar.sh") {
		t.Errorf("custom status line was clobbered: %s", b)
	}
}

func TestInstallFromEmptyThenUninstall(t *testing.T) {
	path := withSettings(t, "") // no settings file at all

	if _, err := InstallStatusLine(); err != nil {
		t.Fatalf("install: %v", err)
	}
	if _, ok := settingsMap(t, path)["statusLine"]; !ok {
		t.Fatal("statusLine not written to a fresh settings file")
	}

	msg, err := UninstallStatusLine()
	if err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if msg == "" {
		t.Error("expected a removal message")
	}
	if _, ok := settingsMap(t, path)["statusLine"]; ok {
		t.Error("statusLine still present after uninstall")
	}
}

func TestUninstallLeavesForeignAlone(t *testing.T) {
	withSettings(t, `{"statusLine":{"type":"command","command":"my-own-bar.sh"}}`)
	if _, err := UninstallStatusLine(); err == nil {
		t.Fatal("expected uninstall to refuse removing a non-acc-claude status line")
	}
}

func TestUninstallNoSettingsIsQuiet(t *testing.T) {
	withSettings(t, "")
	msg, err := UninstallStatusLine()
	if err != nil {
		t.Fatalf("uninstall on missing settings: %v", err)
	}
	if msg != "" {
		t.Errorf("expected empty message, got %q", msg)
	}
}
