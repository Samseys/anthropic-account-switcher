//go:build !windows

package main

// On non-Windows platforms profile snapshots are stored as plain files (macOS
// keeps the *live* credential in the Keychain, but snapshots are files there
// too). These are identity functions so the call sites stay platform-agnostic.

func protectCreds(data []byte) ([]byte, error)   { return data, nil }
func unprotectCreds(data []byte) ([]byte, error) { return data, nil }
