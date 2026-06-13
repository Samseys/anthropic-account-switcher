package profile

import (
	"fmt"
	"os"
	"strings"

	"github.com/Samseys/anthropic-account-switcher/internal/usage"
)

// Status-bar rendering. Claude Code renders ANSI escapes in the status line, so
// we color the usage meters by severity (a traffic light: green/amber/red) to
// make headroom readable at a glance. NO_COLOR disables color but keeps the bars.

const barWidth = 8

func colorEnabled() bool { return os.Getenv("NO_COLOR") == "" }

// ansiIf wraps s in an SGR code (and a reset) when on, else returns it unchanged.
func ansiIf(on bool, code, s string) string {
	if !on {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

// ansi colors for the status line, which always emits ANSI (Claude Code renders
// it) unless NO_COLOR is set.
func ansi(code, s string) string { return ansiIf(colorEnabled(), code, s) }

func bold(s string) string { return ansi("1", s) }
func dim(s string) string  { return ansi("2", s) }

// dimIf is dim, but gated (used by the `usage` command, which only colors a tty).
func dimIf(on bool, s string) string { return ansiIf(on, "2", s) }

// stdoutIsTerminal reports whether stdout is an interactive terminal.
func stdoutIsTerminal() bool {
	fi, err := os.Stdout.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// usageColorOn reports whether the `usage` command should colorize: only on an
// interactive terminal and with NO_COLOR unset. (The status line is different —
// it always emits ANSI, since Claude Code, not a tty, renders its output.)
func usageColorOn() bool { return colorEnabled() && stdoutIsTerminal() }

// coloredPct renders just a window's percent (fixed 4 visible cells, so columns
// align), colored by utilization, or a dash when there is no window.
func coloredPct(w *usage.Window, on bool) string {
	if w == nil {
		return dimIf(on, "  --")
	}
	pct := windowPct(w)
	return ansiIf(on, usageColor(pct), fmt.Sprintf("%3.0f%%", pct))
}

// usageBriefCols is usageBrief's visible width ("5h" + sp + 4 + 2sp + "7d" + sp +
// 4); it lets callers reserve a matching blank column for rows without usage.
const usageBriefCols = 16

// usageBrief is the compact "5h  60%  7d  27%" form used in `list`.
func usageBrief(five, seven *usage.Window, on bool) string {
	return fmt.Sprintf("%s %s  %s %s", dimIf(on, "5h"), coloredPct(five, on), dimIf(on, "7d"), coloredPct(seven, on))
}

// coloredMeter renders a window as a fixed-width bar and a 4-cell percent, both
// colored by utilization when on. The plain text is fixed-width so columns stay
// aligned even though the colored strings carry (zero-width) escape codes.
func coloredMeter(w *usage.Window, width int, on bool) (meter, percent string) {
	if w == nil {
		return dimIf(on, strings.Repeat("░", width)), dimIf(on, "  —")
	}
	pct := windowPct(w)
	col := usageColor(pct)
	return ansiIf(on, col, bar(pct, width)), ansiIf(on, col, fmt.Sprintf("%3.0f%%", pct))
}

// usageColor maps utilization to an SGR color: green < 70, amber < 90, red at or
// above 90 (where autoswitch's default threshold trips).
func usageColor(pct float64) string {
	switch {
	case pct >= 90:
		return "31" // red
	case pct >= 70:
		return "33" // amber
	default:
		return "32" // green
	}
}

// bar renders a width-cell meter, filled proportionally to pct.
func bar(pct float64, width int) string {
	switch {
	case pct < 0:
		pct = 0
	case pct > 100:
		pct = 100
	}
	filled := int(pct/100*float64(width) + 0.5)
	return strings.Repeat("█", filled) + strings.Repeat("░", width-filled)
}

// windowSegment renders "<label> <bar> <pct>%", with the bar and percent colored
// by utilization and the label dimmed.
func windowSegment(label string, w *usage.Window) string {
	return labelledMeter(label, windowPct(w))
}

// labelledMeter is the status-line segment shared by the rate-limit windows and
// the conversation-context readout: a dim label, a colored bar, and a colored
// percent.
func labelledMeter(label string, pct float64) string {
	col := usageColor(pct)
	return fmt.Sprintf("%s %s %s", dim(label), ansi(col, bar(pct, barWidth)), ansi(col, fmt.Sprintf("%.0f%%", pct)))
}
