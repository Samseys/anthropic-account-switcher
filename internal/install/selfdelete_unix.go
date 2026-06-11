//go:build !windows

package install

// Unused on non-Windows: unlinking a running binary works there, so
// Unregister deletes the file directly.
func scheduleSelfDelete(string) error { return nil }
