//go:build !windows

package store

// On non-Windows, profile snapshots are plain files (macOS keeps the *live*
// credential in the Keychain, but snapshots are files too). Identity functions
// so call sites stay platform-agnostic.

// ProtectCreds is a no-op on non-Windows (plaintext snapshots).
func ProtectCreds(data []byte) ([]byte, error) { return data, nil }

// UnprotectCreds is a no-op on non-Windows (plaintext snapshots).
func UnprotectCreds(data []byte) ([]byte, error) { return data, nil }
