package usage

import (
	"testing"
	"time"
)

func TestParseStatusLine(t *testing.T) {
	// Unix-timestamp resets_at (the documented status-line form), plus unrelated
	// fields that must be ignored.
	in := `{
		"model": {"display_name": "Opus"},
		"context": {"used_percentage": 42},
		"rate_limits": {
			"five_hour": {"used_percentage": 92, "resets_at": 1765432100},
			"seven_day": {"used_percentage": 61, "resets_at": 1765800000}
		}
	}`
	rep, err := ParseStatusLine([]byte(in))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if rep == nil || rep.FiveHour == nil || rep.FiveHour.Utilization != 92 {
		t.Fatalf("five_hour = %+v, want 92", rep.FiveHour)
	}
	if !rep.FiveHour.ResetsAt.Equal(time.Unix(1765432100, 0)) {
		t.Errorf("five_hour resets_at = %v, want %v", rep.FiveHour.ResetsAt, time.Unix(1765432100, 0))
	}
	if rep.SevenDay == nil || rep.SevenDay.Utilization != 61 {
		t.Errorf("seven_day = %+v, want 61", rep.SevenDay)
	}
}

func TestParseStatusLineModel(t *testing.T) {
	if got := ParseStatusLineModel([]byte(`{"model":{"display_name":"Opus 4.8"}}`)); got != "Opus 4.8" {
		t.Errorf("model = %q, want %q", got, "Opus 4.8")
	}
	if got := ParseStatusLineModel([]byte(`{"rate_limits":{}}`)); got != "" {
		t.Errorf("model = %q, want empty when absent", got)
	}
	if got := ParseStatusLineModel([]byte(`not json`)); got != "" {
		t.Errorf("model = %q, want empty on bad JSON", got)
	}
}

func TestParseStatusLineNoRateLimits(t *testing.T) {
	// Older Claude Code / non-unified plan: no rate_limits block → (nil, nil).
	rep, err := ParseStatusLine([]byte(`{"model": {"display_name": "Opus"}}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if rep != nil {
		t.Errorf("got %+v, want nil report when rate_limits absent", rep)
	}
}

func TestParseResetsAtRFC3339(t *testing.T) {
	// Tolerate the endpoint-style ISO string too, in case the field shape drifts.
	in := `{"rate_limits": {"five_hour": {"used_percentage": 5, "resets_at": "2026-04-11T07:00:00Z"}}}`
	rep, err := ParseStatusLine([]byte(in))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	want := time.Date(2026, 4, 11, 7, 0, 0, 0, time.UTC)
	if !rep.FiveHour.ResetsAt.Equal(want) {
		t.Errorf("resets_at = %v, want %v", rep.FiveHour.ResetsAt, want)
	}
}
