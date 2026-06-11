package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Samseys/anthropic-account-switcher/internal/claudejson"
)

// setupEnv points all path globals at a temp directory so the tests never
// touch the real Claude Code state.
func setupEnv(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "darwin" {
		t.Skip("skipped on macOS: writeCreds would touch the real login Keychain")
	}
	dir := t.TempDir()
	oldClaude, oldCred, oldCfg, oldProf := claudeDir, credFile, configFile, profileDir
	claudeDir = filepath.Join(dir, ".claude")
	credFile = filepath.Join(claudeDir, ".credentials.json")
	configFile = filepath.Join(dir, ".claude.json")
	profileDir = filepath.Join(claudeDir, "account-profiles")
	t.Cleanup(func() {
		claudeDir, credFile, configFile, profileDir = oldClaude, oldCred, oldCfg, oldProf
	})
	if err := os.MkdirAll(claudeDir, 0o700); err != nil {
		t.Fatal(err)
	}
}

// writeAccount simulates being logged in as the given account.
func writeAccount(t *testing.T, email, id, token string) {
	t.Helper()
	cfg := fmt.Sprintf(`{"oauthAccount": {"emailAddress": %q}, "userID": %q, "other": 1}`, email, id)
	if err := writeFileAtomic(configFile, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	creds := fmt.Sprintf(`{"claudeAiOauth":{"accessToken":%q,"expiresAt":4102444800000}}`, token)
	if err := writeFileAtomic(credFile, []byte(creds), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestSaveSwitchRoundTrip(t *testing.T) {
	setupEnv(t)

	writeAccount(t, "a@example.com", "id-a", "tok-a")
	if _, err := snapshot("work", true); err != nil {
		t.Fatal(err)
	}
	writeAccount(t, "b@example.com", "id-b", "tok-b")
	if _, err := snapshot("personal", true); err != nil {
		t.Fatal(err)
	}

	if got := activeProfile(); got != "personal" {
		t.Fatalf("activeProfile = %q, want personal", got)
	}

	// Stored snapshots must be encrypted on Windows, plaintext elsewhere.
	raw, err := os.ReadFile(filepath.Join(profileDir, "work", "credentials.json"))
	if err != nil {
		t.Fatal(err)
	}
	if encrypted := !bytes.HasPrefix(raw, []byte("{")); encrypted != (runtime.GOOS == "windows") {
		t.Fatalf("snapshot encrypted = %v on %s", encrypted, runtime.GOOS)
	}

	if err := cmdSwitch("work"); err != nil {
		t.Fatal(err)
	}
	live, err := os.ReadFile(credFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(live), "tok-a") {
		t.Fatalf("live credentials not restored: %s", live)
	}
	cfg, _ := readFileOpt(configFile)
	if v, _ := claudejson.TopLevelValue(cfg, "userID"); v != `"id-a"` {
		t.Fatalf("spliced userID = %s", v)
	}
	if e := claudejson.Field(cfg, "emailAddress"); e != "a@example.com" {
		t.Fatalf("spliced email = %q", e)
	}
	if !strings.Contains(cfg, `"other": 1`) {
		t.Fatal("unrelated config content was lost")
	}
	if got := activeProfile(); got != "work" {
		t.Fatalf("activeProfile after switch = %q, want work", got)
	}

	// `switch -` returns to the previous profile.
	if err := cmdSwitch("-"); err != nil {
		t.Fatal(err)
	}
	if got := activeProfile(); got != "personal" {
		t.Fatalf("activeProfile after 'switch -' = %q, want personal", got)
	}

	// A bare `switch` with exactly two profiles toggles to the other one.
	if err := cmdSwitch(""); err != nil {
		t.Fatal(err)
	}
	if got := activeProfile(); got != "work" {
		t.Fatalf("activeProfile after bare switch = %q, want work", got)
	}
}

func TestSwitchResavesOutgoingProfile(t *testing.T) {
	setupEnv(t)

	writeAccount(t, "a@example.com", "id-a", "tok-a")
	if _, err := snapshot("work", true); err != nil {
		t.Fatal(err)
	}
	writeAccount(t, "b@example.com", "id-b", "tok-b")
	if _, err := snapshot("personal", true); err != nil {
		t.Fatal(err)
	}

	// Claude Code rotates the active account's token in place...
	writeAccount(t, "b@example.com", "id-b", "tok-b-rotated")
	if err := cmdSwitch("work"); err != nil {
		t.Fatal(err)
	}
	// ...and the outgoing profile must have captured the rotation.
	pc, err := readProfileCreds(filepath.Join(profileDir, "personal"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(pc), "tok-b-rotated") {
		t.Fatalf("outgoing profile kept stale tokens: %s", pc)
	}
}

func TestSwitchToActiveProfileRefreshesSnapshot(t *testing.T) {
	setupEnv(t)

	writeAccount(t, "a@example.com", "id-a", "tok-a")
	if _, err := snapshot("work", true); err != nil {
		t.Fatal(err)
	}
	writeAccount(t, "a@example.com", "id-a", "tok-a-rotated")

	if err := cmdSwitch("work"); err != nil {
		t.Fatal(err)
	}
	// The live credentials must not have been downgraded to the old snapshot.
	live, _ := os.ReadFile(credFile)
	if !strings.Contains(string(live), "tok-a-rotated") {
		t.Fatalf("live credentials were overwritten: %s", live)
	}
	// And the snapshot picked up the rotated token.
	pc, err := readProfileCreds(filepath.Join(profileDir, "work"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(pc), "tok-a-rotated") {
		t.Fatalf("snapshot not refreshed: %s", pc)
	}
}

func TestSwitchUnknownProfile(t *testing.T) {
	setupEnv(t)
	writeAccount(t, "a@example.com", "id-a", "tok-a")
	if _, err := snapshot("work", true); err != nil {
		t.Fatal(err)
	}
	err := cmdSwitch("nope")
	if err == nil || !strings.Contains(err.Error(), "work") {
		t.Fatalf("want unknown-profile error listing available names, got %v", err)
	}
}

func TestRenameUpdatesPreviousMarker(t *testing.T) {
	setupEnv(t)

	writeAccount(t, "a@example.com", "id-a", "tok-a")
	if _, err := snapshot("work", true); err != nil {
		t.Fatal(err)
	}
	writeAccount(t, "b@example.com", "id-b", "tok-b")
	if _, err := snapshot("personal", true); err != nil {
		t.Fatal(err)
	}
	if err := cmdSwitch("work"); err != nil { // previous = personal
		t.Fatal(err)
	}
	if err := cmdRename("personal", "home"); err != nil {
		t.Fatal(err)
	}
	if err := cmdSwitch("-"); err != nil {
		t.Fatal(err)
	}
	if got := activeProfile(); got != "home" {
		t.Fatalf("activeProfile after rename + 'switch -' = %q, want home", got)
	}
}

func TestProtectCredsRoundTrip(t *testing.T) {
	in := []byte(`{"claudeAiOauth":{"accessToken":"secret"}}`)
	enc, err := protectCreds(in)
	if err != nil {
		t.Fatal(err)
	}
	out, err := unprotectCreds(enc)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(in, out) {
		t.Fatalf("round trip mismatch: %q", out)
	}
	if runtime.GOOS == "windows" && bytes.Equal(in, enc) {
		t.Fatal("DPAPI did not encrypt on Windows")
	}
}
