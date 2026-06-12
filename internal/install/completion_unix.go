//go:build !windows

package install

// Unix tab-completion: bash/zsh get a marked loader block in their rc; fish gets
// a dedicated autoloaded file. Configures the current shell plus any other whose
// config already exists. Uninstall always clears all shells regardless of current.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Samseys/anthropic-account-switcher/internal/paths"
)

func currentShell() string { return filepath.Base(os.Getenv("SHELL")) }

func fishCompletionPath() string {
	return filepath.Join(paths.Home, ".config", "fish", "completions", paths.Bin+".fish")
}

// shellTarget is one shell's completion configuration: rc+loader (bash/zsh) or file (fish).
type shellTarget struct {
	name   string
	rc     string // bash/zsh rc file
	loader string // bash/zsh: the line that sources our completion
	file   string // fish: the dedicated completion file
}

func unixTargets() []shellTarget {
	return []shellTarget{
		{name: "bash", rc: filepath.Join(paths.Home, ".bashrc"),
			loader: fmt.Sprintf("source <(%s completion bash)", paths.Bin)},
		{name: "zsh", rc: filepath.Join(paths.Home, ".zshrc"),
			loader: fmt.Sprintf("source <(%s completion zsh)", paths.Bin)},
		{name: "fish", file: fishCompletionPath()},
	}
}

// applicable reports whether to configure this shell: it's the current shell,
// or its config file/dir already exists.
func (tg shellTarget) applicable(current string) bool {
	if tg.name == current {
		return true
	}
	if tg.file != "" { // fish
		return paths.FileExists(filepath.Join(paths.Home, ".config", "fish"))
	}
	return paths.FileExists(tg.rc)
}

// install enables completion for this shell; returns false if already enabled.
func (tg shellTarget) install() (bool, error) {
	if tg.file != "" { // fish
		if paths.FileExists(tg.file) {
			return false, nil
		}
		if err := os.MkdirAll(filepath.Dir(tg.file), 0o755); err != nil {
			return false, err
		}
		body := fmt.Sprintf("%s completion fish | source\n", paths.Bin)
		if err := paths.WriteFileAtomic(tg.file, []byte(body), 0o644); err != nil {
			return false, err
		}
		return true, nil
	}
	return appendMarkedBlock(tg.rc, tg.loader)
}

func (tg shellTarget) remove() {
	if tg.file != "" {
		_ = os.Remove(tg.file)
		return
	}
	removeMarkedBlock(tg.rc)
}

// installCompletion enables tab completion for every applicable shell, best-effort.
func installCompletion() (string, error) {
	current := currentShell()
	var configured, already []string
	for _, tg := range unixTargets() {
		if !tg.applicable(current) {
			continue
		}
		switch wrote, err := tg.install(); {
		case err != nil: // best-effort: skip shells we can't write to
		case wrote:
			configured = append(configured, tg.name)
		default:
			already = append(already, tg.name)
		}
	}
	switch {
	case len(configured) > 0:
		return fmt.Sprintf("Enabled tab completion for: %s (open a new shell to use it).\n",
			strings.Join(configured, ", ")), nil
	case len(already) > 0:
		return fmt.Sprintf("Tab completion already enabled for: %s.\n", strings.Join(already, ", ")), nil
	default:
		return fmt.Sprintf("Tab completion: run '%s completion <bash|zsh|fish>' and source it from your shell startup file.\n", paths.Bin), nil
	}
}

func removeCompletion() {
	for _, tg := range unixTargets() {
		tg.remove()
	}
}
