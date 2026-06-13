// Command build is the cross-platform build tool for acc-claude. Everything the
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
	"strconv"
	"strings"

	installpkg "github.com/Samseys/anthropic-account-switcher/internal/install"
)

const (
	binary  = "acc-claude"
	pkg     = "./cmd/acc-claude"
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

// version derives a version from git, or "dev" outside a checkout. $VERSION
// overrides (the release/nightly workflows pass it).
func version() string {
	if v := strings.TrimSpace(os.Getenv("VERSION")); v != "" {
		return strings.TrimPrefix(v, "v")
	}
	return gitVersion()
}

// gitVersion turns the git state into a version. A clean checkout exactly on a
// release tag returns that tag verbatim (e.g. "1.0.6"). Any build *ahead* of the
// latest tag, or with a dirty tree, is newer than that tag — but semver orders
// "1.0.6-<suffix>" below "1.0.6", which would make `acc-claude update` nag a dev
// build to "upgrade" to the very release it is already ahead of. So, exactly like
// the nightly job (ci.yml), we version it as a prerelease of the *next* patch
// ("X.Y.(Z+1)-dev.<sha>[.dirty]"): it sorts above the current release and below
// the next one.
//
// --match/--exclude restrict describe to stable release tags; nightly tags are
// version-prefixed (vX.Y.Z-nightly.*) and would otherwise drift the base up.
func gitVersion() string {
	out, err := exec.Command("git", "describe", "--tags", "--long", "--dirty",
		"--match", "v[0-9]*", "--exclude", "*-nightly.*").Output()
	desc := strings.TrimSpace(string(out))
	if err != nil || desc == "" {
		return untaggedVersion()
	}

	// desc is "<tag>-<count>-g<sha>" with an optional "-dirty" suffix. The tag may
	// itself contain '-' (a prerelease tag), so split from the right.
	dirty := strings.HasSuffix(desc, "-dirty")
	desc = strings.TrimSuffix(desc, "-dirty")
	gi := strings.LastIndex(desc, "-g") // sha is hex, so this is the describe separator
	if gi < 0 {
		return strings.TrimPrefix(desc, "v")
	}
	gsha := desc[gi+1:] // "g<sha>"
	rest := desc[:gi]   // "<tag>-<count>"
	ci := strings.LastIndex(rest, "-")
	if ci < 0 {
		return strings.TrimPrefix(rest, "v")
	}
	count, tag := rest[ci+1:], strings.TrimPrefix(rest[:ci], "v")

	if count == "0" && !dirty {
		return tag // clean build sitting exactly on a release tag
	}
	maj, min, patch := semverParts(tag)
	suffix := gsha
	if dirty {
		suffix += ".dirty"
	}
	return fmt.Sprintf("%d.%d.%d-dev.%s", maj, min, patch+1, suffix)
}

// untaggedVersion handles a checkout with no stable release tag (or no git at
// all): there is no base to bump, so we anchor at 0.0.1-dev.
func untaggedVersion() string {
	out, err := exec.Command("git", "describe", "--tags", "--always", "--dirty").Output()
	v := strings.TrimSpace(string(out))
	if err != nil || v == "" {
		return "dev"
	}
	dirty := strings.HasSuffix(v, "-dirty")
	v = strings.TrimSuffix(v, "-dirty")
	suffix := "g" + v // g-prefix keeps an all-digit short sha a valid identifier
	if dirty {
		suffix += ".dirty"
	}
	return "0.0.1-dev." + suffix
}

const versionPkg = "github.com/Samseys/anthropic-account-switcher/internal/paths"

// ldflags stamps the version. -s/-w are intentionally omitted: stripping DWARF
// makes an unsigned binary look obfuscated to antivirus heuristics.
func ldflags() string { return "-X " + versionPkg + ".Version=" + version() }

func exeSuffix(goos string) string {
	if goos == "windows" {
		return ".exe"
	}
	return ""
}

// goBuild compiles pkg for GOOS/GOARCH (empty = host) into out.
func goBuild(goos, goarch, out string) error {
	c := exec.Command("go", "build", "-trimpath", "-ldflags", ldflags(), "-o", out, pkg)
	c.Stdout, c.Stderr = os.Stdout, os.Stderr
	c.Env = append(os.Environ(), "CGO_ENABLED=0")
	if goos != "" {
		c.Env = append(c.Env, "GOOS="+goos, "GOARCH="+goarch)
	}
	return c.Run()
}

// goversioninfoPkg is pinned so we use flags available in that specific release.
const goversioninfoPkg = "github.com/josephspurrier/goversioninfo/cmd/goversioninfo@v1.4.1"

// buildTarget compiles one GOOS/GOARCH into out. On Windows it first generates
// a .syso resource (file metadata + app manifest) so the linker embeds them —
// a metadata-less unsigned binary is a common antivirus false-positive.
func buildTarget(goos, goarch, out string) error {
	if goos == "windows" {
		syso, err := genWindowsResource(goarch)
		if err != nil {
			return fmt.Errorf("generating windows resource: %w", err)
		}
		defer os.Remove(syso)
	}
	return goBuild(goos, goarch, out)
}

// genWindowsResource writes cmd/acc-claude/resource_windows_<arch>.syso (the
// _windows_<arch> suffix acts as a Go build constraint). Fetches goversioninfo
// on first call (requires network access, as CI has).
func genWindowsResource(arch string) (string, error) {
	maj, min, patch := semverParts(version())
	dir := filepath.Join("cmd", "acc-claude")
	out := filepath.Join(dir, "resource_windows_"+arch+".syso")
	args := []string{
		"run", goversioninfoPkg,
		"-64", // 64-bit resource; combined with -arm below this selects ARM64
		"-o", out,
		"-manifest", filepath.Join(dir, "acc-claude.manifest"),
		"-ver-major", strconv.Itoa(maj),
		"-ver-minor", strconv.Itoa(min),
		"-ver-patch", strconv.Itoa(patch),
		"-product-ver-major", strconv.Itoa(maj),
		"-product-ver-minor", strconv.Itoa(min),
		"-product-ver-patch", strconv.Itoa(patch),
		"-file-version", version(),
		"-product-version", version(),
	}
	if arch == "arm64" {
		args = append(args, "-arm")
	}
	args = append(args, filepath.Join(dir, "versioninfo.json"))
	c := exec.Command("go", args...)
	c.Stdout, c.Stderr = os.Stdout, os.Stderr
	return out, c.Run()
}

// semverParts extracts major/minor/patch from a version string; missing or
// non-numeric parts (e.g. "dev") default to 0.
func semverParts(v string) (maj, min, patch int) {
	v = strings.TrimPrefix(v, "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	at := func(i int) int {
		if i < len(parts) {
			n, _ := strconv.Atoi(parts[i])
			return n
		}
		return 0
	}
	return at(0), at(1), at(2)
}

func build() error {
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return err
	}
	out := filepath.Join(binDir, binary+exeSuffix(runtime.GOOS))
	fmt.Printf("building %s (version %s)\n", out, version())
	return buildTarget(runtime.GOOS, runtime.GOARCH, out)
}

// install compiles the current source and installs that build the same way
// install.ps1/install.sh install a downloaded release: it copies the binary into
// the managed per-user location and runs `register` (PATH + completion + status
// line), so the tool is immediately usable. Placement is the build tool's job —
// the binary still never copies itself.
func install() error {
	if err := build(); err != nil {
		return err
	}
	src := filepath.Join(binDir, binary+exeSuffix(runtime.GOOS))
	dst := installpkg.InstalledBinaryPath()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if err := copyExecutable(src, dst); err != nil {
		return fmt.Errorf("installing %s -> %s: %w (is a running acc-claude holding it open?)", src, dst, err)
	}
	fmt.Printf("installed -> %s\n", dst)

	// Run the freshly-installed copy's register, mirroring the install scripts.
	c := exec.Command(dst, "register")
	c.Stdout, c.Stderr = os.Stdout, os.Stderr
	return c.Run()
}

// copyExecutable copies src over dst, preserving the executable bit on Unix.
func copyExecutable(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o755)
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
		if err := buildTarget(p.os, p.arch, out); err != nil {
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

// writeSums writes a SHA256SUMS manifest in the format `sha256sum` produces.
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
