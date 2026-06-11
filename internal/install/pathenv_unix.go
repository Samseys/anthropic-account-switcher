//go:build !windows

package install

// Unix PATH handling: a marked export block appended to the shell rc. This is
// the cross-platform counterpart to pathenv_windows.go's registry editing.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Samseys/anthropic-account-switcher/internal/paths"
)

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

func onPath(dir string) bool {
	for _, p := range filepath.SplitList(os.Getenv("PATH")) {
		if paths.PathEqual(p, dir) {
			return true
		}
	}
	return false
}

func addUserPath(dir string) (string, error) {
	rc := shellRC()
	alreadyMsg := fmt.Sprintf("'%s' is already on your PATH; '%s' is ready to use.\n", dir, paths.Bin)
	if onPath(dir) {
		return alreadyMsg, nil
	}
	// Don't double-add if a previous register already wrote the block.
	if existing, ok := paths.ReadFileOpt(rc); ok && strings.Contains(existing, "# "+paths.Bin) {
		return alreadyMsg, nil
	}
	block := fmt.Sprintf("\n# %s\nexport PATH=\"%s:$PATH\"\n", paths.Bin, dir)
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

func removeUserPath(dir string) error {
	rc := shellRC()
	content, ok := paths.ReadFileOpt(rc)
	if !ok {
		return nil
	}
	lines := strings.Split(content, "\n")
	var kept []string
	for i := 0; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) != "# "+paths.Bin {
			kept = append(kept, lines[i])
			continue
		}
		// Found our marker. Drop it together with the export line we wrote
		// directly after it — and only that line, never an unrelated
		// `export PATH=` the user maintains themselves. Also remove the blank
		// line addUserPath prepends, so register/unregister cycles don't
		// accumulate empty lines.
		if i+1 < len(lines) && isOurPathExport(lines[i+1], dir) {
			i++
		}
		if n := len(kept); n > 0 && strings.TrimSpace(kept[n-1]) == "" {
			kept = kept[:n-1]
		}
	}
	return paths.WriteFileAtomic(rc, []byte(strings.Join(kept, "\n")), 0o644)
}

// isOurPathExport reports whether line is the `export PATH="<dir>:$PATH"` entry
// addUserPath writes for dir.
func isOurPathExport(line, dir string) bool {
	t := strings.TrimSpace(line)
	return strings.HasPrefix(t, "export PATH=") && strings.Contains(line, dir)
}
