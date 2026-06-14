package profile

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Samseys/anthropic-account-switcher/internal/paths"
	"github.com/Samseys/anthropic-account-switcher/internal/proc"
	"github.com/Samseys/anthropic-account-switcher/internal/usage"
)

// usageSnapshot is what the sensor records per poll. It feeds two consumers:
// the watcher's trip detection (global state file) and offline candidate ranking
// (per-profile copy, updated whenever that account is active).
type usageSnapshot struct {
	Account   string        `json:"account"`
	FiveHour  *usage.Window `json:"fiveHour"`
	SevenDay  *usage.Window `json:"sevenDay"`
	UpdatedAt time.Time     `json:"updatedAt"`
	// Online marks an endpoint-sourced reading. The watcher writes its own polls
	// back to the shared state file, so it must not re-read them as a sensor
	// reading — that double-reports the line. See decideWatchAction.
	Online bool `json:"online,omitempty"`
}

// fileUsageCache is the per-profile snapshot; the global state file lives in the profile root.
const fileUsageCache = "usage.json"

func usageStateFile() string { return filepath.Join(paths.ProfileDir, ".usage-state.json") }

// Tunables for the file watcher. Reads are local so the cadence is cheap;
// staleness bounds prevent acting on data from an idle or long-past session.
const (
	watchFileInterval = 5 * time.Second // how often to re-read the local state file
	// sensorQuietWindow is how long to trust the sensor before falling back to
	// the online endpoint. The CLI writes on every message, so a gap longer than
	// this means it has gone quiet (e.g. switched to the VSCode panel, which
	// never runs the sensor). Kept short to catch the handoff in ~2min.
	sensorQuietWindow = 2 * time.Minute
	cacheStaleAfter   = 6 * time.Hour // ignore a candidate's cached usage older than this
	// activeStaleAfter triggers an online re-poll of the active account. Short
	// because `usage`/`list` must show near-live data in the VSCode panel, where
	// the sensor never runs. The poll is still throttled to usage.MinInterval.
	activeStaleAfter = 2 * time.Minute
)

// activePollMarker records the last online poll of the active account, throttling
// to usage.MinInterval across process invocations. Stamped on the *attempt* so
// a failing poll still backs off instead of hitting the endpoint on every panel tick.
func activePollMarker() string { return filepath.Join(paths.ProfileDir, ".active-poll") }

func recentActivePoll() bool {
	b, err := os.ReadFile(activePollMarker())
	if err != nil {
		return false
	}
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(string(b)))
	if err != nil {
		return false
	}
	return time.Since(t) < usage.MinInterval
}

func markActivePoll() {
	_ = paths.WriteFileAtomic(activePollMarker(), []byte(time.Now().Format(time.RFC3339)), 0o600)
}

// StatusLine reads the JSON Claude Code pipes on stdin, prints a usage summary
// for the status bar, and records usage for offline autoswitch. Deliberately
// total: any failure still prints a line and returns nil — Claude Code kills
// slow or failing status-line commands.
func StatusLine() error {
	// No piped input means interactive use — block forever on stdin; explain instead.
	if fi, err := os.Stdin.Stat(); err == nil && fi.Mode()&os.ModeCharDevice != 0 {
		fmt.Printf("The 'statusline' command reads Claude Code's JSON on stdin; it is meant for\n"+
			"your status line, not the terminal. Run '%s statusline --install' to set it up.\n", paths.Bin)
		return nil
	}

	stdin, _ := io.ReadAll(io.LimitReader(os.Stdin, 1<<20))
	rep, _ := usage.ParseStatusLine(stdin)

	name := activeProfile()
	display := name
	if display == "" {
		display = "active account" // logged in but unsaved
	}

	head := bold(display)
	if model := usage.ParseStatusLineModel(stdin); model != "" {
		head += "  " + dim(model)
	}

	// Context and rate-limit windows; any may be absent depending on version/plan.
	var segs []string
	if ctx, ok := usage.ParseStatusLineContext(stdin); ok {
		segs = append(segs, labelledMeter("ctx", ctx))
	}
	if rep != nil && rep.FiveHour != nil {
		segs = append(segs, windowSegment("5h", rep.FiveHour), windowSegment("7d", rep.SevenDay))
	}

	sep := "  " + dim("│") + "  "
	if len(segs) == 0 {
		fmt.Println(head + "  " + dim("usage n/a"))
		return nil
	}
	fmt.Println(head + sep + strings.Join(segs, sep))

	// Only record for a saved profile (watcher keys on names); never fatal.
	// Context is per-conversation, not per-account, so it is display-only.
	if name != "" && rep != nil && rep.FiveHour != nil {
		recordUsage(usageSnapshot{Account: name, FiveHour: rep.FiveHour, SevenDay: rep.SevenDay, UpdatedAt: time.Now()})
	}
	return nil
}

// recordUsage writes the global state file and the profile's own cache. Both are best-effort.
func recordUsage(snap usageSnapshot) {
	b, err := json.Marshal(snap)
	if err != nil {
		return
	}
	_ = paths.WriteFileAtomic(usageStateFile(), b, 0o600)
	if profileExists(snap.Account) {
		_ = paths.WriteFileAtomic(filepath.Join(profilePath(snap.Account), fileUsageCache), b, 0o600)
	}
}

// recordProfileUsage writes only the profile's cache (not the global state file),
// used when the online path polls candidates.
func recordProfileUsage(name string, five, seven *usage.Window) {
	if !profileExists(name) {
		return
	}
	snap := usageSnapshot{Account: name, FiveHour: five, SevenDay: seven, UpdatedAt: time.Now()}
	if b, err := json.Marshal(snap); err == nil {
		_ = paths.WriteFileAtomic(filepath.Join(profilePath(name), fileUsageCache), b, 0o600)
	}
}

func readUsageState() (usageSnapshot, bool) { return readSnapshot(usageStateFile()) }
func readProfileUsageCache(name string) (usageSnapshot, bool) {
	return readSnapshot(filepath.Join(profilePath(name), fileUsageCache))
}

func readSnapshot(path string) (usageSnapshot, bool) {
	var s usageSnapshot
	b, err := os.ReadFile(path)
	if err != nil {
		return s, false
	}
	if err := json.Unmarshal(b, &s); err != nil {
		return s, false
	}
	return s, true
}

type watchAction int

const (
	actNone     watchAction = iota // nothing new to act on this tick
	actSnapshot                    // act on the sensor's recorded reading
	actOnline                      // poll the online endpoint
)

// decideWatchAction is the watch loop's branch decision, extracted as a pure
// function so the CLI↔panel handoff is unit-testable. Trusts the sensor only
// while it is actively writing (within sensorQuietWindow); falls back to polling
// the endpoint when the sensor goes quiet (e.g. switched to the VSCode panel).
// onlineBackoff suppresses the online poll until a 429 reset window has passed.
// once forces a single evaluation regardless of cadence.
func decideWatchAction(snap usageSnapshot, ok bool, active string, now, lastSeen, lastOnline, onlineBackoff time.Time, claudeRunning, once bool) watchAction {
	sensorRecent := ok && !snap.Online && snap.Account == active && now.Sub(snap.UpdatedAt) <= sensorQuietWindow
	pollDue := now.Sub(lastOnline) >= endpointPollInterval && now.After(onlineBackoff)
	switch {
	case sensorRecent && (once || snap.UpdatedAt.After(lastSeen)):
		return actSnapshot
	case !sensorRecent && (once || (claudeRunning && pollDue)):
		return actOnline
	default:
		return actNone
	}
}

// tripThresholds is the per-window utilization (0–100) at which autoswitch trips.
// The sensor matches what Claude Code shows, so it trips at the ceiling; the
// endpoint is coarser and polled, so it leaves more headroom (most on 5h).
type tripThresholds struct {
	fiveHour float64
	sevenDay float64
}

var (
	sensorThresholds = tripThresholds{fiveHour: 99, sevenDay: 99}
	onlineThresholds = tripThresholds{fiveHour: 95, sevenDay: 98}
)

// trigger reports the window that tripped (5h wins ties) and its utilization,
// or ok=false when neither is at its threshold.
func (t tripThresholds) trigger(five, seven float64) (window string, pct float64, ok bool) {
	switch {
	case five >= t.fiveHour:
		return "5h usage", five, true
	case seven >= t.sevenDay:
		return "7d usage", seven, true
	default:
		return "", 0, false
	}
}

func (t tripThresholds) describe() string {
	if t.fiveHour == t.sevenDay {
		return fmt.Sprintf("%.0f%%", t.fiveHour)
	}
	return fmt.Sprintf("%.0f%% 5h / %.0f%% 7d", t.fiveHour, t.sevenDay)
}

// AutoSwitchOptions configures the auto-switcher.
type AutoSwitchOptions struct {
	FiveHour float64 // when > 0, overrides the 5h trip threshold for both sources
	SevenDay float64 // when > 0, overrides the 7d trip threshold for both sources
	Once     bool    // check once and return instead of looping
	DryRun   bool    // report the decision but do not switch
}

// sensor/online resolve each source's thresholds, applying any per-window
// override (which spans both sources) on top of the source's defaults.
func (opts AutoSwitchOptions) sensor() tripThresholds { return opts.resolve(sensorThresholds) }
func (opts AutoSwitchOptions) online() tripThresholds { return opts.resolve(onlineThresholds) }
func (opts AutoSwitchOptions) resolve(def tripThresholds) tripThresholds {
	if opts.FiveHour > 0 {
		def.fiveHour = opts.FiveHour
	}
	if opts.SevenDay > 0 {
		def.sevenDay = opts.SevenDay
	}
	return def
}

// AutoSwitch watches the active account and switches to the profile with the most
// headroom when either window crosses its trip threshold. Prefers the local sensor
// reading; falls back to polling the endpoint when the sensor is quiet (e.g. the
// VSCode panel), at more conservative thresholds. Candidates are always ranked by
// current endpoint usage. Loops until interrupted unless Once is set.
func AutoSwitch(opts AutoSwitchOptions) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	if !opts.Once {
		fmt.Printf("Watching usage; switching at %s (sensor) / %s (endpoint). Ctrl-C to stop.\n",
			opts.sensor().describe(), opts.online().describe())
	}
	// In-place on a terminal; one line per poll when redirected or --once.
	sw := newStatusWriter(!opts.Once && stdoutIsTerminal())
	var lastSeen, lastOnline, onlineBackoff time.Time
	for {
		snap, ok := readUsageState()
		switch decideWatchAction(snap, ok, activeProfile(), time.Now(), lastSeen, lastOnline, onlineBackoff, proc.ClaudeRunning(), opts.Once) {
		case actSnapshot:
			lastSeen = snap.UpdatedAt
			evaluateSnapshot(ctx, opts, sw, snap)
		case actOnline:
			lastOnline = time.Now()
			if backoff := evaluateOnline(ctx, opts, sw); backoff > 0 {
				onlineBackoff = time.Now().Add(backoff)
			}
		}
		if opts.Once {
			return nil
		}
		select {
		case <-ctx.Done():
			sw.commit()
			fmt.Println("Stopped.")
			return nil
		case <-time.After(watchFileInterval):
		}
	}
}

// evaluateSnapshot logs and switches on a fresh sensor reading. The caller has
// already verified the snapshot is current and for the still-active account.
func evaluateSnapshot(ctx context.Context, opts AutoSwitchOptions, sw *statusWriter, snap usageSnapshot) {
	sw.update(watchLine(snap.UpdatedAt, snap.Account, snap.FiveHour, snap.SevenDay, false))
	tripAndSwitch(ctx, opts, sw, snap.Account, windowPct(snap.FiveHour), windowPct(snap.SevenDay), opts.sensor())
}

// tripAndSwitch switches when either window crosses th, picking the candidate with
// the most headroom from current endpoint usage. Candidates are polled online, so
// they rank by the online 5h threshold regardless of which source tripped. Commits
// the live line first so its messages land on their own row.
func tripAndSwitch(ctx context.Context, opts AutoSwitchOptions, sw *statusWriter, active string, five, seven float64, th tripThresholds) {
	window, pct, ok := th.trigger(five, seven)
	if !ok {
		return
	}
	sw.commit()
	target, reason, err := chooseTargetOnline(ctx, active, opts.online().fiveHour)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  %s at %.0f%% but cannot switch: %v\n", window, pct, err)
		return
	}
	if opts.DryRun {
		fmt.Printf("  %s reached %.0f%%; would switch to %q (%s) [dry-run]\n", window, pct, target, reason)
		return
	}
	fmt.Printf("  %s reached %.0f%%; switching to %q (%s)\n", window, pct, target, reason)
	if err := Switch(target); err != nil {
		fmt.Fprintf(os.Stderr, "  switching to %q: %v\n", target, err)
	}
}

type candidate struct {
	name  string
	five  float64
	known bool // false when no usage (live or cached) was available for this profile
}

// pickCandidate ranks candidates: lowest known 5h usage below threshold wins;
// profiles with no usable reading are a last resort (switching refreshes the token).
func pickCandidate(below, unknown []candidate, threshold float64) (name, reason string, err error) {
	if len(below) > 0 {
		sort.Slice(below, func(i, j int) bool { return below[i].five < below[j].five })
		b := below[0]
		return b.name, fmt.Sprintf("lowest 5h usage, %.0f%%", b.five), nil
	}
	if len(unknown) > 0 {
		sort.Slice(unknown, func(i, j int) bool { return unknown[i].name < unknown[j].name })
		return unknown[0].name, "no recent usage available; will refresh on switch", nil
	}
	return "", "", fmt.Errorf("every other saved account is also at or above %.0f%% by current usage", threshold)
}
