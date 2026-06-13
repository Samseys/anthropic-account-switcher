package usage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// This file is the **online** fallback: when no fresh status-line reading is
// available (e.g. the VSCode extension panel, which never runs the status-line
// sensor), the auto-switcher polls Anthropic's OAuth usage endpoint directly —
// the same approach Claude Code's /usage and tools like aistat use.
//
// Two things are load-bearing, both reverse-engineered and confirmed by aistat:
//   - User-Agent: claude-code/<ver>  — other UAs hit an aggressively
//     rate-limited bucket. Override with ACC_CLAUDE_USER_AGENT.
//   - the OAuth client_id for token refresh.
//
// The endpoint is still rate-limited, so callers must cache and poll slowly.

// Overridable in tests; vars (not consts) so httptest can redirect them.
var (
	usageURL = "https://api.anthropic.com/api/oauth/usage"
	// Token refresh must hit api.anthropic.com, NOT platform.claude.com: the
	// latter sits behind an edge rule that returns a canned rate_limit_error
	// (HTTP 429, no request-id) to every caller regardless of credentials,
	// protocol, headers, or TLS fingerprint. api.anthropic.com reaches the real
	// OAuth app (a bad token there gets a proper invalid_grant with a request-id).
	tokenURL = "https://api.anthropic.com/v1/oauth/token"
)

// SetEndpointsForTest points the usage and token URLs at test servers and returns
// a restore func. Allows packages that wrap Fetch/RefreshToken to drive them
// against an httptest server.
func SetEndpointsForTest(usage, token string) func() {
	oldUsage, oldToken := usageURL, tokenURL
	usageURL, tokenURL = usage, token
	return func() { usageURL, tokenURL = oldUsage, oldToken }
}

const (
	oauthBeta = "oauth-2025-04-20"
	// oauthClientID is the Claude Code OAuth client_id, required by the token
	// endpoint for refresh (discovered from the Claude Code binary; see aistat).
	oauthClientID = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"

	// defaultClaudeCodeVersion is advertised in the User-Agent. The bucket keys
	// on the "claude-code/" prefix + a valid semver, not the exact number.
	defaultClaudeCodeVersion = "2.1.0"

	// MinInterval is the floor between endpoint polls; callers must cache at
	// least this long per account.
	MinInterval = 90 * time.Second
)

var (
	ErrRateLimited     = errors.New("usage endpoint rate-limited (HTTP 429); back off")
	// ErrUnauthorized is returned on 401/403 — usually a lapsed access token.
	ErrUnauthorized    = errors.New("usage request unauthorized (access token expired or invalid)")
	ErrRefreshRejected = errors.New("refresh token rejected; log in to this account again")
)

func userAgent() string {
	if v := os.Getenv("ACC_CLAUDE_USER_AGENT"); v != "" {
		return v
	}
	return "claude-code/" + defaultClaudeCodeVersion
}

// rateLimited wraps ErrRateLimited with the server's reset window from standard
// rate-limit headers. Anthropic sends a unified reset as an epoch second;
// Retry-After (delta seconds or HTTP date) is honored as a fallback.
func rateLimited(h http.Header) error {
	if v := h.Get("Anthropic-Ratelimit-Unified-Reset"); v != "" {
		if sec, err := strconv.ParseInt(v, 10, 64); err == nil {
			if d := time.Until(time.Unix(sec, 0)); d > 0 {
				return fmt.Errorf("%w (resets in ~%s)", ErrRateLimited, d.Round(time.Second))
			}
		}
	}
	if v := h.Get("Retry-After"); v != "" {
		if sec, err := strconv.Atoi(v); err == nil {
			return fmt.Errorf("%w (retry after %ds)", ErrRateLimited, sec)
		}
		if t, err := http.ParseTime(v); err == nil {
			if d := time.Until(t); d > 0 {
				return fmt.Errorf("%w (retry after ~%s)", ErrRateLimited, d.Round(time.Second))
			}
		}
	}
	return ErrRateLimited
}

var httpClient = &http.Client{Timeout: 15 * time.Second}

func Fetch(ctx context.Context, accessToken string) (*Report, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, usageURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Anthropic-Beta", oauthBeta)
	req.Header.Set("User-Agent", userAgent())
	req.Header.Set("Accept", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	switch resp.StatusCode {
	case http.StatusOK:
		return parseEndpointReport(body)
	case http.StatusUnauthorized, http.StatusForbidden:
		return nil, ErrUnauthorized
	case http.StatusTooManyRequests:
		return nil, rateLimited(resp.Header)
	default:
		return nil, fmt.Errorf("usage request failed: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
}

// endpointWindow is the usage endpoint's per-window shape: `utilization` and an
// RFC3339 `resets_at` — distinct from the status-line schema (used_percentage +
// a Unix resets_at), which is why this parse is separate from ParseStatusLine.
type endpointWindow struct {
	Utilization float64 `json:"utilization"`
	ResetsAt    string  `json:"resets_at"`
}

func (w *endpointWindow) toWindow() *Window {
	if w == nil {
		return nil
	}
	t, _ := time.Parse(time.RFC3339, w.ResetsAt)
	return &Window{Utilization: w.Utilization, ResetsAt: t}
}

func parseEndpointReport(body []byte) (*Report, error) {
	var raw struct {
		FiveHour *endpointWindow `json:"five_hour"`
		SevenDay *endpointWindow `json:"seven_day"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("parsing usage response: %w", err)
	}
	return &Report{FiveHour: raw.FiveHour.toWindow(), SevenDay: raw.SevenDay.toWindow()}, nil
}

type Token struct {
	AccessToken  string
	RefreshToken string    // the rotated token, or the original if unchanged
	ExpiresAt    time.Time // zero if the server omitted expires_in
}

func RefreshToken(ctx context.Context, refreshToken string) (Token, error) {
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"client_id":     {oauthClientID},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return Token{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", userAgent())
	req.Header.Set("Accept", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return Token{}, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(body, &e)
		if e.Error == "invalid_grant" {
			return Token{}, ErrRefreshRejected
		}
		// A genuine rate limit from the OAuth app: surface it as ErrRateLimited
		// (with any reset window) so callers back off instead of re-hammering the
		// endpoint and printing the raw 429 body.
		if resp.StatusCode == http.StatusTooManyRequests {
			return Token{}, rateLimited(resp.Header)
		}
		return Token{}, fmt.Errorf("token refresh failed: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var wire struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &wire); err != nil {
		return Token{}, fmt.Errorf("parsing token response: %w", err)
	}
	if wire.AccessToken == "" {
		return Token{}, fmt.Errorf("token refresh returned no access_token")
	}
	tok := Token{AccessToken: wire.AccessToken, RefreshToken: wire.RefreshToken}
	if tok.RefreshToken == "" {
		tok.RefreshToken = refreshToken // server did not rotate it
	}
	if wire.ExpiresIn > 0 {
		tok.ExpiresAt = time.Now().Add(time.Duration(wire.ExpiresIn) * time.Second)
	}
	return tok, nil
}

// Credentials holds the OAuth tokens extracted from a credentials blob.
type Credentials struct {
	AccessToken  string
	RefreshToken string
	ExpiresAt    time.Time // zero when unknown
}

// NearExpiry reports whether the access token is within skew of expiring (or has
// no known expiry), meaning it should be refreshed before use.
func (c Credentials) NearExpiry(skew time.Duration) bool {
	return c.ExpiresAt.IsZero() || time.Now().Add(skew).After(c.ExpiresAt)
}

// ParseCredentials extracts the OAuth tokens from a credentials blob (live or a
// decrypted profile snapshot).
func ParseCredentials(creds []byte) (Credentials, error) {
	var c struct {
		ClaudeAiOauth struct {
			AccessToken  string `json:"accessToken"`
			RefreshToken string `json:"refreshToken"`
			ExpiresAt    int64  `json:"expiresAt"` // ms since epoch
		} `json:"claudeAiOauth"`
	}
	if err := json.Unmarshal(creds, &c); err != nil {
		return Credentials{}, fmt.Errorf("parsing credentials: %w", err)
	}
	o := c.ClaudeAiOauth
	if o.AccessToken == "" {
		return Credentials{}, fmt.Errorf("no OAuth access token in credentials")
	}
	cr := Credentials{AccessToken: o.AccessToken, RefreshToken: o.RefreshToken}
	if o.ExpiresAt > 0 {
		cr.ExpiresAt = time.UnixMilli(o.ExpiresAt)
	}
	return cr, nil
}

// WithCredentialTokens returns creds with the access/refresh tokens and expiry
// replaced by tok, preserving every other field byte-for-reasonable-shape. Used
// to persist a refreshed token back into a credentials blob.
func WithCredentialTokens(creds []byte, tok Token) ([]byte, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(creds, &m); err != nil {
		return nil, fmt.Errorf("parsing credentials: %w", err)
	}
	var oauth map[string]json.RawMessage
	if raw, ok := m["claudeAiOauth"]; ok {
		_ = json.Unmarshal(raw, &oauth)
	}
	if oauth == nil {
		oauth = map[string]json.RawMessage{}
	}
	set := func(key string, val any) {
		b, _ := json.Marshal(val)
		oauth[key] = b
	}
	set("accessToken", tok.AccessToken)
	set("refreshToken", tok.RefreshToken)
	if !tok.ExpiresAt.IsZero() {
		set("expiresAt", tok.ExpiresAt.UnixMilli())
	}
	ob, _ := json.Marshal(oauth)
	m["claudeAiOauth"] = ob
	return json.Marshal(m)
}
