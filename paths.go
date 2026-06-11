package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

const bin = "claude-acc"

// version is overridden at build time via -ldflags "-X main.version=...".
var version = "dev"

var (
	home       = mustHome()
	claudeDir  = filepath.Join(home, ".claude")
	credFile   = filepath.Join(claudeDir, ".credentials.json")
	configFile = filepath.Join(home, ".claude.json")
	profileDir = filepath.Join(claudeDir, "account-profiles")
)

func mustHome() string {
	h, err := os.UserHomeDir()
	if err != nil {
		die("cannot determine home directory: %v", err)
	}
	return h
}

func die(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "ERROR: "+format+"\n", a...)
	os.Exit(1)
}

var sanitizeRe = regexp.MustCompile(`[^a-zA-Z0-9._@-]`)

func sanitize(s string) string { return sanitizeRe.ReplaceAllString(s, "_") }

// readFileOpt reads a file, returning ("", false) if it is missing/unreadable.
func readFileOpt(path string) (string, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	return string(b), true
}

// readTrim reads a file and trims surrounding whitespace, or returns "".
func readTrim(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// writeFileAtomic writes data via a temp file in the same directory and renames
// it into place. Go writes no BOM, which Claude Code's JSON parser requires.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".claude-acc-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once the rename succeeds
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	// Best effort: chmod is meaningful on Unix, a near no-op on Windows.
	_ = os.Chmod(tmpName, perm)
	return os.Rename(tmpName, path)
}

func copyFileAtomic(src, dst string, perm os.FileMode) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return writeFileAtomic(dst, data, perm)
}

// pathEqual compares two filesystem paths the way the host OS treats them:
// case-insensitively on Windows, and ignoring a trailing separator everywhere.
func pathEqual(a, b string) bool {
	a = strings.TrimRight(a, `\/`)
	b = strings.TrimRight(b, `\/`)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}
