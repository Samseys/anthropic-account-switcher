package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// register makes `claude-acc` callable from any shell by copying this binary
// into a canonical per-user bin directory and ensuring that directory is on the
// user's PATH. unregister reverses both steps.
//
//	Windows : %LOCALAPPDATA%\claude-acc\claude-acc.exe + user PATH (via powershell)
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

func cmdUnregister(flag string) error {
	purge := flag == "--purge"
	dir := installDir()

	if dst := installedBinaryPath(); fileExists(dst) {
		// On Windows we may be running the very binary we're trying to delete;
		// that's fine for a copy elsewhere, but skip deleting our own image.
		self, _ := os.Executable()
		if resolved, err := filepath.EvalSymlinks(self); err == nil {
			self = resolved
		}
		if !pathEqual(self, dst) {
			_ = os.Remove(dst)
		}
	}
	if runtime.GOOS == "windows" {
		_ = removeWindowsUserPath(dir)
	} else {
		_ = removeUnixPath(dir)
	}

	fmt.Printf("Unregistered '%s' (removed the installed binary and PATH entry).\n", bin)
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

// ---- Windows user PATH (persisted via the registry, broadcast on change) ----
//
// We let PowerShell perform the [Environment]::SetEnvironmentVariable call: it
// writes HKCU\Environment and broadcasts WM_SETTINGCHANGE so new processes see
// the update. The value is passed through an env var to avoid quoting issues.

func psUserPath() string {
	out, err := exec.Command("powershell", "-NoProfile", "-Command",
		"[Environment]::GetEnvironmentVariable('Path','User')").Output()
	if err != nil {
		return ""
	}
	return strings.TrimRight(string(out), "\r\n")
}

func setPSUserPath(value string) error {
	cmd := exec.Command("powershell", "-NoProfile", "-Command",
		"[Environment]::SetEnvironmentVariable('Path',$env:CLAUDE_ACC_NEWPATH,'User')")
	cmd.Env = append(os.Environ(), "CLAUDE_ACC_NEWPATH="+value)
	return cmd.Run()
}

func addWindowsUserPath(dir string) (bool, error) {
	cur := psUserPath()
	for _, p := range strings.Split(cur, ";") {
		if p != "" && pathEqual(strings.TrimSpace(p), dir) {
			return false, nil
		}
	}
	newPath := cur
	if newPath != "" && !strings.HasSuffix(newPath, ";") {
		newPath += ";"
	}
	newPath += dir
	return true, setPSUserPath(newPath)
}

func removeWindowsUserPath(dir string) error {
	cur := psUserPath()
	if cur == "" {
		return nil
	}
	var kept []string
	for _, p := range strings.Split(cur, ";") {
		if p == "" || pathEqual(strings.TrimSpace(p), dir) {
			continue
		}
		kept = append(kept, p)
	}
	return setPSUserPath(strings.Join(kept, ";"))
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
