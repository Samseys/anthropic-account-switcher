//go:build !windows

package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Samseys/anthropic-account-switcher/internal/paths"
)

// TestRegisterUnregisterLeavesNoTraces runs a full register -> unregister round
// trip against a redirected HOME and asserts unregister removes every trace it
// added — the PATH block, the completion block (both land in the same rc), and
// the installed binary — while leaving the user's own rc content (including
// their own `export PATH=`) untouched.
//
// The install dir (~/.local/bin) is intentionally NOT removed on Unix: it is a
// shared location that may predate us and hold other tools, so its survival is
// correct behavior, not a leak.
func TestRegisterUnregisterLeavesNoTraces(t *testing.T) {
	home := withScratchHome(t, "bash")
	rc := filepath.Join(home, ".bashrc")

	// Seed pre-existing rc content, including the user's OWN PATH export, to
	// prove unregister removes only our marked blocks and not look-alikes.
	const userExport = `export PATH="$HOME/bin:$PATH"`
	seed := "# user rc\nalias ll='ls -l'\n" + userExport + "\n"
	if err := os.WriteFile(rc, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}

	// The installer (install.sh) places the binary; Register only wires up PATH
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
	after, _ := paths.ReadFileOpt(rc)
	if !strings.Contains(after, "# "+paths.Bin+"\nexport PATH=") {
		t.Fatalf("register did not add the PATH block:\n%s", after)
	}
	if !strings.Contains(after, completionMarker) {
		t.Fatalf("register did not add the completion block:\n%s", after)
	}
	if !paths.FileExists(dst) {
		t.Fatalf("register did not install the binary at %s", dst)
	}

	// --- unregister ---
	if err := Unregister(false); err != nil {
		t.Fatalf("Unregister: %v", err)
	}

	final, _ := paths.ReadFileOpt(rc)
	// No marker of ours remains (the PATH marker "# claude-acc" is a prefix of
	// the completion marker, so this single check covers both blocks).
	if strings.Contains(final, "# "+paths.Bin) {
		t.Errorf("TRACE LEFT: a claude-acc block remains in %s:\n%s", rc, final)
	}
	// The user's own content survived, including their own PATH export.
	if !strings.Contains(final, "alias ll='ls -l'") || !strings.Contains(final, userExport) {
		t.Errorf("unregister clobbered the user's rc content:\n%s", final)
	}
	// The installed binary is gone (the install dir itself is kept by design).
	if paths.FileExists(dst) {
		t.Errorf("TRACE LEFT: installed binary still at %s", dst)
	}
}

// TestUnregisterRemovesFishCompletion covers the fish path, where completion is
// a dedicated autoloaded file rather than an rc block.
func TestUnregisterRemovesFishCompletion(t *testing.T) {
	home := withScratchHome(t, "fish")

	if err := Register(); err != nil {
		t.Fatalf("Register: %v", err)
	}
	fishFile := filepath.Join(home, ".config", "fish", "completions", paths.Bin+".fish")
	if !paths.FileExists(fishFile) {
		t.Fatalf("register did not create the fish completion file at %s", fishFile)
	}

	if err := Unregister(false); err != nil {
		t.Fatalf("Unregister: %v", err)
	}
	if paths.FileExists(fishFile) {
		t.Errorf("TRACE LEFT: fish completion file still at %s", fishFile)
	}
}
