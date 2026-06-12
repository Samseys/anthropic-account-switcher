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

// Windows PATH handling: edits HKCU\Environment directly rather than via
// [Environment]::SetEnvironmentVariable, which expands REG_EXPAND_SZ and would
// flatten entries like %USERPROFILE%\bin (the classic setx PATH-mangling bug).

// envKeyPath is a var (not const) so tests can redirect it to a throwaway key.
var envKeyPath = `Environment`

// getUserPath returns the raw (unexpanded) user PATH and its registry type.
// A missing value is not an error; returns "" and EXPAND_SZ (Windows's default type).
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

// broadcastEnvChange sends WM_SETTINGCHANGE so running shells pick up the new PATH.
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

// samePathEntry reports whether a PATH entry refers to dir (whitespace-insensitive).
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
