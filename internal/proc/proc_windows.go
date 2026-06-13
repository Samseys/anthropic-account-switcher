//go:build windows

package proc

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// running uses Toolhelp32 image names; node-hosted sessions are missed
// (no command-line access), acceptable for a warn-only check.
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
