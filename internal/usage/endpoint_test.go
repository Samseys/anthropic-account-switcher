package usage

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestFetchSendsClaudeCodeUAAndParses(t *testing.T) {
	var gotUA, gotAuth, gotBeta string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA, gotAuth, gotBeta = r.Header.Get("User-Agent"), r.Header.Get("Authorization"), r.Header.Get("Anthropic-Beta")
		w.Write([]byte(`{"five_hour":{"utilization":62,"resets_at":"2026-06-13T07:00:00Z"},"seven_day":{"utilization":29,"resets_at":"2026-06-19T00:00:00Z"}}`))
	}))
	defer srv.Close()
	old := usageURL
	usageURL = srv.URL
	defer func() { usageURL = old }()

	rep, err := Fetch(context.Background(), "tok-abc")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if !strings.HasPrefix(gotUA, "claude-code/") {
		t.Errorf("User-Agent = %q, must start with claude-code/", gotUA)
	}
	if gotAuth != "Bearer tok-abc" {
		t.Errorf("Authorization = %q", gotAuth)
	}
	if gotBeta != oauthBeta {
		t.Errorf("Anthropic-Beta = %q, want %q", gotBeta, oauthBeta)
	}
	if rep.FiveHour == nil || rep.FiveHour.Utilization != 62 {
		t.Errorf("five_hour = %+v, want 62", rep.FiveHour)
	}
	if !rep.FiveHour.ResetsAt.Equal(time.Date(2026, 6, 13, 7, 0, 0, 0, time.UTC)) {
		t.Errorf("five_hour resets_at = %v", rep.FiveHour.ResetsAt)
	}
}

func TestFetchStatusErrors(t *testing.T) {
	for _, tc := range []struct {
		code int
		want error
	}{
		{http.StatusTooManyRequests, ErrRateLimited},
		{http.StatusUnauthorized, ErrUnauthorized},
		{http.StatusForbidden, ErrUnauthorized},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(tc.code)
		}))
		old := usageURL
		usageURL = srv.URL
		_, err := Fetch(context.Background(), "tok")
		usageURL = old
		srv.Close()
		if err != tc.want {
			t.Errorf("HTTP %d → %v, want %v", tc.code, err, tc.want)
		}
	}
}

func TestRefreshTokenFormAndParse(t *testing.T) {
	var gotGrant, gotRefresh, gotClient, gotCT string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotGrant, gotRefresh, gotClient = r.Form.Get("grant_type"), r.Form.Get("refresh_token"), r.Form.Get("client_id")
		gotCT = r.Header.Get("Content-Type")
		w.Write([]byte(`{"access_token":"new-access","refresh_token":"new-refresh","expires_in":3600}`))
	}))
	defer srv.Close()
	old := tokenURL
	tokenURL = srv.URL
	defer func() { tokenURL = old }()

	tok, err := RefreshToken(context.Background(), "old-refresh")
	if err != nil {
		t.Fatalf("RefreshToken: %v", err)
	}
	if gotGrant != "refresh_token" || gotRefresh != "old-refresh" || gotClient != oauthClientID {
		t.Errorf("form: grant=%q refresh=%q client=%q", gotGrant, gotRefresh, gotClient)
	}
	if !strings.Contains(gotCT, "x-www-form-urlencoded") {
		t.Errorf("Content-Type = %q", gotCT)
	}
	if tok.AccessToken != "new-access" || tok.RefreshToken != "new-refresh" {
		t.Errorf("token = %+v", tok)
	}
	if tok.ExpiresAt.Before(time.Now().Add(50 * time.Minute)) {
		t.Errorf("ExpiresAt not ~1h out: %v", tok.ExpiresAt)
	}
}

func TestRefreshTokenInvalidGrant(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":"invalid_grant"}`))
	}))
	defer srv.Close()
	old := tokenURL
	tokenURL = srv.URL
	defer func() { tokenURL = old }()

	if _, err := RefreshToken(context.Background(), "revoked"); err != ErrRefreshRejected {
		t.Errorf("err = %v, want ErrRefreshRejected", err)
	}
}

func TestParseCredentialsAndWriteBack(t *testing.T) {
	creds := []byte(`{"claudeAiOauth":{"accessToken":"a","refreshToken":"r","expiresAt":1700000000000,"subscriptionType":"max"},"other":"keep"}`)
	c, err := ParseCredentials(creds)
	if err != nil {
		t.Fatalf("ParseCredentials: %v", err)
	}
	if c.AccessToken != "a" || c.RefreshToken != "r" {
		t.Errorf("creds = %+v", c)
	}
	if !c.ExpiresAt.Equal(time.UnixMilli(1700000000000)) {
		t.Errorf("ExpiresAt = %v", c.ExpiresAt)
	}

	// Write back a refreshed token, preserving unrelated fields.
	out, err := WithCredentialTokens(creds, Token{AccessToken: "a2", RefreshToken: "r2", ExpiresAt: time.UnixMilli(1800000000000)})
	if err != nil {
		t.Fatalf("WithCredentialTokens: %v", err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("result not JSON: %v", err)
	}
	if _, ok := m["other"]; !ok {
		t.Error("unrelated top-level field was dropped")
	}
	c2, _ := ParseCredentials(out)
	if c2.AccessToken != "a2" || c2.RefreshToken != "r2" {
		t.Errorf("written creds = %+v", c2)
	}
	if !strings.Contains(string(out), `"subscriptionType":"max"`) {
		t.Errorf("nested oauth field dropped: %s", out)
	}
}
