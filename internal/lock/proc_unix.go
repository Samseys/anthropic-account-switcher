//go:build !windows

package lock

import "syscall"

// processAlive reports whether a process with the given PID is currently
// running. Signal 0 performs error checking without delivering a signal: a nil
// or EPERM result means the process exists (EPERM = it exists but we don't own
// it); ESRCH means it's gone.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}
