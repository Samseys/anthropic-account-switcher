package install

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/Samseys/anthropic-account-switcher/internal/paths"
)

// completionMarker is distinct from the PATH block marker ("# acc-claude") so
// they don't interfere when both land in the same startup file.
const completionMarker = "# " + paths.Bin + " completion"

// appendMarkedBlock appends the completion marker and loader line to file
// (creating parents as needed), idempotent. Returns false if already present.
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

// removeMarkedBlock strips our marked block from file. Missing file is a no-op.
// Splits on "\n" not "\r\n", so any "\r" on kept lines is preserved.
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
				i++ // skip the loader line
			}
			if n := len(kept); n > 0 && strings.TrimSpace(kept[n-1]) == "" {
				kept = kept[:n-1] // remove preceding blank line
			}
			continue
		}
		kept = append(kept, lines[i])
	}
	_ = paths.WriteFileAtomic(file, []byte(strings.Join(kept, "\n")), 0o644)
}
