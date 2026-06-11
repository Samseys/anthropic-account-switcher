//go:build !windows

package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Samseys/anthropic-account-switcher/internal/paths"
)

// withHome points paths.Home (and so shellRC) at a temp dir with no SHELL set,
// so PATH edits land in <tmp>/.profile instead of the real shell rc.
func withHome(t *testing.T) string {
	t.Helper()
	old := paths.Home
	paths.Home = t.TempDir()
	t.Setenv("SHELL", "")
	t.Cleanup(func() { paths.Home = old })
	return filepath.Join(paths.Home, ".profile")
}

func TestAddRemoveUserPathRoundTrip(t *testing.T) {
	rc := withHome(t)
	dir := "/home/me/.local/bin"

	// A user line that merely mentions the same dir must survive unregister.
	userLine := `export PATH="` + dir + `/extra:$PATH" # mine`
	if err := os.WriteFile(rc, []byte(userLine+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	msg, err := addUserPath(dir)
	if err != nil || !strings.Contains(msg, "Added") {
		t.Fatalf("addUserPath msg=%q err=%v", msg, err)
	}
	got, _ := paths.ReadFileOpt(rc)
	if !strings.Contains(got, "# "+paths.Bin) || !strings.Contains(got, `export PATH="`+dir+`:$PATH"`) {
		t.Fatalf("block not written: %q", got)
	}

	if err := removeUserPath(dir); err != nil {
		t.Fatal(err)
	}
	got, _ = paths.ReadFileOpt(rc)
	if strings.Contains(got, "# "+paths.Bin) {
		t.Fatalf("marker not removed: %q", got)
	}
	if !strings.Contains(got, userLine) {
		t.Fatalf("unrelated user PATH line was clobbered: %q", got)
	}
	// The blank line the block prepends should not linger.
	if strings.Contains(got, "\n\n") {
		t.Fatalf("blank line accumulated: %q", got)
	}
}

func TestAddUserPathIsIdempotent(t *testing.T) {
	withHome(t)
	dir := "/home/me/.local/bin"

	if msg, err := addUserPath(dir); err != nil || !strings.Contains(msg, "Added") {
		t.Fatalf("first add: msg=%q err=%v", msg, err)
	}
	// A second register must not write the block again.
	if msg, err := addUserPath(dir); err != nil || !strings.Contains(msg, "already") {
		t.Fatalf("second add should be a no-op: msg=%q err=%v", msg, err)
	}
}
