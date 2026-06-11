//go:build windows

package install

import (
	"strings"
	"testing"

	"golang.org/x/sys/windows/registry"

	"github.com/Samseys/anthropic-account-switcher/internal/paths"
)

// withScratchEnvKey redirects envKeyPath at a throwaway HKCU subkey so PATH
// edits in tests never touch the real user environment.
func withScratchEnvKey(t *testing.T) {
	t.Helper()
	keyPath := `Software\` + paths.Bin + `-test`
	k, _, err := registry.CreateKey(registry.CURRENT_USER, keyPath, registry.ALL_ACCESS)
	if err != nil {
		t.Fatalf("creating scratch key: %v", err)
	}
	k.Close()
	old := envKeyPath
	envKeyPath = keyPath
	t.Cleanup(func() {
		envKeyPath = old
		_ = registry.DeleteKey(registry.CURRENT_USER, keyPath)
	})
}

func currentPath(t *testing.T) string {
	t.Helper()
	v, _, err := getUserPath()
	if err != nil {
		t.Fatalf("getUserPath: %v", err)
	}
	return v
}

func TestAddRemoveUserPathRoundTrip(t *testing.T) {
	withScratchEnvKey(t)
	// Seed a pre-existing entry to prove we append rather than clobber, and that
	// it survives unregister.
	existing := `C:\Windows\System32`
	if err := setUserPath(existing, registry.EXPAND_SZ); err != nil {
		t.Fatal(err)
	}
	dir := `C:\Tools\bin`

	msg, err := addUserPath(dir)
	if err != nil || !strings.Contains(msg, "Added") {
		t.Fatalf("addUserPath msg=%q err=%v", msg, err)
	}
	if got, want := currentPath(t), existing+";"+dir; got != want {
		t.Fatalf("PATH after add = %q, want %q", got, want)
	}

	if err := removeUserPath(dir); err != nil {
		t.Fatal(err)
	}
	if got := currentPath(t); got != existing {
		t.Fatalf("PATH after remove = %q, want %q (pre-existing entry must survive)", got, existing)
	}
}

func TestAddUserPathIsIdempotent(t *testing.T) {
	withScratchEnvKey(t)
	dir := `C:\Tools\bin`

	if msg, err := addUserPath(dir); err != nil || !strings.Contains(msg, "Added") {
		t.Fatalf("first add: msg=%q err=%v", msg, err)
	}
	// A second register must not add a duplicate entry.
	if msg, err := addUserPath(dir); err != nil || !strings.Contains(msg, "already") {
		t.Fatalf("second add should be a no-op: msg=%q err=%v", msg, err)
	}
	if got := currentPath(t); got != dir {
		t.Fatalf("PATH = %q, want single entry %q", got, dir)
	}
}
