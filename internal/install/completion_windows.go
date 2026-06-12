//go:build windows

package install

// Windows tab-completion install: the completion script is written to a static
// file and a marked block in the PowerShell profile dot-sources it at startup.
// The profile path is resolved by asking PowerShell itself
// ($PROFILE.CurrentUserAllHosts), which correctly accounts for a relocated (e.g.
// OneDrive) Documents folder and for both Windows PowerShell 5.1 and PowerShell
// 7 being installed.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Samseys/anthropic-account-switcher/internal/cli"
	"github.com/Samseys/anthropic-account-switcher/internal/paths"
)

// powershellProfilePaths returns the CurrentUserAllHosts profile path for each
// installed PowerShell host. It is a var so tests can stub the resolution.
var powershellProfilePaths = func() []string {
	var out []string
	seen := map[string]bool{}
	for _, exe := range []string{"powershell", "pwsh"} {
		b, err := exec.Command(exe, "-NoProfile", "-Command", "$PROFILE.CurrentUserAllHosts").Output()
		if err != nil {
			continue
		}
		p := strings.TrimSpace(string(b))
		if p == "" || seen[strings.ToLower(p)] {
			continue
		}
		seen[strings.ToLower(p)] = true
		out = append(out, p)
	}
	return out
}

// completionScriptPath is the file the completion script is written to and
// dot-sourced from. It is a var so tests can redirect it off the real install
// dir.
var completionScriptPath = func() string {
	return filepath.Join(installDir(), paths.Bin+".completion.ps1")
}

// loaderLine dot-sources the completion script file at PowerShell startup. We
// write a static script and source it (guarded by Test-Path) rather than piping
// `acc-claude completion powershell` into Invoke-Expression: executing live
// command output at every shell start is a classic AMSI/antivirus red flag, and
// the script defers to the binary at completion time so the file never goes
// stale.
func loaderLine() string {
	p := completionScriptPath()
	return fmt.Sprintf("if (Test-Path '%s') { . '%s' }", p, p)
}

// writeCompletionScript materializes the PowerShell completion script to disk.
func writeCompletionScript() error {
	script, err := cli.CompletionScript(paths.Bin, "powershell")
	if err != nil {
		return err
	}
	p := completionScriptPath()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return paths.WriteFileAtomic(p, []byte(script), 0o644)
}

// installCompletion enables PowerShell tab completion. Best-effort: it returns a
// status message, and a manual-instructions message (no error) when no profile
// could be resolved.
func installCompletion() (string, error) {
	profiles := powershellProfilePaths()
	if len(profiles) == 0 {
		return fmt.Sprintf("Tab completion: add '%s' to your PowerShell $PROFILE.\n", loaderLine()), nil
	}
	if err := writeCompletionScript(); err != nil {
		return "", fmt.Errorf("writing completion script: %w", err)
	}
	var configured, already []string
	for _, p := range profiles {
		switch wrote, err := appendMarkedBlock(p, loaderLine()); {
		case err != nil:
			// best-effort: a profile we can't write to is skipped
		case wrote:
			configured = append(configured, p)
		default:
			already = append(already, p)
		}
	}
	switch {
	case len(configured) > 0:
		return fmt.Sprintf("Enabled PowerShell tab completion in:\n  %s\nOpen a new PowerShell window to use it.\n",
			strings.Join(configured, "\n  ")), nil
	case len(already) > 0:
		return fmt.Sprintf("PowerShell tab completion already enabled in:\n  %s\n",
			strings.Join(already, "\n  ")), nil
	default:
		return "", fmt.Errorf("could not write to your PowerShell profile(s)")
	}
}

// removeCompletion reverses installCompletion across every resolved profile and
// deletes the dot-sourced script file.
func removeCompletion() {
	for _, p := range powershellProfilePaths() {
		removeMarkedBlock(p)
	}
	_ = os.Remove(completionScriptPath())
}
