// Package paths holds the shared, process-wide configuration every other
// package depends on: the canonical locations of Claude Code's state, the tool
// name and version, and the small filesystem helpers (atomic writes, optional
// reads, OS-aware path comparison) used throughout. It is the leaf of the
// dependency graph — it imports nothing from this module.
package paths

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"runtime/debug"
	"strings"
)

// Bin is the tool's binary/command name.
const Bin = "claude-acc"

// Version is overridden at build time via
// -ldflags "-X .../internal/paths.Version=...".
var Version string

// VersionString prefers the ldflags-injected version, then the module version
// recorded by `go install module@vX.Y.Z`, then "dev" for plain local builds.
func VersionString() string {
	if Version != "" {
		return Version
	}
	if bi, ok := debug.ReadBuildInfo(); ok {
		if v := bi.Main.Version; v != "" && v != "(devel)" {
			return strings.TrimPrefix(v, "v")
		}
	}
	return "dev"
}

// Claude Code honors CLAUDE_CONFIG_DIR to relocate ~/.claude; when it is set,
// ~/.claude.json moves inside that directory too.
//
// These are vars rather than consts so tests can point them at a scratch
// directory.
var (
	Home       = mustHome()
	ClaudeDir  = defaultClaudeDir()
	CredFile   = filepath.Join(ClaudeDir, ".credentials.json")
	ConfigFile = defaultConfigFile()
	ProfileDir = filepath.Join(ClaudeDir, "account-profiles")
)

func defaultClaudeDir() string {
	if v := os.Getenv("CLAUDE_CONFIG_DIR"); v != "" {
		return v
	}
	return filepath.Join(Home, ".claude")
}

func defaultConfigFile() string {
	if v := os.Getenv("CLAUDE_CONFIG_DIR"); v != "" {
		return filepath.Join(v, ".claude.json")
	}
	return filepath.Join(Home, ".claude.json")
}

func mustHome() string {
	h, err := os.UserHomeDir()
	if err != nil {
		Die("cannot determine home directory: %v", err)
	}
	return h
}

// Die prints an error to stderr and exits with status 1.
func Die(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "ERROR: "+format+"\n", a...)
	os.Exit(1)
}

var sanitizeRe = regexp.MustCompile(`[^a-zA-Z0-9._@-]`)

// Sanitize maps an arbitrary profile name to a filesystem-safe form. It is
// idempotent.
func Sanitize(s string) string { return sanitizeRe.ReplaceAllString(s, "_") }

// ReadFileOpt reads a file, returning ("", false) if it is missing/unreadable.
func ReadFileOpt(path string) (string, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	return string(b), true
}

// ReadTrim reads a file and trims surrounding whitespace, or returns "".
func ReadTrim(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// WriteFileAtomic writes data via a temp file in the same directory and renames
// it into place. Go writes no BOM, which Claude Code's JSON parser requires.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
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

// CopyFileAtomic copies src to dst, writing dst atomically.
func CopyFileAtomic(src, dst string, perm os.FileMode) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return WriteFileAtomic(dst, data, perm)
}

// SelfPath returns the absolute path of the running binary with symlinks
// resolved. If symlink resolution fails the raw os.Executable path is kept.
func SelfPath() (string, error) {
	self, err := os.Executable()
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(self); err == nil {
		self = resolved
	}
	return self, nil
}

// FileExists reports whether path exists and is statable.
func FileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// PrintJSON writes v to stdout as indented JSON.
func PrintJSON(v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(b))
	return nil
}

// PathEqual compares two filesystem paths the way the host OS treats them:
// case-insensitively on Windows, and ignoring a trailing separator everywhere.
func PathEqual(a, b string) bool {
	a = strings.TrimRight(a, `\/`)
	b = strings.TrimRight(b, `\/`)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}
