package install

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/Samseys/anthropic-account-switcher/internal/paths"
)

// Tab-completion install/uninstall, wired into register/unregister. On Unix the
// installed line sources the running binary's output at shell startup (`source
// <(acc-claude completion <shell>)`); on Windows a static script file is written
// and dot-sourced instead (see completion_windows.go). Either way the script
// defers to the binary at completion time, so it never goes stale on update.
//
// The shell-specific work lives in completion_unix.go and completion_windows.go;
// the marker-block file editing shared by both lives here. completionMarker is a
// distinct comment from the PATH block's "# acc-claude" marker, so the two never
// interfere on shells where both land in the same startup file.
const completionMarker = "# " + paths.Bin + " completion"

// appendMarkedBlock idempotently appends the completion marker and loader line
// to file (creating it and any parent directories). It reports whether it wrote
// anything: false means the marker was already present.
func appendMarkedBlock(file, line string) (bool, error) {
	if existing, ok := paths.ReadFileOpt(file); ok && strings.Contains(existing, completionMarker) {
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return false, err
	}
	f, err := os.OpenFile(file, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return false, err
	}
	defer f.Close()
	if _, err := f.WriteString("\n" + completionMarker + "\n" + line + "\n"); err != nil {
		return false, err
	}
	return true, nil
}

// removeMarkedBlock deletes the completion marker, the loader line written right
// after it, and the blank separator line before it. It is best-effort: a missing
// file is a no-op. TrimSpace makes the marker match whether the file uses LF or
// CRLF line endings, and splitting on "\n" preserves any "\r" on kept lines.
func removeMarkedBlock(file string) {
	content, ok := paths.ReadFileOpt(file)
	if !ok {
		return
	}
	lines := strings.Split(content, "\n")
	kept := make([]string, 0, len(lines))
	for i := 0; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == completionMarker {
			if i+1 < len(lines) {
				i++ // drop the loader line that follows the marker
			}
			if n := len(kept); n > 0 && strings.TrimSpace(kept[n-1]) == "" {
				kept = kept[:n-1] // drop the blank separator before the marker
			}
			continue
		}
		kept = append(kept, lines[i])
	}
	_ = paths.WriteFileAtomic(file, []byte(strings.Join(kept, "\n")), 0o644)
}
