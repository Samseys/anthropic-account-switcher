//go:build windows

package store

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// Profile credential snapshots are encrypted at rest with DPAPI (current-user
// scope), so they are unreadable to other accounts on the machine and useless
// if copied elsewhere. The live ~/.claude/.credentials.json stays plaintext —
// Claude Code itself reads that file.

const cryptprotectUIForbidden = 0x1

func dataBlob(d []byte) *windows.DataBlob {
	if len(d) == 0 {
		return &windows.DataBlob{}
	}
	return &windows.DataBlob{Size: uint32(len(d)), Data: &d[0]}
}

func blobToBytes(b *windows.DataBlob) []byte {
	if b.Data == nil || b.Size == 0 {
		return nil
	}
	out := make([]byte, b.Size)
	copy(out, unsafe.Slice(b.Data, b.Size))
	return out
}

// ProtectCreds returns the at-rest (DPAPI-encrypted) form of a snapshot.
func ProtectCreds(data []byte) ([]byte, error) {
	var out windows.DataBlob
	if err := windows.CryptProtectData(dataBlob(data), nil, nil, 0, nil, cryptprotectUIForbidden, &out); err != nil {
		return nil, err
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	return blobToBytes(&out), nil
}

func UnprotectCreds(data []byte) ([]byte, error) {
	var out windows.DataBlob
	if err := windows.CryptUnprotectData(dataBlob(data), nil, nil, 0, nil, cryptprotectUIForbidden, &out); err != nil {
		return nil, err
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	return blobToBytes(&out), nil
}
