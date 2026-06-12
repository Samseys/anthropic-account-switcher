// Package update checks the GitHub Releases API for a newer version and tells
// the user how to upgrade via the installer. It never downloads or replaces the
// running binary (that dropper shape is what endpoint security flags).
package update

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"golang.org/x/mod/semver"

	"github.com/Samseys/anthropic-account-switcher/internal/paths"
)

const repo = "Samseys/anthropic-account-switcher"

func installCommand() string {
	const base = "https://raw.githubusercontent.com/" + repo + "/main"
	if runtime.GOOS == "windows" {
		return "irm " + base + "/install.ps1 | iex"
	}
	return "curl -fsSL " + base + "/install.sh | sh"
}

type ghRelease struct {
	TagName string `json:"tag_name"`
	HTMLURL string `json:"html_url"`
}

func httpGet(url string, timeout time.Duration) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", paths.Bin+"/"+paths.VersionString()) // required by GitHub
	req.Header.Set("Accept", "application/vnd.github+json")
	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return body, nil
}

func fetchLatestRelease(timeout time.Duration) (*ghRelease, error) {
	url := "https://api.github.com/repos/" + repo + "/releases/latest"
	body, err := httpGet(url, timeout)
	if err != nil {
		return nil, err
	}
	var rel ghRelease
	if err := json.Unmarshal(body, &rel); err != nil {
		return nil, fmt.Errorf("parsing release metadata: %w", err)
	}
	if rel.TagName == "" {
		return nil, fmt.Errorf("no published release found")
	}
	return &rel, nil
}

// compareVersions returns -1/0/+1 via semver order (leading "v" optional).
// Pre-releases sort below their release, so nightly builds see the stable as newer.
func compareVersions(a, b string) int {
	return semver.Compare(canonV(a), canonV(b))
}

func canonV(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "v") {
		s = "v" + s
	}
	return s
}

// Update prints the installer command if a newer release exists.
// checkOnly is accepted for CLI compatibility; force bypasses the "already up to date" check.
func Update(checkOnly, force bool) error {
	current := paths.VersionString()
	fmt.Printf("Current version: %s\n", current)
	fmt.Println("Checking for the latest release...")

	rel, err := fetchLatestRelease(15 * time.Second)
	if err != nil {
		return fmt.Errorf("checking for updates: %w", err)
	}
	latest := strings.TrimPrefix(rel.TagName, "v")
	fmt.Printf("Latest version:  %s\n", latest)

	if !force && current != "dev" && compareVersions(current, latest) >= 0 {
		fmt.Println("Already up to date.")
		return nil
	}

	fmt.Printf("\nA newer version is available: %s\n", latest)
	if rel.HTMLURL != "" {
		fmt.Printf("Release notes: %s\n", rel.HTMLURL)
	}
	fmt.Printf("\nTo update, re-run the installer:\n\n    %s\n\n", installCommand())
	return nil
}

const updateCheckInterval = 24 * time.Hour

type updateCheck struct {
	CheckedAt int64  `json:"checkedAt"`
	Latest    string `json:"latest"`
}

func updateCheckPath() string {
	return filepath.Join(paths.ProfileDir, ".update-check.json")
}

// MaybeNotify prints a one-line hint to stderr when a newer version is cached.
// The network refresh is throttled to once per updateCheckInterval; failures are silent.
func MaybeNotify() {
	if os.Getenv("ACC_CLAUDE_NO_UPDATE_CHECK") != "" {
		return
	}
	current := paths.VersionString()
	if current == "dev" {
		return
	}

	refreshLatestIfStale()

	latest := lastKnownLatest()
	if latest == "" {
		return
	}
	if compareVersions(current, latest) < 0 {
		fmt.Fprintf(os.Stderr, "\nA new version of %s is available: %s (you have %s). Run '%s update'.\n",
			paths.Bin, latest, current, paths.Bin)
	}
}

// refreshLatestIfStale re-queries the API at most once per updateCheckInterval.
// A failed fetch writes nothing, so the throttle only advances on success.
func refreshLatestIfStale() {
	c, _ := readUpdateCheck()
	if time.Since(time.Unix(c.CheckedAt, 0)) <= updateCheckInterval {
		return
	}
	rel, err := fetchLatestRelease(3 * time.Second)
	if err != nil {
		return
	}
	writeUpdateCheck(strings.TrimPrefix(rel.TagName, "v"))
}

func lastKnownLatest() string {
	c, _ := readUpdateCheck()
	return c.Latest
}

func readUpdateCheck() (updateCheck, bool) {
	var c updateCheck
	raw, ok := paths.ReadFileOpt(updateCheckPath())
	if !ok {
		return c, false
	}
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		return c, false
	}
	return c, true
}

func writeUpdateCheck(latest string) {
	if err := os.MkdirAll(paths.ProfileDir, 0o755); err != nil {
		return
	}
	data, err := json.Marshal(updateCheck{CheckedAt: time.Now().Unix(), Latest: latest})
	if err != nil {
		return
	}
	_ = paths.WriteFileAtomic(updateCheckPath(), data, 0o644)
}
