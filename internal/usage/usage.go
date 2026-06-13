// Package usage models Claude Code's rate-limit usage and reads it from two
// sources, offline-first:
//
//   - statusline.go (primary): parses the usage Claude Code hands to a status-line
//     command on stdin — free on every message for Pro/Max subscribers, and never
//     rate-limited. This is how the terminal CLI learns usage.
//   - endpoint.go (fallback): calls Anthropic's `/api/oauth/usage` endpoint
//     directly, for contexts where the status line never runs (chiefly the VSCode
//     panel) and for polling *inactive* accounts that never feed the status line.
//     The endpoint is aggressively rate-limited, so callers must cache and poll no
//     faster than MinInterval.
package usage

import "time"

// Window is one rate-limit window's consumption: a 0–100 utilization percentage
// and when the window rolls over. A nil *Window means no active window.
type Window struct {
	Utilization float64   `json:"utilization"`
	ResetsAt    time.Time `json:"resetsAt"`
}

// Report is a snapshot of the rate-limit windows Claude Code surfaces.
type Report struct {
	FiveHour *Window `json:"fiveHour"`
	SevenDay *Window `json:"sevenDay"`
}
