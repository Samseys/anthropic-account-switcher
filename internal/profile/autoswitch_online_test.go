package profile

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Samseys/anthropic-account-switcher/internal/paths"
	"github.com/Samseys/anthropic-account-switcher/internal/usage"
)

// writeProfileCreds creates a saved profile directory holding the given (plaintext)
// credentials JSON, encrypted at rest the same way `save` would write it.
func writeProfileCreds(t *testing.T, name, credsJSON string) {
	t.Helper()
	dir := profilePath(name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	enc, err := encryptCreds([]byte(credsJSON))
	if err != nil {
		t.Fatal(err)
	}
	if err := paths.WriteFileAtomic(filepath.Join(dir, fileCreds), enc, 0o600); err != nil {
		t.Fatal(err)
	}
}

// usageByToken serves the usage endpoint, returning a per-access-token utilization
// (keyed on the Bearer token) and 401 for anything unmapped.
func usageByToken(util map[string]float64) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		u, ok := util[tok]
		if !ok {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		fmt.Fprintf(w, `{"five_hour":{"utilization":%g,"resets_at":"2026-06-13T07:00:00Z"},"seven_day":{"utilization":1,"resets_at":"2026-06-19T00:00:00Z"}}`, u)
	}))
}

// TestFetchProfileOnlineRefreshesNearExpiry verifies a candidate whose access
// token is near expiry is refreshed, the new token persisted to its snapshot, and
// the usage then fetched with the refreshed token.
func TestFetchProfileOnlineRefreshesNearExpiry(t *testing.T) {
	setupEnv(t)

	// expiresAt in the past → NearExpiry, so a refresh must happen first.
	writeProfileCreds(t, "work", `{"claudeAiOauth":{"accessToken":"old-acc","refreshToken":"old-ref","expiresAt":1000,"subscriptionType":"max"}}`)

	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if got := r.Form.Get("refresh_token"); got != "old-ref" {
			t.Errorf("refresh_token = %q, want old-ref", got)
		}
		w.Write([]byte(`{"access_token":"new-acc","refresh_token":"new-ref","expires_in":3600}`))
	}))
	defer tokenSrv.Close()
	usageSrv := usageByToken(map[string]float64{"new-acc": 42}) // only the refreshed token works
	defer usageSrv.Close()
	defer usage.SetEndpointsForTest(usageSrv.URL, tokenSrv.URL)()

	rep, err := fetchProfileOnline(context.Background(), "work")
	if err != nil {
		t.Fatalf("fetchProfileOnline: %v", err)
	}
	if rep.FiveHour == nil || rep.FiveHour.Utilization != 42 {
		t.Fatalf("five_hour = %+v, want 42", rep.FiveHour)
	}

	// The refreshed token must have been persisted back into the snapshot.
	raw, err := readProfileCreds(profilePath("work"))
	if err != nil {
		t.Fatal(err)
	}
	c, err := usage.ParseCredentials(raw)
	if err != nil {
		t.Fatal(err)
	}
	if c.AccessToken != "new-acc" || c.RefreshToken != "new-ref" {
		t.Errorf("persisted creds = %+v, want new-acc/new-ref", c)
	}
	if !strings.Contains(string(raw), `"subscriptionType":"max"`) {
		t.Errorf("unrelated oauth field dropped on refresh: %s", raw)
	}
}

// TestRefreshUsageOnlineUpdatesInactive verifies `list --refresh` replaces an
// inactive profile's cached usage with a live endpoint reading and writes it back.
func TestRefreshUsageOnlineUpdatesInactive(t *testing.T) {
	setupEnv(t)

	const future = `4102444800000`
	writeProfileCreds(t, "alpha", `{"claudeAiOauth":{"accessToken":"acc-alpha","refreshToken":"r","expiresAt":`+future+`}}`)

	usageSrv := usageByToken(map[string]float64{"acc-alpha": 55})
	defer usageSrv.Close()
	defer usage.SetEndpointsForTest(usageSrv.URL, "")()

	infos := []profileInfo{{Name: "alpha"}}
	refreshUsageOnline(infos, true)

	if infos[0].FiveHour == nil || infos[0].FiveHour.Utilization != 55 {
		t.Fatalf("five_hour = %+v, want 55", infos[0].FiveHour)
	}
	if infos[0].UsageRecordedAt == nil {
		t.Error("UsageRecordedAt not set after refresh")
	}
	if snap, ok := readProfileUsageCache("alpha"); !ok || windowPct(snap.FiveHour) != 55 {
		t.Errorf("cache = %+v (ok=%v), want 55", snap.FiveHour, ok)
	}
}

// TestChooseTargetOnlineRanksByLiveUsage verifies candidates are polled live and
// the one with the lowest current 5-hour usage wins, with its reading cached.
func TestChooseTargetOnlineRanksByLiveUsage(t *testing.T) {
	setupEnv(t)

	const future = `4102444800000` // ~year 2100, so no refresh is triggered
	writeProfileCreds(t, "alpha", `{"claudeAiOauth":{"accessToken":"acc-alpha","refreshToken":"r","expiresAt":`+future+`}}`)
	writeProfileCreds(t, "beta", `{"claudeAiOauth":{"accessToken":"acc-beta","refreshToken":"r","expiresAt":`+future+`}}`)

	usageSrv := usageByToken(map[string]float64{"acc-alpha": 80, "acc-beta": 20})
	defer usageSrv.Close()
	defer usage.SetEndpointsForTest(usageSrv.URL, "")()

	name, _, err := chooseTargetOnline(context.Background(), "work", 90)
	if err != nil {
		t.Fatalf("chooseTargetOnline: %v", err)
	}
	if name != "beta" {
		t.Fatalf("target = %q, want beta (lower live usage)", name)
	}

	// Live readings should have been written to each candidate's cache.
	if snap, ok := readProfileUsageCache("beta"); !ok || windowPct(snap.FiveHour) != 20 {
		t.Errorf("beta cache = %+v (ok=%v), want 20", snap.FiveHour, ok)
	}
	if snap, ok := readProfileUsageCache("alpha"); !ok || windowPct(snap.FiveHour) != 80 {
		t.Errorf("alpha cache = %+v (ok=%v), want 80", snap.FiveHour, ok)
	}
}
