//go:build !windows

package main

// Unused on non-Windows: unlinking a running binary works there, so
// cmdUnregister deletes the file directly.
func scheduleSelfDelete(string) error { return nil }
