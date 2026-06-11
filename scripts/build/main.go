// Command build is the cross-platform build tool for claude-acc. Everything the
// Makefile needs that would otherwise differ between cmd.exe and a POSIX shell
// (cross-compiling, hashing, mkdir/rm) lives here so the Makefile stays trivial.
//
// Run via the Makefile (`make dist`) or directly: `go run ./scripts/build dist`.
package main

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

const (
	binary  = "claude-acc"
	pkg     = "./cmd/claude-acc"
	distDir = "dist"
	binDir  = "bin"
)

// platforms are the release targets, kept in sync with the release workflow.
var platforms = []struct{ os, arch string }{
	{"windows", "amd64"}, {"windows", "arm64"},
	{"darwin", "amd64"}, {"darwin", "arm64"},
	{"linux", "amd64"}, {"linux", "arm64"},
}

func main() {
	cmd := "build"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	var err error
	switch cmd {
	case "version":
		fmt.Println(version())
	case "build":
		err = build()
	case "install":
		err = install()
	case "dist":
		err = dist()
	case "clean":
		err = clean()
	default:
		err = fmt.Errorf("unknown command %q (want: version|build|install|dist|clean)", cmd)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// version derives a version from git (nearest tag + commit, "-dirty" when the
// tree has uncommitted changes), or "dev" outside a checkout. $VERSION overrides.
func version() string {
	if v := strings.TrimSpace(os.Getenv("VERSION")); v != "" {
		return strings.TrimPrefix(v, "v")
	}
	out, err := exec.Command("git", "describe", "--tags", "--always", "--dirty").Output()
	v := strings.TrimSpace(string(out))
	if err != nil || v == "" {
		return "dev"
	}
	return strings.TrimPrefix(v, "v")
}

func ldflags() string { return "-s -w -X main.version=" + version() }

func exeSuffix(goos string) string {
	if goos == "windows" {
		return ".exe"
	}
	return ""
}

// goBuild compiles pkg for the given GOOS/GOARCH (empty = host) to out.
func goBuild(goos, goarch, out string) error {
	c := exec.Command("go", "build", "-trimpath", "-ldflags", ldflags(), "-o", out, pkg)
	c.Stdout, c.Stderr = os.Stdout, os.Stderr
	c.Env = append(os.Environ(), "CGO_ENABLED=0")
	if goos != "" {
		c.Env = append(c.Env, "GOOS="+goos, "GOARCH="+goarch)
	}
	return c.Run()
}

func build() error {
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return err
	}
	out := filepath.Join(binDir, binary+exeSuffix(runtime.GOOS))
	fmt.Printf("building %s (version %s)\n", out, version())
	return goBuild("", "", out)
}

func install() error {
	c := exec.Command("go", "install", "-trimpath", "-ldflags", ldflags(), pkg)
	c.Stdout, c.Stderr = os.Stdout, os.Stderr
	c.Env = append(os.Environ(), "CGO_ENABLED=0")
	return c.Run()
}

func dist() error {
	if err := clean(); err != nil {
		return err
	}
	if err := os.MkdirAll(distDir, 0o755); err != nil {
		return err
	}
	ver := version()
	var names []string
	for _, p := range platforms {
		name := fmt.Sprintf("%s_%s_%s%s", binary, p.os, p.arch, exeSuffix(p.os))
		out := filepath.Join(distDir, name)
		fmt.Println("building", out)
		if err := goBuild(p.os, p.arch, out); err != nil {
			return err
		}
		names = append(names, name)
	}
	if err := writeSums(distDir, names); err != nil {
		return err
	}
	fmt.Printf("done -> %s (version %s)\n", distDir, ver)
	return nil
}

// writeSums writes a coreutils-style SHA256SUMS manifest ("<hex>  <name>"),
// matching what `sha256sum` produces and what update/install verify against.
func writeSums(dir string, names []string) error {
	sort.Strings(names)
	var b strings.Builder
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return err
		}
		fmt.Fprintf(&b, "%x  %s\n", sha256.Sum256(data), name)
	}
	return os.WriteFile(filepath.Join(dir, "SHA256SUMS"), []byte(b.String()), 0o644)
}

func clean() error {
	for _, d := range []string{distDir, binDir} {
		if err := os.RemoveAll(d); err != nil {
			return err
		}
	}
	return nil
}
