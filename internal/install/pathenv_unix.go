//go:build !windows

package install

// Unix PATH handling: maintains a single marked export block in the shell rc.
// The rc — not the live $PATH — is the source of truth, because it is what
// future shells inherit; a dir can be exported in this process yet absent from
// the rc (and vice versa). Register reconciles the block to the current install
// dir, so it is idempotent on re-run and self-corrects if the dir ever moves.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Samseys/anthropic-account-switcher/internal/paths"
)

// pathMarker tags our PATH export block. It is matched as a whole line, so it
// never aliases the completion marker ("# acc-claude completion"), which is a
// different line that merely shares this prefix.
var pathMarker = "# " + paths.Bin

func shellRC() string {
	switch filepath.Base(os.Getenv("SHELL")) {
	case "zsh":
		return filepath.Join(paths.Home, ".zshrc")
	case "bash":
		return filepath.Join(paths.Home, ".bashrc")
	default:
		return filepath.Join(paths.Home, ".profile")
	}
}

// pathExportFor is the export line our block writes for dir.
func pathExportFor(dir string) string {
	return fmt.Sprintf("export PATH=\"%s:$PATH\"", dir)
}

// ourPathExport returns the export line introduced by our marker in content and
// whether it was found. Only a marker immediately followed by our export line
// counts, so a stray marker is treated as absent (and cleaned on the next add).
func ourPathExport(content string) (string, bool) {
	lines := strings.Split(content, "\n")
	for i, ln := range lines {
		if strings.TrimSpace(ln) == pathMarker && i+1 < len(lines) && isOurPathExport(lines[i+1]) {
			return strings.TrimSpace(lines[i+1]), true
		}
	}
	return "", false
}

func addUserPath(dir string) (string, error) {
	rc := shellRC()
	want := pathExportFor(dir)
	existing, _ := paths.ReadFileOpt(rc)

	if cur, found := ourPathExport(existing); found {
		if cur == want {
			return fmt.Sprintf("'%s' is already on your PATH; '%s' is ready to use.\n", dir, paths.Bin), nil
		}
		// Our block points at a different dir (the install location moved): drop
		// the stale block so we converge to one block at the current dir instead
		// of leaving the old entry behind.
		if err := removeUserPath(dir); err != nil {
			return "", err
		}
	}

	block := fmt.Sprintf("\n%s\n%s\n", pathMarker, want)
	f, err := os.OpenFile(rc, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := f.WriteString(block); err != nil {
		return "", err
	}
	return fmt.Sprintf("Added '%s' to your PATH in %s.\nRun 'source \"%s\"' or open a new terminal, then use '%s'.\n", dir, rc, rc, paths.Bin), nil
}

// removeUserPath strips our marked block from the shell rc. It keys on the
// marker line alone (not dir), so a block left by an older install at a
// different location is also removed. The dir argument is unused on Unix; it is
// kept for parity with the Windows signature.
func removeUserPath(dir string) error {
	_ = dir
	rc := shellRC()
	content, ok := paths.ReadFileOpt(rc)
	if !ok {
		return nil
	}
	lines := strings.Split(content, "\n")
	var kept []string
	for i := 0; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) != pathMarker {
			kept = append(kept, lines[i])
			continue
		}
		// Drop the export line we wrote after the marker (ours by construction,
		// whatever dir it targets), and the blank separator before it.
		if i+1 < len(lines) && isOurPathExport(lines[i+1]) {
			i++
		}
		if n := len(kept); n > 0 && strings.TrimSpace(kept[n-1]) == "" {
			kept = kept[:n-1]
		}
	}
	return paths.WriteFileAtomic(rc, []byte(strings.Join(kept, "\n")), 0o644)
}

// isOurPathExport reports whether line is an `export PATH=` entry. Used only to
// confirm the line following our marker is the export we wrote (it always is),
// so a malformed block never eats an unrelated following line.
func isOurPathExport(line string) bool {
	return strings.HasPrefix(strings.TrimSpace(line), "export PATH=")
}
