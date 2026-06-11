// Package update implements self-update over the GitHub Releases API. The
// release pipeline publishes one static binary per OS/arch plus a SHA256SUMS
// manifest; Update discovers the latest release, downloads the asset for this
// platform, verifies it against SHA256SUMS, and (via internal/install) atomically
// replaces the running binary in place. MaybeNotify is the passive "a new
// version is available" nudge.
package update

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/Samseys/anthropic-account-switcher/internal/install"
	"github.com/Samseys/anthropic-account-switcher/internal/paths"
)

const repo = "Samseys/anthropic-account-switcher"

// assetName is the release asset for the platform we are running on. It must
// match the names produced by the release workflow (claude-acc_<os>_<arch>).
func assetName() string {
	name := fmt.Sprintf("%s_%s_%s", paths.Bin, runtime.GOOS, runtime.GOARCH)
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return name
}

type ghRelease struct {
	TagName string `json:"tag_name"`
	HTMLURL string `json:"html_url"`
	Assets  []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

func httpGet(url string, timeout time.Duration) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	// GitHub rejects requests without a User-Agent.
	req.Header.Set("User-Agent", paths.Bin+"/"+paths.VersionString())
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

// compareVersions returns -1 if a < b, 0 if equal, +1 if a > b. It parses up to
// three dotted numeric components (a leading "v" is ignored); anything it can't
// parse numerically falls back to a string compare so we still detect a change.
func compareVersions(a, b string) int {
	na, oka := parseSemver(a)
	nb, okb := parseSemver(b)
	if !oka || !okb {
		switch {
		case a == b:
			return 0
		case a < b:
			return -1
		default:
			return 1
		}
	}
	for i := range 3 {
		if na[i] != nb[i] {
			if na[i] < nb[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}

func parseSemver(s string) ([3]int, bool) {
	var out [3]int
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	// Drop any pre-release/build suffix (e.g. "1.2.3-rc1").
	if i := strings.IndexAny(s, "-+"); i >= 0 {
		s = s[:i]
	}
	parts := strings.Split(s, ".")
	if len(parts) == 0 || len(parts) > 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

// Update updates to the latest release (or, with checkOnly, only reports
// whether one is available).
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
		fmt.Printf("Already up to date.\n")
		return nil
	}
	if checkOnly {
		fmt.Printf("A newer version is available: %s\nRun '%s update' to install it.\n", latest, paths.Bin)
		return nil
	}

	want := assetName()
	var assetURL, sumsURL string
	for _, a := range rel.Assets {
		switch a.Name {
		case want:
			assetURL = a.URL
		case "SHA256SUMS":
			sumsURL = a.URL
		}
	}
	if assetURL == "" {
		return fmt.Errorf("release %s has no asset %q for your platform (%s/%s)",
			rel.TagName, want, runtime.GOOS, runtime.GOARCH)
	}

	fmt.Printf("Downloading %s...\n", want)
	data, err := httpGet(assetURL, 5*time.Minute)
	if err != nil {
		return fmt.Errorf("downloading %s: %w", want, err)
	}

	if sumsURL != "" {
		sums, err := httpGet(sumsURL, 30*time.Second)
		if err != nil {
			return fmt.Errorf("downloading SHA256SUMS: %w", err)
		}
		if err := verifyChecksum(data, want, sums); err != nil {
			return err
		}
		fmt.Println("Checksum verified.")
	} else {
		fmt.Println("WARNING: release has no SHA256SUMS; skipping checksum verification.")
	}

	self, err := paths.SelfPath()
	if err != nil {
		return err
	}
	if err := install.ReplaceRunningBinary(self, data, 0o755); err != nil {
		return fmt.Errorf("replacing %s: %w", self, err)
	}

	fmt.Printf("Updated '%s' to %s (%s).\n", paths.Bin, latest, self)
	return nil
}

// verifyChecksum confirms that data hashes to the SHA256SUMS entry for name.
func verifyChecksum(data []byte, name string, sums []byte) error {
	sum := sha256.Sum256(data)
	got := hex.EncodeToString(sum[:])
	for line := range strings.SplitSeq(string(sums), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		// SHA256SUMS lines are "<hex>  <filename>"; the name may carry a
		// leading "*" for binary mode.
		if strings.TrimPrefix(fields[1], "*") != name {
			continue
		}
		if !strings.EqualFold(fields[0], got) {
			return fmt.Errorf("checksum mismatch for %s: expected %s, got %s", name, fields[0], got)
		}
		return nil
	}
	return fmt.Errorf("no checksum for %s in SHA256SUMS", name)
}

// ---- passive "update available" nudge ----

const updateCheckInterval = 24 * time.Hour

type updateCheck struct {
	CheckedAt int64  `json:"checkedAt"`
	Latest    string `json:"latest"`
}

func updateCheckPath() string {
	return filepath.Join(paths.ProfileDir, ".update-check.json")
}

// MaybeNotify prints a one-line "new version available" hint to stderr. The
// hint fires on *every* command (the caller already excludes --json), but the
// network check that feeds it is throttled to once per updateCheckInterval: the
// warning is driven by the last-known "latest" recorded in the cache, not by
// whether a check happened on this run. It is best-effort and silent on any
// failure — never disrupting the command the user actually ran.
func MaybeNotify() {
	if os.Getenv("CLAUDE_ACC_NO_UPDATE_CHECK") != "" {
		return
	}
	current := paths.VersionString()
	if current == "dev" {
		return // local build; nothing to compare against
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

// refreshLatestIfStale re-queries the Releases API at most once per
// updateCheckInterval, recording the result and the time in the cache. A failed
// fetch writes nothing — the cache (and so the throttle) only advances on
// success. The warning still fires from the last-known "latest"; an offline
// user who finds the retries bothersome can set CLAUDE_ACC_NO_UPDATE_CHECK=1.
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

// lastKnownLatest returns the most recent "latest" version recorded in the
// cache, regardless of age, or "" if there is no usable cache yet.
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
