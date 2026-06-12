package paths

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestSanitize(t *testing.T) {
	cases := []struct{ in, want string }{
		{"work", "work"},
		{"a@example.com", "a@example.com"},
		{"v1.2_x-y", "v1.2_x-y"},
		{"has space", "has_space"},
		{`..\..\evil`, ".._.._evil"},
		{"a/b:c*d?e", "a_b_c_d_e"},
		{"", ""},
	}
	for _, c := range cases {
		if got := Sanitize(c.in); got != c.want {
			t.Errorf("Sanitize(%q) = %q, want %q", c.in, got, c.want)
		}
		if got := Sanitize(Sanitize(c.in)); got != c.want {
			t.Errorf("Sanitize is not idempotent on %q: %q", c.in, got)
		}
	}
}

func TestPathEqual(t *testing.T) {
	if !PathEqual("/a/b", "/a/b/") {
		t.Error("trailing separator should be ignored")
	}
	if !PathEqual(`C:\x\y\`, `C:\x\y`) {
		t.Error("trailing backslash should be ignored")
	}
	if PathEqual("/a/b", "/a/c") {
		t.Error("different paths must not compare equal")
	}
	caseFolded := PathEqual(`C:\Tools`, `c:\tools`)
	if want := runtime.GOOS == "windows"; caseFolded != want {
		t.Errorf("case-insensitive compare = %v on %s, want %v", caseFolded, runtime.GOOS, want)
	}
}

func TestWriteFileAtomic(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.json")
	if err := WriteFileAtomic(path, []byte(`{"a":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `{"a":1}` {
		t.Fatalf("content = %q", b)
	}
	// Claude Code's JSON parser rejects a BOM; the write must not add one.
	if bytes.HasPrefix(b, []byte{0xEF, 0xBB, 0xBF}) {
		t.Fatal("write added a UTF-8 BOM")
	}

	if err := WriteFileAtomic(path, []byte(`{"a":2}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if b, _ = os.ReadFile(path); string(b) != `{"a":2}` {
		t.Fatalf("after overwrite, content = %q", b)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("leftover files in dir: %v", entries)
	}
}

func TestReadHelpers(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(p, []byte("  hello \r\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if got := ReadTrim(p); got != "hello" {
		t.Errorf("ReadTrim = %q", got)
	}
	if got := ReadTrim(filepath.Join(dir, "missing")); got != "" {
		t.Errorf("ReadTrim on a missing file = %q, want \"\"", got)
	}

	if s, ok := ReadFileOpt(p); !ok || s != "  hello \r\n" {
		t.Errorf("ReadFileOpt = %q, %v", s, ok)
	}
	if _, ok := ReadFileOpt(filepath.Join(dir, "missing")); ok {
		t.Error("ReadFileOpt reported a missing file as readable")
	}

	if !FileExists(p) {
		t.Error("FileExists = false for an existing file")
	}
	if FileExists(filepath.Join(dir, "missing")) {
		t.Error("FileExists = true for a missing file")
	}
}
