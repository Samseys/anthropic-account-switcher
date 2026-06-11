//go:build !windows

package main

// Stubs so the runtime.GOOS == "windows" call sites compile on every target.

func addWindowsUserPath(string) (bool, error) { return false, nil }
func removeWindowsUserPath(string) error      { return nil }
