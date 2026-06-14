package profile

import (
	"testing"
	"time"
)

// TestDecideWatchAction covers the watch loop's branch decision, with emphasis on
// the CLI↔panel handoff: once the status-line sensor goes quiet (you closed the
// CLI and moved to the VSCode panel, which never runs it), the watcher must fall
// back to online polling instead of stalling on the last sensor reading.
func TestDecideWatchAction(t *testing.T) {
	const active = "personal"
	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	snap := func(account string, age time.Duration) usageSnapshot {
		return usageSnapshot{Account: account, UpdatedAt: now.Add(-age)}
	}

	cases := []struct {
		name          string
		snap          usageSnapshot
		ok            bool
		lastSeen      time.Time
		lastOnline    time.Time
		onlineBackoff time.Time
		claudeRunning bool
		once          bool
		want          watchAction
	}{
		{
			name: "fresh sensor reading, not yet seen -> snapshot",
			snap: snap(active, 30*time.Second), ok: true,
			lastSeen: now.Add(-5 * time.Minute), want: actSnapshot,
		},
		{
			name: "fresh sensor reading already processed -> none",
			snap: snap(active, 30*time.Second), ok: true,
			lastSeen: now.Add(-30 * time.Second), want: actNone,
		},
		{
			// The handoff: CLI closed, sensor quiet >2min, VSCode (claude) running.
			// The old 10min window left this idle; now it polls online.
			name: "sensor quiet, claude running, poll due -> online",
			snap: snap(active, 5*time.Minute), ok: true,
			lastOnline: now.Add(-2 * time.Minute), claudeRunning: true, want: actOnline,
		},
		{
			name: "sensor quiet but claude not running -> none",
			snap: snap(active, 5*time.Minute), ok: true,
			lastOnline: now.Add(-2 * time.Minute), claudeRunning: false, want: actNone,
		},
		{
			name: "sensor quiet, claude running, but polled too recently -> none",
			snap: snap(active, 5*time.Minute), ok: true,
			lastOnline: now.Add(-30 * time.Second), claudeRunning: true, want: actNone,
		},
		{
			name: "poll due but within a 429 backoff window -> none",
			snap: snap(active, 5*time.Minute), ok: true,
			lastOnline: now.Add(-2 * time.Minute), onlineBackoff: now.Add(2 * time.Minute),
			claudeRunning: true, want: actNone,
		},
		{
			name: "reading is for a different account -> treated as quiet -> online",
			snap: snap("ducknet", 30*time.Second), ok: true,
			lastOnline: now.Add(-2 * time.Minute), claudeRunning: true, want: actOnline,
		},
		{
			name: "no state file, claude running -> online",
			snap: usageSnapshot{}, ok: false,
			lastOnline: now.Add(-2 * time.Minute), claudeRunning: true, want: actOnline,
		},
		{
			// The watcher's own online poll lands in the shared state file; it
			// must not be re-read as a sensor reading (that double-reported it).
			name: "recent online snapshot -> not a sensor reading -> online",
			snap: usageSnapshot{Account: active, UpdatedAt: now.Add(-30 * time.Second), Online: true}, ok: true,
			lastOnline: now.Add(-2 * time.Minute), claudeRunning: true, want: actOnline,
		},
		{
			name: "recent online snapshot, polled too recently -> none",
			snap: usageSnapshot{Account: active, UpdatedAt: now.Add(-30 * time.Second), Online: true}, ok: true,
			lastOnline: now.Add(-30 * time.Second), claudeRunning: true, want: actNone,
		},
		{
			name: "once forces snapshot even when already seen",
			snap: snap(active, 30*time.Second), ok: true,
			lastSeen: now.Add(-30 * time.Second), once: true, want: actSnapshot,
		},
		{
			name: "once forces online when quiet, regardless of claude running",
			snap: snap(active, 5*time.Minute), ok: true,
			claudeRunning: false, once: true, want: actOnline,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := decideWatchAction(tc.snap, tc.ok, active, now, tc.lastSeen, tc.lastOnline, tc.onlineBackoff, tc.claudeRunning, tc.once)
			if got != tc.want {
				t.Errorf("decideWatchAction = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestThresholdOverrides covers how the positional thresholds map onto each
// source: none keeps the per-source defaults, one value covers both windows, two
// set 5h then 7d — and an override always spans both sources.
func TestThresholdOverrides(t *testing.T) {
	cases := []struct {
		name                           string
		a, b                           string
		wantS5, wantS7, wantO5, wantO7 float64
	}{
		{"defaults", "", "", 99, 99, 95, 98},
		{"one value both windows", "75", "", 75, 75, 75, 75},
		{"two values split windows", "90", "96", 90, 96, 90, 96},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			five, seven, err := parseThresholds(tc.a, tc.b)
			if err != nil {
				t.Fatalf("parseThresholds(%q,%q): %v", tc.a, tc.b, err)
			}
			opts := AutoSwitchOptions{FiveHour: five, SevenDay: seven}
			s, o := opts.sensor(), opts.online()
			if s.fiveHour != tc.wantS5 || s.sevenDay != tc.wantS7 {
				t.Errorf("sensor = %v/%v, want %v/%v", s.fiveHour, s.sevenDay, tc.wantS5, tc.wantS7)
			}
			if o.fiveHour != tc.wantO5 || o.sevenDay != tc.wantO7 {
				t.Errorf("online = %v/%v, want %v/%v", o.fiveHour, o.sevenDay, tc.wantO5, tc.wantO7)
			}
		})
	}
}
