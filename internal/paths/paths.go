// Package paths is the leaf of the dependency graph: it owns canonical Claude
// Code file locations, the tool name/version, and shared filesystem helpers.
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
	"time"
)

const Bin = "acc-claude"

// Version is overridden at build time via -ldflags "-X .../internal/paths.Version=...".
var Version string

// VersionString returns the ldflags version, the module version from go install, or "dev".
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

// Vars (not consts) so tests can redirect them to a scratch directory.
// Honors CLAUDE_CONFIG_DIR exactly as Claude Code does.
var (
	Home         = mustHome()
	ClaudeDir    = defaultClaudeDir()
	CredFile     = filepath.Join(ClaudeDir, ".credentials.json")
	ConfigFile   = defaultConfigFile()
	ProfileDir   = filepath.Join(ClaudeDir, "account-profiles")
	SettingsFile = filepath.Join(ClaudeDir, "settings.json")
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

func Die(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "ERROR: "+format+"\n", a...)
	os.Exit(1)
}

var sanitizeRe = regexp.MustCompile(`[^a-zA-Z0-9._@-]`)

// Sanitize maps an arbitrary profile name to a filesystem-safe form (idempotent).
func Sanitize(s string) string { return sanitizeRe.ReplaceAllString(s, "_") }

// ReadFileOpt returns ("", false) if the file is missing or unreadable.
func ReadFileOpt(path string) (string, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	return string(b), true
}

// ReadTrim returns the file's contents with surrounding whitespace trimmed, or "".
func ReadTrim(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// WriteFileAtomic writes via a temp-then-rename. No BOM is written, which
// Claude Code's JSON parser requires.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".acc-claude-*.tmp")
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
	_ = os.Chmod(tmpName, perm) // near no-op on Windows
	return renameWithRetry(tmpName, path)
}

// renameWithRetry retries the rename under capped exponential backoff.
// On Windows, AV scanners briefly hold a handle on freshly written files,
// causing ERROR_ACCESS_DENIED; a few seconds is enough for JSON/text files.
// On Unix the first attempt always succeeds.
func renameWithRetry(oldpath, newpath string) error {
	const deadlineAfter = 5 * time.Second
	deadline := time.Now().Add(deadlineAfter)
	backoff := 50 * time.Millisecond
	for {
		err := os.Rename(oldpath, newpath)
		if err == nil || time.Now().After(deadline) {
			return err
		}
		time.Sleep(backoff)
		if backoff < time.Second {
			backoff *= 2
		}
	}
}

// SelfPath returns the absolute path of the running binary with symlinks resolved.
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

func FileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func PrintJSON(v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(b))
	return nil
}

// PathEqual compares paths case-insensitively on Windows, ignoring trailing separators.
func PathEqual(a, b string) bool {
	a = strings.TrimRight(a, `\/`)
	b = strings.TrimRight(b, `\/`)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}
