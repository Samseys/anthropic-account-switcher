//go:build windows

package install

// Windows tab-completion: writes a static script and dot-sources it from a
// marked block in the PowerShell profile ($PROFILE.CurrentUserAllHosts),
// which handles relocated Documents folders and both PS 5.1 and PS 7.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Samseys/anthropic-account-switcher/internal/cli"
	"github.com/Samseys/anthropic-account-switcher/internal/paths"
)

// powershellProfilePaths is a var so tests can stub it.
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

// completionScriptPath is a var so tests can redirect it.
var completionScriptPath = func() string {
	return filepath.Join(installDir(), paths.Bin+".completion.ps1")
}

// loaderLine dot-sources the completion script at startup. A static file is used
// rather than piping into Invoke-Expression — live IEX is an AMSI/antivirus flag.
func loaderLine() string {
	p := completionScriptPath()
	return fmt.Sprintf("if (Test-Path '%s') { . '%s' }", p, p)
}

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

// installCompletion enables PowerShell tab completion, best-effort.
// Returns manual instructions (no error) when no profile could be resolved.
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
		case err != nil: // best-effort: skip profiles we can't write to
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

func removeCompletion() {
	for _, p := range powershellProfilePaths() {
		removeMarkedBlock(p)
	}
	_ = os.Remove(completionScriptPath())
}
