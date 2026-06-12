//go:build windows

package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Samseys/anthropic-account-switcher/internal/paths"
)

// withScratchProfiles points the PowerShell-profile resolver at throwaway files
// in a temp directory so completion edits never touch a real $PROFILE.
func withScratchProfiles(t *testing.T, names ...string) []string {
	t.Helper()
	dir := t.TempDir()
	var ps []string
	for _, n := range names {
		ps = append(ps, filepath.Join(dir, n))
	}
	old := powershellProfilePaths
	powershellProfilePaths = func() []string { return ps }
	t.Cleanup(func() { powershellProfilePaths = old })
	return ps
}

func TestInstallCompletionRoundTrip(t *testing.T) {
	profile := withScratchProfiles(t, "Microsoft.PowerShell_profile.ps1")[0]
	// Seed pre-existing content to prove we append and that it survives removal.
	const seed = "# my prompt\nfunction prompt { 'PS> ' }\n"
	if err := os.WriteFile(profile, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}

	msg, err := installCompletion()
	if err != nil || !strings.Contains(msg, "Enabled") {
		t.Fatalf("installCompletion msg=%q err=%v", msg, err)
	}
	got, _ := paths.ReadFileOpt(profile)
	if !strings.Contains(got, completionMarker) || !strings.Contains(got, loaderLine()) {
		t.Fatalf("profile missing completion block:\n%s", got)
	}
	if !strings.Contains(got, seed) {
		t.Fatalf("pre-existing profile content was clobbered:\n%s", got)
	}

	removeCompletion()
	got, _ = paths.ReadFileOpt(profile)
	if strings.Contains(got, completionMarker) {
		t.Fatalf("completion block survived removal:\n%s", got)
	}
	if !strings.Contains(got, "function prompt") {
		t.Fatalf("removal clobbered the user's own content:\n%s", got)
	}
}

func TestInstallCompletionIsIdempotent(t *testing.T) {
	profile := withScratchProfiles(t, "profile.ps1")[0]

	if _, err := installCompletion(); err != nil {
		t.Fatal(err)
	}
	if msg, err := installCompletion(); err != nil || !strings.Contains(msg, "already") {
		t.Fatalf("second install should be a no-op: msg=%q err=%v", msg, err)
	}
	got, _ := paths.ReadFileOpt(profile)
	if n := strings.Count(got, completionMarker); n != 1 {
		t.Fatalf("expected exactly one completion block, found %d:\n%s", n, got)
	}
}

func TestInstallCompletionMultipleHosts(t *testing.T) {
	// Both Windows PowerShell and PowerShell 7 present: each profile is edited.
	profiles := withScratchProfiles(t, "winps.ps1", "pwsh.ps1")
	if _, err := installCompletion(); err != nil {
		t.Fatal(err)
	}
	for _, p := range profiles {
		got, _ := paths.ReadFileOpt(p)
		if !strings.Contains(got, completionMarker) {
			t.Fatalf("profile %s was not configured:\n%s", p, got)
		}
	}
}

func TestInstallCompletionNoProfile(t *testing.T) {
	// No PowerShell resolved: fall back to manual instructions, no error.
	withScratchProfiles(t) // empty
	msg, err := installCompletion()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(msg, "$PROFILE") {
		t.Fatalf("expected manual instructions, got %q", msg)
	}
}
