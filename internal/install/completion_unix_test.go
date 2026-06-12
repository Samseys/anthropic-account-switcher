//go:build !windows

package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Samseys/anthropic-account-switcher/internal/paths"
)

// withScratchHome points paths.Home at a temp dir and sets $SHELL, so completion
// edits land in a throwaway rc file instead of the real one.
func withScratchHome(t *testing.T, shell string) string {
	t.Helper()
	dir := t.TempDir()
	oldHome := paths.Home
	paths.Home = dir
	t.Setenv("SHELL", "/usr/bin/"+shell)
	t.Cleanup(func() { paths.Home = oldHome })
	return dir
}

func TestInstallCompletionBashRoundTrip(t *testing.T) {
	home := withScratchHome(t, "bash")
	rc := filepath.Join(home, ".bashrc")
	const seed = "# user rc\nalias ll='ls -l'\n"
	if err := os.WriteFile(rc, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}

	msg, err := installCompletion()
	if err != nil || !strings.Contains(msg, "Enabled") {
		t.Fatalf("installCompletion msg=%q err=%v", msg, err)
	}
	got, _ := paths.ReadFileOpt(rc)
	if !strings.Contains(got, completionMarker) || !strings.Contains(got, "completion bash") {
		t.Fatalf(".bashrc missing completion block:\n%s", got)
	}
	if !strings.Contains(got, "alias ll='ls -l'") {
		t.Fatalf("removal clobbered user content:\n%s", got)
	}

	// Idempotent.
	if msg, err := installCompletion(); err != nil || !strings.Contains(msg, "already") {
		t.Fatalf("second install should be a no-op: msg=%q err=%v", msg, err)
	}
	got, _ = paths.ReadFileOpt(rc)
	if n := strings.Count(got, completionMarker); n != 1 {
		t.Fatalf("expected one completion block, found %d:\n%s", n, got)
	}

	removeCompletion()
	got, _ = paths.ReadFileOpt(rc)
	if strings.Contains(got, completionMarker) {
		t.Fatalf("completion block survived removal:\n%s", got)
	}
	if !strings.Contains(got, "alias ll='ls -l'") {
		t.Fatalf("removal clobbered user content:\n%s", got)
	}
}

func TestInstallCompletionFish(t *testing.T) {
	home := withScratchHome(t, "fish")
	if _, err := installCompletion(); err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(home, ".config", "fish", "completions", paths.Bin+".fish")
	body, ok := paths.ReadFileOpt(f)
	if !ok || !strings.Contains(body, "completion fish | source") {
		t.Fatalf("fish completion file not written correctly: ok=%v body=%q", ok, body)
	}
	removeCompletion()
	if paths.FileExists(f) {
		t.Fatal("fish completion file survived removal")
	}
}

func TestInstallCompletionUnknownShell(t *testing.T) {
	withScratchHome(t, "ksh")
	msg, err := installCompletion()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(msg, "completion") {
		t.Fatalf("expected manual instructions, got %q", msg)
	}
}

// TestInstallCompletionConfiguresAllExistingShells: the current shell is bash,
// but a .zshrc also exists, so completion must be installed for both — and not
// for fish, which the user has not set up.
func TestInstallCompletionConfiguresAllExistingShells(t *testing.T) {
	home := withScratchHome(t, "bash")
	bashrc := filepath.Join(home, ".bashrc")
	zshrc := filepath.Join(home, ".zshrc")
	if err := os.WriteFile(zshrc, []byte("# zsh\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	msg, err := installCompletion()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "bash") || !strings.Contains(msg, "zsh") {
		t.Errorf("expected both bash and zsh configured, msg=%q", msg)
	}
	for _, rc := range []string{bashrc, zshrc} {
		if got, _ := paths.ReadFileOpt(rc); !strings.Contains(got, completionMarker) {
			t.Errorf("%s missing completion block:\n%s", rc, got)
		}
	}
	// fish was never set up, so we must not have created its file.
	if paths.FileExists(filepath.Join(home, ".config", "fish", "completions", paths.Bin+".fish")) {
		t.Error("fish completion file created for a non-fish user")
	}
}

// TestRemoveCompletionClearsAllShells: register under bash, then unregister
// while the current shell is zsh — the bash block must still be removed.
func TestRemoveCompletionClearsAllShells(t *testing.T) {
	home := withScratchHome(t, "bash")
	bashrc := filepath.Join(home, ".bashrc")
	if err := os.WriteFile(bashrc, []byte("# bash\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installCompletion(); err != nil {
		t.Fatal(err)
	}
	if got, _ := paths.ReadFileOpt(bashrc); !strings.Contains(got, completionMarker) {
		t.Fatalf(".bashrc not configured:\n%s", got)
	}

	// Now the user is in zsh and runs unregister.
	t.Setenv("SHELL", "/usr/bin/zsh")
	removeCompletion()

	if got, _ := paths.ReadFileOpt(bashrc); strings.Contains(got, completionMarker) {
		t.Errorf("TRACE LEFT: bash completion block survived unregister under zsh:\n%s", got)
	}
}
