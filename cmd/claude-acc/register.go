package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// register makes `claude-acc` callable from any shell by copying this binary
// into a canonical per-user bin directory and ensuring that directory is on the
// user's PATH. unregister reverses both steps.
//
//	Windows : %LOCALAPPDATA%\claude-acc\claude-acc.exe + user PATH (HKCU\Environment)
//	Unix    : ~/.local/bin/claude-acc                  + PATH export in shell rc

func installDir() string {
	if runtime.GOOS == "windows" {
		base := os.Getenv("LOCALAPPDATA")
		if base == "" {
			base = filepath.Join(home, "AppData", "Local")
		}
		return filepath.Join(base, bin)
	}
	return filepath.Join(home, ".local", "bin")
}

func installedBinaryPath() string {
	name := bin
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(installDir(), name)
}

func cmdRegister() error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	if resolved, err := filepath.EvalSymlinks(self); err == nil {
		self = resolved
	}

	dir := installDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	dst := installedBinaryPath()
	if !pathEqual(self, dst) {
		if err := copyFileAtomic(self, dst, 0o755); err != nil {
			return fmt.Errorf("copying binary to %s: %w", dst, err)
		}
	}

	fmt.Printf("Registered '%s' -> %s\n", bin, dst)

	if runtime.GOOS == "windows" {
		added, err := addWindowsUserPath(dir)
		if err != nil {
			return err
		}
		if added {
			fmt.Printf("Added '%s' to your user PATH. Open a NEW terminal for it to apply.\n", dir)
		} else {
			fmt.Printf("'%s' is already on your user PATH.\n", dir)
		}
		return nil
	}

	added, rc, err := addUnixPath(dir)
	if err != nil {
		return err
	}
	if added {
		fmt.Printf("Added '%s' to your PATH in %s.\n", dir, rc)
		fmt.Printf("Run 'source \"%s\"' or open a new terminal, then use '%s'.\n", rc, bin)
	} else {
		fmt.Printf("'%s' is already on your PATH; '%s' is ready to use.\n", dir, bin)
	}
	return nil
}

func cmdUnregister(purge bool) error {
	dir := installDir()

	removedBinary := false
	scheduledDelete := false
	dst := installedBinaryPath()
	if fileExists(dst) {
		self, _ := os.Executable()
		if resolved, err := filepath.EvalSymlinks(self); err == nil {
			self = resolved
		}
		if !pathEqual(self, dst) || runtime.GOOS != "windows" {
			// Unix can unlink even its own running image; the inode lives on
			// until the process exits.
			if err := os.Remove(dst); err == nil {
				removedBinary = true
			}
		} else if scheduleSelfDelete(dst) == nil {
			// Windows locks a running exe; a detached helper deletes it the
			// moment we exit.
			scheduledDelete = true
		}
	}
	if runtime.GOOS == "windows" {
		_ = removeWindowsUserPath(dir)
	} else {
		_ = removeUnixPath(dir)
	}

	// Clean up the install folder itself. The self-delete helper removes it
	// once the locked binary is gone; here we cover the cases where the binary
	// was deleted outright or was already absent. removeInstallDir only acts on
	// the dedicated per-tool folder and only when it is empty.
	if removedBinary || !scheduledDelete && !fileExists(dst) {
		removeInstallDir(dir)
	}

	switch {
	case removedBinary:
		fmt.Printf("Unregistered '%s' (removed the installed binary and PATH entry).\n", bin)
	case scheduledDelete:
		fmt.Printf("Unregistered '%s' (removed the PATH entry).\n", bin)
		fmt.Printf("The installed binary at %s will delete itself a moment after this command exits.\n", dst)
	case fileExists(dst):
		fmt.Printf("Unregistered '%s' (removed the PATH entry).\n", bin)
		fmt.Printf("Could not delete the installed binary; remove %s manually.\n", dst)
	default:
		fmt.Printf("Unregistered '%s' (removed the PATH entry; no installed binary was found).\n", bin)
	}
	fmt.Println("(Your active Claude Code login is not touched - this only removes the tool.)")
	if purge {
		if err := os.RemoveAll(profileDir); err == nil {
			fmt.Printf("Removed saved profiles at %s\n", profileDir)
		}
	} else {
		fmt.Printf("Saved profiles kept at %s (run '%s unregister --purge' to delete them too).\n", profileDir, bin)
	}
	return nil
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// removeInstallDir deletes the install folder, but only the dedicated per-tool
// directory we create on Windows (%LOCALAPPDATA%\claude-acc). On Unix the
// install dir is a shared location (~/.local/bin) that must never be removed.
// os.Remove only deletes an empty directory, so a folder that still holds other
// files is left intact.
func removeInstallDir(dir string) {
	if runtime.GOOS != "windows" || filepath.Base(dir) != bin {
		return
	}
	_ = os.Remove(dir)
}

// ---- Unix PATH (a marked export block appended to the shell rc) ----

func shellRC() string {
	switch filepath.Base(os.Getenv("SHELL")) {
	case "zsh":
		return filepath.Join(home, ".zshrc")
	case "bash":
		return filepath.Join(home, ".bashrc")
	default:
		return filepath.Join(home, ".profile")
	}
}

func onPath(dir string) bool {
	for _, p := range filepath.SplitList(os.Getenv("PATH")) {
		if pathEqual(p, dir) {
			return true
		}
	}
	return false
}

func addUnixPath(dir string) (bool, string, error) {
	rc := shellRC()
	if onPath(dir) {
		return false, rc, nil
	}
	// Don't double-add if a previous register already wrote the block.
	if existing, ok := readFileOpt(rc); ok && strings.Contains(existing, "# "+bin) {
		return false, rc, nil
	}
	block := fmt.Sprintf("\n# %s\nexport PATH=\"%s:$PATH\"\n", bin, dir)
	f, err := os.OpenFile(rc, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return false, rc, err
	}
	defer f.Close()
	if _, err := f.WriteString(block); err != nil {
		return false, rc, err
	}
	return true, rc, nil
}

func removeUnixPath(dir string) error {
	rc := shellRC()
	content, ok := readFileOpt(rc)
	if !ok {
		return nil
	}
	lines := strings.Split(content, "\n")
	var kept []string
	for _, line := range lines {
		t := strings.TrimSpace(line)
		if t == "# "+bin {
			continue
		}
		if strings.HasPrefix(t, "export PATH=") && strings.Contains(line, dir) {
			continue
		}
		kept = append(kept, line)
	}
	return writeFileAtomic(rc, []byte(strings.Join(kept, "\n")), 0o644)
}
