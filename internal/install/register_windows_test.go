//go:build windows

package install

import (
	"os"
	"strings"
	"testing"

	"golang.org/x/sys/windows/registry"

	"github.com/Samseys/anthropic-account-switcher/internal/paths"
)

// TestRegisterUnregisterLeavesNoTraces runs a full register -> unregister round
// trip against redirected state (scratch PATH key, stubbed PowerShell profile,
// temp install dir) and asserts unregister removes every trace it added — the
// PATH entry, the completion block, the installed binary, and the install dir —
// while leaving pre-existing user content untouched.
func TestRegisterUnregisterLeavesNoTraces(t *testing.T) {
	withScratchEnvKey(t)
	profile := withScratchProfiles(t, "Microsoft.PowerShell_profile.ps1")[0]
	t.Setenv("LOCALAPPDATA", t.TempDir()) // installDir() = %LOCALAPPDATA%\acc-claude

	// Seed pre-existing content on both sides to prove it survives unregister.
	const existingPath = `C:\Windows\System32`
	if err := setUserPath(existingPath, registry.EXPAND_SZ); err != nil {
		t.Fatal(err)
	}
	const seedProfile = "# my prompt\nfunction prompt { 'PS> ' }\n"
	if err := os.WriteFile(profile, []byte(seedProfile), 0o644); err != nil {
		t.Fatal(err)
	}

	// The installer (install.ps1) places the binary; Register only wires up PATH
	// and completion for it. Stage a stand-in at the install location so the
	// round trip can later assert unregister removes it.
	dst := installedBinaryPath()
	if err := os.MkdirAll(installDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	// --- register ---
	if err := Register(); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if p := currentPath(t); !strings.Contains(strings.ToLower(p), strings.ToLower(installDir())) {
		t.Fatalf("register did not add install dir to PATH: %q", p)
	}
	if prof, _ := paths.ReadFileOpt(profile); !strings.Contains(prof, completionMarker) {
		t.Fatalf("register did not add completion block:\n%s", prof)
	}
	if !paths.FileExists(dst) {
		t.Fatalf("register did not install the binary at %s", dst)
	}

	// --- unregister ---
	if err := Unregister(false); err != nil {
		t.Fatalf("Unregister: %v", err)
	}

	// PATH: back to exactly the pre-existing value.
	if p := currentPath(t); p != existingPath {
		t.Errorf("TRACE LEFT in PATH: %q, want %q", p, existingPath)
	}
	// Profile: completion block gone, user content intact.
	prof, _ := paths.ReadFileOpt(profile)
	if strings.Contains(prof, completionMarker) {
		t.Errorf("TRACE LEFT: completion block still in profile:\n%s", prof)
	}
	if !strings.Contains(prof, "function prompt") {
		t.Errorf("unregister clobbered user profile content:\n%s", prof)
	}
	// Binary and install dir: removed.
	if paths.FileExists(dst) {
		t.Errorf("TRACE LEFT: installed binary still at %s", dst)
	}
	if paths.FileExists(installDir()) {
		t.Errorf("TRACE LEFT: install dir still at %s", installDir())
	}
}

// TestRegisterIsIdempotent proves a re-run converges to the same state: the user
// PATH and the completion profile are unchanged after the second Register, with
// no duplicate install-dir entry or completion block.
func TestRegisterIsIdempotent(t *testing.T) {
	withScratchEnvKey(t)
	profile := withScratchProfiles(t, "Microsoft.PowerShell_profile.ps1")[0]
	t.Setenv("LOCALAPPDATA", t.TempDir())

	// Stage the binary the installer would have placed.
	if err := os.MkdirAll(installDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(installedBinaryPath(), []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := Register(); err != nil {
		t.Fatalf("first Register: %v", err)
	}
	firstPath := currentPath(t)
	firstProfile, _ := paths.ReadFileOpt(profile)

	if err := Register(); err != nil {
		t.Fatalf("second Register: %v", err)
	}
	if got := currentPath(t); got != firstPath {
		t.Errorf("Register not idempotent; PATH changed on re-run:\nfirst:  %q\nsecond: %q", firstPath, got)
	}
	if got, _ := paths.ReadFileOpt(profile); got != firstProfile {
		t.Errorf("Register not idempotent; profile changed on re-run:\n--- first ---\n%s\n--- second ---\n%s", firstProfile, got)
	}
	if n := strings.Count(currentPath(t), installDir()); n != 1 {
		t.Errorf("want exactly one install-dir PATH entry, got %d: %q", n, currentPath(t))
	}
	if got, _ := paths.ReadFileOpt(profile); strings.Count(got, completionMarker) != 1 {
		t.Errorf("want exactly one completion block:\n%s", got)
	}
}
