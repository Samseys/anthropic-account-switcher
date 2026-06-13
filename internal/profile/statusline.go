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

// usageSnapshot is what the status-line sensor records: the active account, its
// rate-limit windows (utilization + reset), and when they were sampled. It feeds
// two consumers — the watcher's trip detection (a single global state file) and
// offline candidate ranking (a copy in each profile's own directory, refreshed
// whenever that account is the active one).
type usageSnapshot struct {
	Account   string        `json:"account"`
	FiveHour  *usage.Window `json:"fiveHour"`
	SevenDay  *usage.Window `json:"sevenDay"`
	UpdatedAt time.Time     `json:"updatedAt"`
}

// fileUsageCache is the per-profile snapshot (under the profile dir); the global
// state file lives alongside .previous in the profile root.
const fileUsageCache = "usage.json"

func usageStateFile() string { return filepath.Join(paths.ProfileDir, ".usage-state.json") }

// Tunables for the file watcher. The reads are local (no network, no rate limit)
// so the poll cadence is cheap; the staleness bounds keep us from acting on data
// left over from an idle or long-past session.
const (
	watchFileInterval = 5 * time.Second  // how often to re-read the local state file
	stateStaleAfter   = 10 * time.Minute // ignore active-account usage older than this
	cacheStaleAfter   = 6 * time.Hour    // ignore a candidate's cached usage older than this
)

// StatusLine implements the `statusline` command. It reads the JSON Claude Code
// pipes on stdin, prints a one-line usage summary for the status bar, and records
// the active account's usage so an `autoswitch` watcher can react without ever
// touching the network. It is deliberately total: any failure still prints a line
// and returns nil, because Claude Code kills slow or failing status-line commands.
func StatusLine() error {
	// Running interactively with no piped input would block forever on stdin;
	// guide the user to settings.json instead.
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

	// Lead with the account and the current model (when Claude Code reports it).
	head := bold(display)
	if model := usage.ParseStatusLineModel(stdin); model != "" {
		head += "  " + dim(model)
	}

	// Compose the metric segments: conversation context, then the rate-limit
	// windows. Any may be absent depending on Claude Code version / plan.
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

	// Record rate-limit usage for the watcher; only for a saved profile (the
	// watcher keys on profile names) and never fatal to the status line. Context
	// is per-conversation, not per-account, so it is display-only.
	if name != "" && rep != nil && rep.FiveHour != nil {
		recordUsage(usageSnapshot{Account: name, FiveHour: rep.FiveHour, SevenDay: rep.SevenDay, UpdatedAt: time.Now()})
	}
	return nil
}

// recordUsage writes the global state file (for trip detection) and the active
// profile's own cache (for offline candidate ranking). Both are best-effort.
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

// recordProfileUsage writes only a profile's own usage cache (not the global
// state file), used when the online path polls candidate accounts.
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

// AutoSwitchOptions configures the auto-switcher.
type AutoSwitchOptions struct {
	Threshold float64 // percent (0–100) at which to switch; default 90
	Week      bool    // also trip on the 7-day window, not just the 5-hour
	Once      bool    // check once and return instead of looping
	DryRun    bool    // report the decision but do not switch
}

// AutoSwitch watches the active account's rate-limit usage and, when it crosses
// Threshold, switches to the saved profile with the most headroom. It prefers the
// reading the `statusline` sensor records locally (free, never rate-limited) and
// falls back to polling Anthropic's usage endpoint when no fresh sensor data is
// available — e.g. the VSCode panel, which never runs status-line commands. Either
// way the *candidate* accounts are ranked by their current endpoint usage, since
// inactive accounts never run the sensor. It loops until interrupted (Ctrl-C /
// SIGTERM) unless Once is set.
func AutoSwitch(opts AutoSwitchOptions) error {
	if opts.Threshold <= 0 {
		opts.Threshold = 90
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	if !opts.Once {
		fmt.Printf("Watching usage; switching at %.0f%% (%s window). Ctrl-C to stop.\n",
			opts.Threshold, windowLabel(opts.Week))
	}
	var lastSeen, lastOnline time.Time
	for {
		snap, ok := readUsageState()
		// The sensor reading is usable only if it is recent and for the account
		// that is still active; otherwise fall back to the online endpoint.
		fresh := ok && snap.Account == activeProfile() && time.Since(snap.UpdatedAt) <= stateStaleAfter
		switch {
		case fresh && (opts.Once || snap.UpdatedAt.After(lastSeen)):
			lastSeen = snap.UpdatedAt
			evaluateSnapshot(ctx, opts, snap)
		case !fresh && (opts.Once || (proc.ClaudeRunning() && time.Since(lastOnline) >= endpointPollInterval)):
			lastOnline = time.Now()
			evaluateOnline(ctx, opts)
		}
		if opts.Once {
			return nil
		}
		select {
		case <-ctx.Done():
			fmt.Println("\nStopped.")
			return nil
		case <-time.After(watchFileInterval):
		}
	}
}

func windowLabel(week bool) string {
	if week {
		return "5h or 7d"
	}
	return "5h"
}

// evaluateSnapshot logs a sensor-recorded snapshot and switches if it trips the
// threshold. The caller has already established the snapshot is fresh and for the
// still-active account, so this only re-checks the threshold and acts on it.
func evaluateSnapshot(ctx context.Context, opts AutoSwitchOptions, snap usageSnapshot) {
	five, seven := windowPct(snap.FiveHour), windowPct(snap.SevenDay)
	fmt.Printf("[%s] %-16s 5h %3.0f%%  7d %3.0f%%\n", snap.UpdatedAt.Local().Format("15:04:05"), snap.Account, five, seven)
	tripAndSwitch(ctx, opts, snap.Account, five, seven)
}

// tripAndSwitch is the shared decision shared by the sensor and online paths:
// if the active account's usage crosses the threshold, pick the candidate with
// the most current headroom (ranked from live endpoint usage) and switch to it.
func tripAndSwitch(ctx context.Context, opts AutoSwitchOptions, active string, five, seven float64) {
	if !(five >= opts.Threshold || (opts.Week && seven >= opts.Threshold)) {
		return
	}
	target, reason, err := chooseTargetOnline(ctx, active, opts.Threshold)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  threshold reached but cannot switch: %v\n", err)
		return
	}
	if opts.DryRun {
		fmt.Printf("  threshold reached (%.0f%%); would switch to %q (%s) [dry-run]\n", five, target, reason)
		return
	}
	fmt.Printf("  threshold reached (%.0f%%); switching to %q (%s)\n", five, target, reason)
	if err := Switch(target); err != nil {
		fmt.Fprintf(os.Stderr, "  switching to %q: %v\n", target, err)
	}
}

type candidate struct {
	name  string
	five  float64
	known bool // false when no usage (live or cached) was available for this profile
}

// pickCandidate ranks scored candidates: one with a known 5-hour reading below
// the threshold wins (lowest first); profiles with no usable reading are a last
// resort, since switching into one refreshes its token anyway.
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
