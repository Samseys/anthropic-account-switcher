package usage

import (
	"encoding/json"
	"fmt"
	"time"
)

// ParseStatusLine extracts rate-limit usage from the JSON Claude Code pipes to a
// status-line command on stdin. This is a *different* schema from the OAuth usage
// endpoint (Fetch): here percentages are `used_percentage` and a window's reset
// is `resets_at` as a Unix timestamp. It returns (nil, nil) when the payload
// carries no rate_limits block — older Claude Code, or a plan that does not
// surface unified limits — so callers can degrade gracefully rather than error.
//
// Claude Code >= 2.1.x emits rate_limits for Pro/Max subscribers; treat every
// field as optional, since the shape is an evolving product surface.
func ParseStatusLine(stdin []byte) (*Report, error) {
	var sl struct {
		RateLimits *struct {
			FiveHour *slWindow `json:"five_hour"`
			SevenDay *slWindow `json:"seven_day"`
		} `json:"rate_limits"`
	}
	if err := json.Unmarshal(stdin, &sl); err != nil {
		return nil, fmt.Errorf("parsing status-line JSON: %w", err)
	}
	if sl.RateLimits == nil {
		return nil, nil
	}
	return &Report{
		FiveHour: sl.RateLimits.FiveHour.toWindow(),
		SevenDay: sl.RateLimits.SevenDay.toWindow(),
	}, nil
}

// ParseStatusLineContext extracts the conversation's context-window utilization
// (0–100) from the status-line stdin payload, reporting false when absent.
// Claude Code pre-computes this as context_window.used_percentage.
func ParseStatusLineContext(stdin []byte) (float64, bool) {
	var sl struct {
		ContextWindow *struct {
			UsedPercentage *float64 `json:"used_percentage"`
		} `json:"context_window"`
	}
	if err := json.Unmarshal(stdin, &sl); err != nil {
		return 0, false
	}
	if sl.ContextWindow == nil || sl.ContextWindow.UsedPercentage == nil {
		return 0, false
	}
	return *sl.ContextWindow.UsedPercentage, true
}

// ParseStatusLineModel extracts the current model's display name from the same
// status-line stdin payload, or "" if absent.
func ParseStatusLineModel(stdin []byte) string {
	var sl struct {
		Model struct {
			DisplayName string `json:"display_name"`
		} `json:"model"`
	}
	if err := json.Unmarshal(stdin, &sl); err != nil {
		return ""
	}
	return sl.Model.DisplayName
}

type slWindow struct {
	UsedPercentage float64         `json:"used_percentage"`
	ResetsAt       json.RawMessage `json:"resets_at"`
}

func (w *slWindow) toWindow() *Window {
	if w == nil {
		return nil
	}
	return &Window{Utilization: w.UsedPercentage, ResetsAt: parseResetsAt(w.ResetsAt)}
}

// parseResetsAt tolerates either a Unix timestamp in seconds (the status-line
// form) or an RFC3339 string, returning the zero time when neither parses.
func parseResetsAt(raw json.RawMessage) time.Time {
	if len(raw) == 0 || string(raw) == "null" {
		return time.Time{}
	}
	var secs int64
	if err := json.Unmarshal(raw, &secs); err == nil {
		return time.Unix(secs, 0)
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			return t
		}
	}
	return time.Time{}
}
