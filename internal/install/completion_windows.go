//go:build windows

package install

// Windows tab-completion install: a marked block in the PowerShell profile that
// sources `claude-acc completion powershell` at startup. The profile path is
// resolved by asking PowerShell itself ($PROFILE.CurrentUserAllHosts), which
// correctly accounts for a relocated (e.g. OneDrive) Documents folder and for
// both Windows PowerShell 5.1 and PowerShell 7 being installed.

import (
	"fmt"
	"os/exec"
	"strings"

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

// loaderLine sources the live completion script every time PowerShell starts.
func loaderLine() string {
	return fmt.Sprintf("%s completion powershell | Out-String | Invoke-Expression", paths.Bin)
}

// installCompletion enables PowerShell tab completion. Best-effort: it returns a
// status message, and a manual-instructions message (no error) when no profile
// could be resolved.
func installCompletion() (string, error) {
	profiles := powershellProfilePaths()
	if len(profiles) == 0 {
		return fmt.Sprintf("Tab completion: add '%s' to your PowerShell $PROFILE.\n", loaderLine()), nil
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

// removeCompletion reverses installCompletion across every resolved profile.
func removeCompletion() {
	for _, p := range powershellProfilePaths() {
		removeMarkedBlock(p)
	}
}
