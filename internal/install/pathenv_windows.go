//go:build windows

package install

import (
	"fmt"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"github.com/Samseys/anthropic-account-switcher/internal/paths"
)

// Windows PATH handling: the user PATH is edited directly in HKCU\Environment
// rather than via [Environment]::SetEnvironmentVariable. The .NET getter expands
// REG_EXPAND_SZ values, so a read-modify-write through it would flatten entries
// like %USERPROFILE%\bin (the classic setx PATH-mangling bug). Reading the raw
// value and writing it back with its original registry type preserves them.
// This is the Windows counterpart to pathenv_unix.go.

// envKeyPath is the HKCU subkey that holds the user PATH. It is a var, not a
// const, only so tests can point the round-trip at a throwaway key instead of
// mutating the real user environment (see pathenv_windows_test.go).
var envKeyPath = `Environment`

// getUserPath returns the raw (unexpanded) user PATH value and its registry
// type. A missing value is not an error: it returns "" and EXPAND_SZ, the type
// Windows itself uses for PATH.
func getUserPath() (string, uint32, error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, envKeyPath, registry.QUERY_VALUE)
	if err != nil {
		return "", 0, fmt.Errorf("opening HKCU\\%s: %w", envKeyPath, err)
	}
	defer k.Close()
	val, typ, err := k.GetStringValue("Path")
	if err == registry.ErrNotExist {
		return "", registry.EXPAND_SZ, nil
	}
	if err != nil {
		return "", 0, fmt.Errorf("reading the user PATH: %w", err)
	}
	return val, typ, nil
}

func setUserPath(value string, typ uint32) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, envKeyPath, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("opening HKCU\\%s for writing: %w", envKeyPath, err)
	}
	defer k.Close()
	if typ == registry.SZ {
		err = k.SetStringValue("Path", value)
	} else {
		err = k.SetExpandStringValue("Path", value)
	}
	if err != nil {
		return fmt.Errorf("writing the user PATH: %w", err)
	}
	broadcastEnvChange()
	return nil
}

// broadcastEnvChange tells running shells/Explorer that the environment
// changed (the WM_SETTINGCHANGE broadcast SetEnvironmentVariable would send).
func broadcastEnvChange() {
	const (
		hwndBroadcast   = 0xffff
		wmSettingChange = 0x001a
		smtoAbortIfHung = 0x0002
	)
	param, err := windows.UTF16PtrFromString("Environment")
	if err != nil {
		return
	}
	proc := windows.NewLazySystemDLL("user32.dll").NewProc("SendMessageTimeoutW")
	_, _, _ = proc.Call(hwndBroadcast, wmSettingChange, 0,
		uintptr(unsafe.Pointer(param)), smtoAbortIfHung, 5000, 0)
}

// samePathEntry reports whether a raw ";"-separated PATH entry refers to dir,
// ignoring surrounding whitespace and empty fields.
func samePathEntry(entry, dir string) bool {
	return entry != "" && paths.PathEqual(strings.TrimSpace(entry), dir)
}

func addUserPath(dir string) (string, error) {
	cur, typ, err := getUserPath()
	if err != nil {
		return "", err
	}
	for p := range strings.SplitSeq(cur, ";") {
		if samePathEntry(p, dir) {
			return fmt.Sprintf("'%s' is already on your user PATH.\n", dir), nil
		}
	}
	newPath := cur
	if newPath != "" && !strings.HasSuffix(newPath, ";") {
		newPath += ";"
	}
	newPath += dir
	if err := setUserPath(newPath, typ); err != nil {
		return "", err
	}
	return fmt.Sprintf("Added '%s' to your user PATH. Open a NEW terminal for it to apply.\n", dir), nil
}

func removeUserPath(dir string) error {
	cur, typ, err := getUserPath()
	if err != nil || cur == "" {
		return err
	}
	var kept []string
	removed := false
	for p := range strings.SplitSeq(cur, ";") {
		if samePathEntry(p, dir) {
			removed = true
			continue
		}
		kept = append(kept, p)
	}
	if !removed {
		return nil
	}
	return setUserPath(strings.Join(kept, ";"), typ)
}
