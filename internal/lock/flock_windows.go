//go:build windows

package lock

import (
	"os"

	"golang.org/x/sys/windows"
)

// flockTry acquires a non-blocking exclusive LockFileEx on the first byte.
// LockFileEx requires an explicit byte range; one byte suffices as a mutex.
func flockTry(f *os.File) error {
	err := windows.LockFileEx(windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0, 1, 0, new(windows.Overlapped))
	if err == windows.ERROR_LOCK_VIOLATION {
		return errContended
	}
	return err
}

func flockRelease(f *os.File) error {
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, new(windows.Overlapped))
}
