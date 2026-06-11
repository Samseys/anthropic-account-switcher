//go:build !windows

package install

import (
	"os"

	"github.com/Samseys/anthropic-account-switcher/internal/paths"
)

// ReplaceRunningBinary overwrites the binary at dst with data. On Unix a running
// binary can be replaced by an atomic rename over its own path: the old inode
// stays alive for the current process, while new execs pick up the new file.
func ReplaceRunningBinary(dst string, data []byte, perm os.FileMode) error {
	return paths.WriteFileAtomic(dst, data, perm)
}
