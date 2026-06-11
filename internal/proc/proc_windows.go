//go:build windows

package proc

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// running enumerates the process table natively (no subprocess, no CLR) and
// looks for a Claude Code process by image name. Modern Claude Code ships as a
// native claude.exe, so the image name is enough; a legacy node-hosted session
// (visible only in the command line) is missed, which is acceptable for a
// best-effort, warn-only check.
func running() bool {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return false
	}
	defer windows.CloseHandle(snap)

	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	for err = windows.Process32First(snap, &entry); err == nil; err = windows.Process32Next(snap, &entry) {
		if mentionsClaude(windows.UTF16ToString(entry.ExeFile[:])) {
			return true
		}
	}
	return false
}
