package profile

import (
	"fmt"
	"strconv"
	"time"

	"github.com/Samseys/anthropic-account-switcher/internal/paths"
	"github.com/Samseys/anthropic-account-switcher/internal/usage"
)

const noSensorData = "No usage recorded yet. Run 'acc-claude statusline --install', then let Claude\n" +
	"Code emit at least one message."

// humanReset renders a window's reset as "in 2h14m (07:00)", or "" when nil.
func humanReset(w *usage.Window) string {
	if w == nil || w.ResetsAt.IsZero() {
		return ""
	}
	d := time.Until(w.ResetsAt)
	if d < 0 {
		return "now"
	}
	return fmt.Sprintf("in %s (%s)", roundDur(d), w.ResetsAt.Local().Format("15:04"))
}

// roundDur formats d as a human string: minutes when under a day, else whole hours;
// trailing zero units are dropped.
func roundDur(d time.Duration) string {
	if d >= 24*time.Hour {
		return fmt.Sprintf("%dd%dh", int(d/(24*time.Hour)), int(d%(24*time.Hour)/time.Hour))
	}
	d = d.Round(time.Minute)
	h := d / time.Hour
	m := d % time.Hour / time.Minute
	if h == 0 {
		return fmt.Sprintf("%dm", m)
	}
	return fmt.Sprintf("%dh%dm", h, m)
}

// humanAgo returns e.g. "3m ago" or "just now".
func humanAgo(t time.Time) string {
	d := time.Since(t)
	if d < time.Minute {
		return "just now"
	}
	return roundDur(d) + " ago"
}

func windowPct(w *usage.Window) float64 {
	if w == nil {
		return 0
	}
	return w.Utilization
}

// Usage prints the last-known usage the status-line sensor recorded — for the
// active account, or every saved profile with all set.
func Usage(asJSON, all bool) error {
	// Bring the active account's reading up to date when the sensor has not kept it
	// warm (the VSCode panel). Best-effort and throttled; reads below are local.
	refreshActiveUsageIfStale(false)
	if all {
		return usageAll(asJSON)
	}
	return usageActive(asJSON)
}

type usageJSON struct {
	Profile   string        `json:"profile,omitempty"`
	Active    bool          `json:"active"`
	FiveHour  *usage.Window `json:"fiveHour,omitempty"`
	SevenDay  *usage.Window `json:"sevenDay,omitempty"`
	UpdatedAt *time.Time    `json:"updatedAt,omitempty"`
	Note      string        `json:"note,omitempty"`
}

func usageActive(asJSON bool) error {
	snap, ok := readUsageState()
	if !ok {
		if asJSON {
			return paths.PrintJSON(usageJSON{Note: "no usage recorded yet"})
		}
		fmt.Println(noSensorData)
		return nil
	}
	if asJSON {
		t := snap.UpdatedAt
		return paths.PrintJSON(usageJSON{
			Profile: snap.Account, Active: true,
			FiveHour: snap.FiveHour, SevenDay: snap.SevenDay, UpdatedAt: &t,
		})
	}
	on := usageColorOn()
	fmt.Printf("Usage for the active account (%s):\n\n", snap.Account)
	printUsageWindow("5-hour", snap.FiveHour, on)
	printUsageWindow("7-day", snap.SevenDay, on)
	fmt.Printf("\n  %s\n", dimIf(on, "recorded "+humanAgo(snap.UpdatedAt)))
	return nil
}

// printUsageWindow renders one labelled window as "label <bar> pct%  resets ...".
func printUsageWindow(label string, w *usage.Window, on bool) {
	meter, percent := coloredMeter(w, 8, on)
	reset := humanReset(w)
	if reset != "" {
		reset = "resets " + reset
	}
	fmt.Printf("  %-7s %s %s   %s\n", label, meter, percent, dimIf(on, reset))
}

func usageAll(asJSON bool) error {
	names := profileNames()
	if len(names) == 0 {
		if asJSON {
			return paths.PrintJSON([]usageJSON{})
		}
		fmt.Printf("No saved profiles yet. Run '%s save' to store the current account.\n", paths.Bin)
		return nil
	}
	active := activeProfile()

	var rows []usageJSON
	for _, name := range names {
		row := usageJSON{Profile: name, Active: name == active}
		if snap, ok := readProfileUsageCache(name); ok {
			t := snap.UpdatedAt
			row.FiveHour, row.SevenDay, row.UpdatedAt = snap.FiveHour, snap.SevenDay, &t
		} else {
			row.Note = "no usage recorded yet"
		}
		rows = append(rows, row)
	}

	if asJSON {
		return paths.PrintJSON(rows)
	}

	maxName := 0
	for _, n := range names {
		maxName = max(maxName, len(n))
	}
	on := usageColorOn()
	fmt.Println("Usage by profile (last recorded reading):")
	fmt.Println()
	for _, r := range rows {
		mark := "  "
		if r.Active {
			mark = "* "
		}
		if r.UpdatedAt == nil {
			fmt.Printf("  %s%-*s   %s\n", mark, maxName, r.Profile, dimIf(on, r.Note))
			continue
		}
		m5, p5 := coloredMeter(r.FiveHour, 8, on)
		m7, p7 := coloredMeter(r.SevenDay, 8, on)
		fmt.Printf("  %s%-*s   %s %s %s   %s %s %s   %s\n",
			mark, maxName, r.Profile,
			dimIf(on, "5h"), m5, p5, dimIf(on, "7d"), m7, p7,
			dimIf(on, "("+humanAgo(*r.UpdatedAt)+")"))
	}
	fmt.Println()
	fmt.Println("* = currently active account")
	return nil
}

func parseThreshold(arg string) (float64, error) {
	if arg == "" {
		return 0, nil // signals "use default"
	}
	arg = trimPercent(arg)
	v, err := strconv.ParseFloat(arg, 64)
	if err != nil || v <= 0 || v > 100 {
		return 0, fmt.Errorf("threshold must be a number between 1 and 100, got %q", arg)
	}
	return v, nil
}

func trimPercent(s string) string {
	if n := len(s); n > 0 && s[n-1] == '%' {
		return s[:n-1]
	}
	return s
}
