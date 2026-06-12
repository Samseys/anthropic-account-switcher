//go:build !windows

package install

// Unix tab-completion install across every relevant shell:
//   - bash and zsh load it from a marked block in their rc file (sourced after
//     the PATH block, so `claude-acc` is already reachable);
//   - fish gets a dedicated file in its completions directory, which it autoloads.
//
// install configures the current shell plus any other shell whose config already
// exists, so a user of more than one shell is covered without us fabricating an
// rc for a shell they have never used. uninstall clears ALL shells regardless of
// the current one, so a tool registered under one shell unregisters cleanly from
// another.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Samseys/anthropic-account-switcher/internal/paths"
)

// currentShell returns the base name of the user's login shell (e.g. "bash").
func currentShell() string { return filepath.Base(os.Getenv("SHELL")) }

// fishCompletionPath is the autoloaded per-user completion file for fish.
func fishCompletionPath() string {
	return filepath.Join(paths.Home, ".config", "fish", "completions", paths.Bin+".fish")
}

// shellTarget is one shell's completion configuration. Exactly one of rc (bash/
// zsh, with loader) or file (fish) is set.
type shellTarget struct {
	name   string
	rc     string // bash/zsh rc file
	loader string // bash/zsh: the line that sources our completion
	file   string // fish: the dedicated completion file
}

// unixTargets is every shell completion location on this OS.
func unixTargets() []shellTarget {
	return []shellTarget{
		{name: "bash", rc: filepath.Join(paths.Home, ".bashrc"),
			loader: fmt.Sprintf("source <(%s completion bash)", paths.Bin)},
		{name: "zsh", rc: filepath.Join(paths.Home, ".zshrc"),
			loader: fmt.Sprintf("source <(%s completion zsh)", paths.Bin)},
		{name: "fish", file: fishCompletionPath()},
	}
}

// applicable reports whether this shell should be configured: it is the user's
// current shell, or it is already set up (its config file/dir exists).
func (tg shellTarget) applicable(current string) bool {
	if tg.name == current {
		return true
	}
	if tg.file != "" { // fish
		return paths.FileExists(filepath.Join(paths.Home, ".config", "fish"))
	}
	return paths.FileExists(tg.rc)
}

// install enables completion for this shell, reporting whether it wrote anything
// (false means it was already enabled).
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

// remove disables completion for this shell, best-effort.
func (tg shellTarget) remove() {
	if tg.file != "" {
		_ = os.Remove(tg.file)
		return
	}
	removeMarkedBlock(tg.rc)
}

// installCompletion enables tab completion for every applicable shell. It is
// best-effort and returns a human-readable status message.
func installCompletion() (string, error) {
	current := currentShell()
	var configured, already []string
	for _, tg := range unixTargets() {
		if !tg.applicable(current) {
			continue
		}
		switch wrote, err := tg.install(); {
		case err != nil:
			// best-effort: a shell config we can't write to is skipped
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

// removeCompletion reverses installCompletion for ALL shells, so completion is
// cleaned up regardless of which shell is current now.
func removeCompletion() {
	for _, tg := range unixTargets() {
		tg.remove()
	}
}
