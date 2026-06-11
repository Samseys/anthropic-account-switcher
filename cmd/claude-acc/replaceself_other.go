//go:build !windows

package main

import "os"

// On Unix a running binary can be replaced by an atomic rename over its own
// path: the old inode stays alive for the current process, while new execs
// pick up the new file.
func replaceRunningBinary(dst string, data []byte, perm os.FileMode) error {
	return writeFileAtomic(dst, data, perm)
}
