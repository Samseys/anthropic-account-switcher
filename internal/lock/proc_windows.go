//go:build windows

package lock

import "golang.org/x/sys/windows"

// stillActive is the exit code Windows reports for a process that is still
// running (STILL_ACTIVE).
const stillActive = 259

// processAlive reports whether a process with the given PID is currently
// running. A handle we cannot open, or one that reports an exit code, means the
// process is gone.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return true // can't tell; err on the side of "alive" so we don't steal
	}
	return code == stillActive
}
