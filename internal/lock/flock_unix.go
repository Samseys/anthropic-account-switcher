//go:build !windows

package lock

import (
	"os"
	"syscall"
)

// flockTry attempts a non-blocking exclusive flock on f.
func flockTry(f *os.File) error {
	err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err == syscall.EWOULDBLOCK {
		return errContended
	}
	return err
}

func flockRelease(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}
