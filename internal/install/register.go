// Package install makes acc-claude reachable from any shell: register puts the
// per-user bin directory on the user's PATH and installs shell tab-completion,
// and unregister reverses both (deleting the installed binary where possible).
// It never copies, downloads, or rewrites any executable — placing the binary
// is the installer scripts' job (install.ps1 / install.sh); a program that
// writes executables into a user dir is exactly the dropper shape endpoint
// security flags.
//
//	Windows : %LOCALAPPDATA%\acc-claude\acc-claude.exe + user PATH (HKCU\Environment)
//	Unix    : ~/.local/bin/acc-claude                  + PATH export in shell rc
package install

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/Samseys/anthropic-account-switcher/internal/lock"
	"github.com/Samseys/anthropic-account-switcher/internal/paths"
)

func installDir() string {
	if runtime.GOOS == "windows" {
		base := os.Getenv("LOCALAPPDATA")
		if base == "" {
			base = filepath.Join(paths.Home, "AppData", "Local")
		}
		return filepath.Join(base, paths.Bin)
	}
	return filepath.Join(paths.Home, ".local", "bin")
}

func installedBinaryPath() string {
	name := paths.Bin
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(installDir(), name)
}

// Register puts the per-user bin directory on PATH and installs shell completion.
// Does not copy or rewrite any executable — the installer script does that.
func Register() error {
	dir := installDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	dst := installedBinaryPath()
	if self, err := paths.SelfPath(); err == nil && !paths.PathEqual(self, dst) {
		// Not the installed copy (local build or run from Downloads). Tell the
		// user where a managed install would live; PATH is still being wired.
		fmt.Printf("Note: running from %s (not the install location %s).\n", self, dst)
		fmt.Printf("Re-run the installer for a managed install; PATH is still being set for %s.\n", dir)
	}

	msg, err := addUserPath(dir)
	if err != nil {
		return err
	}
	fmt.Printf("Registered '%s' on PATH (%s)\n", paths.Bin, dir)
	fmt.Print(msg)

	// Completion failure must not fail the command: the binary is already on PATH.
	if msg, err := installCompletion(); err != nil {
		fmt.Printf("Note: could not enable tab completion automatically (%v).\n", err)
		fmt.Printf("Enable it manually with '%s completion <shell>'.\n", paths.Bin)
	} else {
		fmt.Print(msg)
	}
	return nil
}

// Unregister removes the installed binary and the PATH entry. With purge it
// also deletes the saved profiles.
func Unregister(purge bool) error {
	dir := installDir()

	removedBinary := false
	dst := installedBinaryPath()
	if paths.FileExists(dst) {
		self, _ := paths.SelfPath()
		// On Windows the running .exe is locked and cannot delete itself; on
		// Unix the inode lives until the process exits, so unlink is safe there.
		if !paths.PathEqual(self, dst) || runtime.GOOS != "windows" {
			if err := os.Remove(dst); err == nil {
				removedBinary = true
			}
		}
	}
	_ = removeUserPath(dir)
	removeCompletion()

	if removedBinary || !paths.FileExists(dst) {
		removeInstallDir(dir)
	}

	leftoverDir := ""
	switch {
	case removedBinary:
		fmt.Printf("Unregistered '%s' (removed the installed binary and PATH entry).\n", paths.Bin)
	case paths.FileExists(dst):
		fmt.Printf("Unregistered '%s' (removed the PATH entry).\n", paths.Bin)
		// The running .exe can't delete itself on Windows; user must do it manually.
		leftoverDir = filepath.Dir(dst)
	default:
		fmt.Printf("Unregistered '%s' (removed the PATH entry; no installed binary was found).\n", paths.Bin)
	}
	fmt.Println("(Your active Claude Code login is not touched - this only removes the tool.)")
	if purge {
		// Acquire then immediately release: holding the lock while calling
		// RemoveAll would keep an open handle inside the directory, blocking
		// removal on Windows. Best-effort: proceed even if the lock fails.
		if release, err := lock.Acquire(); err == nil {
			release()
		}
		if err := os.RemoveAll(paths.ProfileDir); err == nil {
			fmt.Printf("Removed saved profiles at %s\n", paths.ProfileDir)
		}
	} else {
		fmt.Printf("Saved profiles kept at %s (run '%s unregister --purge' to delete them too).\n", paths.ProfileDir, paths.Bin)
	}

	if leftoverDir != "" {
		fmt.Println()
		fmt.Println("  ACTION REQUIRED: the running binary could not delete itself.")
		fmt.Printf("  Delete this folder manually to finish removing the tool:\n\n")
		fmt.Printf("      %s\n\n", leftoverDir)
	}
	return nil
}

// removeInstallDir removes the install folder only on Windows (the dedicated
// per-tool dir). On Unix ~/.local/bin is shared and must not be removed.
// os.Remove silently skips non-empty directories.
func removeInstallDir(dir string) {
	if runtime.GOOS != "windows" || filepath.Base(dir) != paths.Bin {
		return
	}
	_ = os.Remove(dir)
}
