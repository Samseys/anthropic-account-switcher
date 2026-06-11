//go:build windows

package main

import (
	"fmt"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// The user PATH is edited directly in HKCU\Environment rather than via
// [Environment]::SetEnvironmentVariable: the .NET getter expands REG_EXPAND_SZ
// values, so a read-modify-write through it would flatten entries like
// %USERPROFILE%\bin (the classic setx PATH-mangling bug). Reading the raw
// value and writing it back with its original registry type preserves them.

// getUserPath returns the raw (unexpanded) user PATH value and its registry
// type. A missing value is not an error: it returns "" and EXPAND_SZ, the type
// Windows itself uses for PATH.
func getUserPath() (string, uint32, error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, "Environment", registry.QUERY_VALUE)
	if err != nil {
		return "", 0, fmt.Errorf("opening HKCU\\Environment: %w", err)
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
	k, err := registry.OpenKey(registry.CURRENT_USER, "Environment", registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("opening HKCU\\Environment for writing: %w", err)
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

func addWindowsUserPath(dir string) (bool, error) {
	cur, typ, err := getUserPath()
	if err != nil {
		return false, err
	}
	for _, p := range strings.Split(cur, ";") {
		if p != "" && pathEqual(strings.TrimSpace(p), dir) {
			return false, nil
		}
	}
	newPath := cur
	if newPath != "" && !strings.HasSuffix(newPath, ";") {
		newPath += ";"
	}
	newPath += dir
	return true, setUserPath(newPath, typ)
}

func removeWindowsUserPath(dir string) error {
	cur, typ, err := getUserPath()
	if err != nil || cur == "" {
		return err
	}
	var kept []string
	removed := false
	for _, p := range strings.Split(cur, ";") {
		if p != "" && pathEqual(strings.TrimSpace(p), dir) {
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
