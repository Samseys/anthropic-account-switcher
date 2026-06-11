//go:build !windows

package store

// On non-Windows platforms profile snapshots are stored as plain files (macOS
// keeps the *live* credential in the Keychain, but snapshots are files there
// too). These are identity functions so the call sites stay platform-agnostic.

// ProtectCreds returns the at-rest form of a snapshot (plaintext here).
func ProtectCreds(data []byte) ([]byte, error) { return data, nil }

// UnprotectCreds reverses ProtectCreds (plaintext here).
func UnprotectCreds(data []byte) ([]byte, error) { return data, nil }
