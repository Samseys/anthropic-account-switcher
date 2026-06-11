//go:build windows

package install

import (
	"os"

	"github.com/Samseys/anthropic-account-switcher/internal/paths"
)

// ReplaceRunningBinary overwrites the binary at dst with data. Windows locks a
// running executable's image, so it cannot be overwritten in place. It can,
// however, be renamed: we move the current binary aside, write the new one to
// the original path, then hand the stale copy to the same detached self-delete
// helper used by Unregister.
func ReplaceRunningBinary(dst string, data []byte, perm os.FileMode) error {
	old := dst + ".old"
	_ = os.Remove(old) // clear any leftover from a previous update
	if err := os.Rename(dst, old); err != nil {
		return err
	}
	if err := paths.WriteFileAtomic(dst, data, perm); err != nil {
		// Roll back so the user is not left without a working binary.
		_ = os.Rename(old, dst)
		return err
	}
	_ = scheduleSelfDelete(old)
	return nil
}
