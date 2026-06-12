// Package install makes claude-acc reachable from any shell and manages the
// on-disk binary: it copies the running binary into a canonical per-user bin
// directory, puts that directory on the user's PATH, and reverses both on
// unregister. It also owns replacing the running binary in place (used by the
// updater) and the Windows self-delete dance.
//
//	Windows : %LOCALAPPDATA%\claude-acc\claude-acc.exe + user PATH (HKCU\Environment)
//	Unix    : ~/.local/bin/claude-acc                  + PATH export in shell rc
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

// Register puts the per-user bin directory on the user's PATH and installs shell
// tab-completion. It does not copy, move, or rewrite any executable: the binary
// is placed at installedBinaryPath() by the installer script (install.ps1 /
// install.sh) and runs from there, so register only wires up the surrounding
// environment for the binary that already exists.
func Register() error {
	dir := installDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	dst := installedBinaryPath()
	if self, err := paths.SelfPath(); err == nil && !paths.PathEqual(self, dst) {
		// We are not the installed copy (e.g. a local build, or the binary was
		// run straight from Downloads). We deliberately do not copy ourselves
		// into place - that is the installer's job, and a binary that writes
		// executables into a user dir is exactly what endpoint security flags -
		// so just tell the user where a managed install would live.
		fmt.Printf("Note: running from %s (not the install location %s).\n", self, dst)
		fmt.Printf("Re-run the installer for a managed install; PATH is still being set for %s.\n", dir)
	}

	fmt.Printf("Registered '%s' on PATH (%s)\n", paths.Bin, dir)

	msg, err := addUserPath(dir)
	if err != nil {
		return err
	}
	fmt.Print(msg)

	// Tab completion is a convenience, not the point of register: a failure here
	// must not fail the command (the binary is already installed and on PATH).
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
		// Unix can unlink even its own running image (the inode lives on until
		// the process exits). On Windows the running .exe is locked, so a binary
		// unregistering itself cannot delete its own image; the message below
		// tells the user to remove the folder, and a later run sweeps any
		// leftover. We still delete here when the running binary is *not* the
		// installed copy (e.g. unregistering from a downloaded build).
		if !paths.PathEqual(self, dst) || runtime.GOOS != "windows" {
			if err := os.Remove(dst); err == nil {
				removedBinary = true
			}
		}
	}
	_ = removeUserPath(dir)
	removeCompletion()

	// Clean up the install folder itself, covering the cases where the binary
	// was deleted outright or was already absent. removeInstallDir only acts on
	// the dedicated per-tool folder and only when it is empty.
	if removedBinary || !paths.FileExists(dst) {
		removeInstallDir(dir)
	}

	switch {
	case removedBinary:
		fmt.Printf("Unregistered '%s' (removed the installed binary and PATH entry).\n", paths.Bin)
	case paths.FileExists(dst):
		fmt.Printf("Unregistered '%s' (removed the PATH entry).\n", paths.Bin)
		fmt.Printf("Could not delete the running binary; remove %s manually.\n", filepath.Dir(dst))
	default:
		fmt.Printf("Unregistered '%s' (removed the PATH entry; no installed binary was found).\n", paths.Bin)
	}
	fmt.Println("(Your active Claude Code login is not touched - this only removes the tool.)")
	if purge {
		// Take the profile lock so we don't delete the directory out from under
		// a concurrent save/switch. Best-effort: if the lock can't be taken we
		// still proceed, since unregister is the user explicitly tearing down.
		if release, err := lock.Acquire(); err == nil {
			defer release()
		}
		if err := os.RemoveAll(paths.ProfileDir); err == nil {
			fmt.Printf("Removed saved profiles at %s\n", paths.ProfileDir)
		}
	} else {
		fmt.Printf("Saved profiles kept at %s (run '%s unregister --purge' to delete them too).\n", paths.ProfileDir, paths.Bin)
	}
	return nil
}

// removeInstallDir deletes the install folder, but only the dedicated per-tool
// directory we create on Windows (%LOCALAPPDATA%\claude-acc). On Unix the
// install dir is a shared location (~/.local/bin) that must never be removed.
// os.Remove only deletes an empty directory, so a folder that still holds other
// files is left intact.
func removeInstallDir(dir string) {
	if runtime.GOOS != "windows" || filepath.Base(dir) != paths.Bin {
		return
	}
	_ = os.Remove(dir)
}
