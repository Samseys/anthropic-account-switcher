package profile

import (
	"fmt"
	"os"
	"strings"

	"github.com/Samseys/anthropic-account-switcher/internal/usage"
)

// Status-bar rendering. Claude Code renders ANSI escapes in the status line;
// meters are colored by severity (green/amber/red). NO_COLOR disables color but keeps bars.

const barWidth = 8

func colorEnabled() bool { return os.Getenv("NO_COLOR") == "" }

func ansiIf(on bool, code, s string) string {
	if !on {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

// ansi always emits ANSI (Claude Code renders it), unless NO_COLOR is set.
func ansi(code, s string) string { return ansiIf(colorEnabled(), code, s) }

func bold(s string) string { return ansi("1", s) }
func dim(s string) string  { return ansi("2", s) }

// dimIf is dim, gated on a flag (the `usage` command only colors a tty).
func dimIf(on bool, s string) string { return ansiIf(on, "2", s) }

func stdoutIsTerminal() bool {
	fi, err := os.Stdout.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// usageColorOn gates color for the `usage` command (tty + NO_COLOR unset).
// The status line always emits ANSI regardless — Claude Code, not a tty, renders it.
func usageColorOn() bool { return colorEnabled() && stdoutIsTerminal() }

// coloredPct renders a window's percent in 4 fixed cells (column-safe), colored
// by utilization, or a dash when there is no window.
func coloredPct(w *usage.Window, on bool) string {
	if w == nil {
		return dimIf(on, "  --")
	}
	pct := windowPct(w)
	return ansiIf(on, usageColor(pct), fmt.Sprintf("%3.0f%%", pct))
}

// usageBriefCols is usageBrief's visible width; callers reserve a matching blank
// column for rows without usage to keep trailing fields aligned.
const usageBriefCols = 16

func usageBrief(five, seven *usage.Window, on bool) string {
	return fmt.Sprintf("%s %s  %s %s", dimIf(on, "5h"), coloredPct(five, on), dimIf(on, "7d"), coloredPct(seven, on))
}

// coloredMeter renders a window as a fixed-width bar + 4-cell percent.
// Plain text is fixed-width so columns align even though ANSI escapes are zero-width.
func coloredMeter(w *usage.Window, width int, on bool) (meter, percent string) {
	if w == nil {
		return dimIf(on, strings.Repeat("░", width)), dimIf(on, "  —")
	}
	pct := windowPct(w)
	col := usageColor(pct)
	return ansiIf(on, col, bar(pct, width)), ansiIf(on, col, fmt.Sprintf("%3.0f%%", pct))
}

// usageColor maps utilization to an SGR color: green < 70, amber < 90, red >= 90
// (where autoswitch's default threshold trips).
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

func windowSegment(label string, w *usage.Window) string {
	return labelledMeter(label, windowPct(w))
}

// labelledMeter renders a dim label, colored bar, and colored percent.
// Shared by rate-limit windows and the conversation-context readout.
func labelledMeter(label string, pct float64) string {
	col := usageColor(pct)
	return fmt.Sprintf("%s %s %s", dim(label), ansi(col, bar(pct, barWidth)), ansi(col, fmt.Sprintf("%.0f%%", pct)))
}
