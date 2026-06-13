package profile

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Samseys/anthropic-account-switcher/internal/paths"
	"github.com/Samseys/anthropic-account-switcher/internal/proc"
	"github.com/Samseys/anthropic-account-switcher/internal/store"
	"github.com/Samseys/anthropic-account-switcher/internal/usage"
)

// Online fallback for the auto-switcher. The status-line sensor only runs in the
// terminal CLI (the VSCode panel never invokes status-line commands), so when no
// fresh sensor reading is available we read usage straight from Anthropic's OAuth
// usage endpoint instead. The endpoint also feeds *candidate* ranking: saved but
// inactive accounts never run the sensor, so their per-profile cache only reflects
// the last time they were active — to switch based on each account's *current*
// headroom we must poll them live. See internal/usage/endpoint.go.

const (
	// endpointPollInterval is the floor between active-account endpoint polls in
	// the watch loop; the endpoint rate-limits hard, so we poll no faster.
	endpointPollInterval = usage.MinInterval
	// tokenRefreshSkew refreshes a candidate's access token this far before its
	// stated expiry, so a poll never races the expiry boundary.
	tokenRefreshSkew = 60 * time.Second
)

// fetchActiveOnline reads the live account's usage from the endpoint. It never
// refreshes or rewrites the live credentials — Claude Code owns that token and
// keeps it fresh while it is running — so on an unauthorized response it simply
// returns the error and the caller skips this cycle.
func fetchActiveOnline(ctx context.Context) (*usage.Report, error) {
	raw, err := store.ReadCreds()
	if err != nil {
		return nil, err
	}
	creds, err := usage.ParseCredentials(raw)
	if err != nil {
		return nil, err
	}
	return usage.Fetch(ctx, creds.AccessToken)
}

// fetchProfileOnline reads a saved profile's current usage from the endpoint,
// refreshing and persisting its access token first when it is near expiry (or
// retrying once on an unauthorized response). Candidate-profile tokens are safe
// to refresh and write back — unlike the live credentials — because Claude Code
// is not the one holding them.
func fetchProfileOnline(ctx context.Context, name string) (*usage.Report, error) {
	dir := profilePath(name)
	raw, err := readProfileCreds(dir)
	if err != nil {
		return nil, err
	}
	creds, err := usage.ParseCredentials(raw)
	if err != nil {
		return nil, err
	}
	if creds.NearExpiry(tokenRefreshSkew) {
		if raw, creds, err = refreshProfile(ctx, dir, raw, creds); err != nil {
			return nil, err
		}
	}
	rep, err := usage.Fetch(ctx, creds.AccessToken)
	if errors.Is(err, usage.ErrUnauthorized) {
		// The expiry estimate was wrong (or absent); refresh once and retry.
		if _, creds, rerr := refreshProfile(ctx, dir, raw, creds); rerr == nil {
			return usage.Fetch(ctx, creds.AccessToken)
		}
		return nil, err
	}
	return rep, err
}

// refreshProfile exchanges a profile's refresh token for a fresh access token and
// persists it back into the profile's encrypted credential snapshot. Persisting
// is best-effort: if the write fails we still return the in-memory token so the
// poll can proceed (the next refresh will try to persist again).
func refreshProfile(ctx context.Context, dir string, raw []byte, creds usage.Credentials) ([]byte, usage.Credentials, error) {
	if creds.RefreshToken == "" {
		return raw, creds, fmt.Errorf("no refresh token saved for this profile; log in to it again")
	}
	tok, err := usage.RefreshToken(ctx, creds.RefreshToken)
	if err != nil {
		return raw, creds, err
	}
	merged, perr := persistProfileToken(dir, raw, tok)
	if perr != nil {
		fmt.Fprintf(os.Stderr, "  warning: refreshed token but could not persist it to %s: %v\n", dir, perr)
	}
	return merged, usage.Credentials{AccessToken: tok.AccessToken, RefreshToken: tok.RefreshToken, ExpiresAt: tok.ExpiresAt}, nil
}

// persistProfileToken writes a refreshed token back into the profile's credential
// snapshot, preserving every other field and re-encrypting at rest. It returns
// the merged plaintext creds even when the write fails, so the caller can keep
// using the fresh token in memory.
func persistProfileToken(dir string, oldCreds []byte, tok usage.Token) ([]byte, error) {
	merged, err := usage.WithCredentialTokens(oldCreds, tok)
	if err != nil {
		return oldCreds, err
	}
	enc, err := encryptCreds(merged)
	if err != nil {
		return merged, err
	}
	if err := paths.WriteFileAtomic(filepath.Join(dir, fileCreds), enc, 0o600); err != nil {
		return merged, err
	}
	return merged, nil
}

// refreshUsageOnline replaces the *inactive* profiles' cached usage with a live
// endpoint reading. With force (`list --refresh`) it polls every inactive profile;
// otherwise it refreshes one only when its data is no longer trustworthy — its
// access token has expired, or its usage reading is missing or older than
// cacheStaleAfter — so a plain `list` shows current numbers (and quietly renews a
// lapsed token) without polling on every invocation. Inactive accounts refresh and
// persist their own token as needed, and the renewed expiry is reflected back into
// the row. The active account is handled separately by refreshActiveUsageIfStale
// (read-only; Claude Code owns its live token). Each fresh reading is also written
// to the profile's cache. Failures warn and leave the cached reading in place.
func refreshUsageOnline(infos []profileInfo, force bool) {
	ctx := context.Background()
	for i := range infos {
		p := &infos[i]
		if p.Active {
			continue // the active account is refreshed via refreshActiveUsageIfStale
		}
		expired := p.TokenExpiresAt != nil && p.TokenExpiresAt.Before(time.Now())
		stale := p.UsageRecordedAt == nil || time.Since(*p.UsageRecordedAt) > cacheStaleAfter
		if !force && !expired && !stale {
			continue // default: only refresh expired or stale data
		}

		rep, err := fetchProfileOnline(ctx, p.Name)
		p.TokenExpiresAt = profileTokenExpiry(profilePath(p.Name)) // may have been renewed by the refresh
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not refresh usage for %q: %v\n", p.Name, err)
			continue
		}
		t := time.Now()
		p.FiveHour, p.SevenDay, p.UsageRecordedAt = rep.FiveHour, rep.SevenDay, &t
		recordProfileUsage(p.Name, rep.FiveHour, rep.SevenDay)
	}
}

// shouldPollActive decides whether to poll the active account's usage online now.
// The sensor keeps the reading warm in the terminal, but not in the VSCode panel,
// so the read paths fall back to the endpoint when the local reading is older than
// activeStaleAfter — while Claude Code is running (it owns and keeps the live token
// fresh) and no faster than usage.MinInterval. force (`list --refresh`) bypasses
// all three gates.
func shouldPollActive(lastRecorded time.Time, haveReading, force bool) bool {
	if force {
		return true
	}
	if haveReading && time.Since(lastRecorded) <= activeStaleAfter {
		return false
	}
	return proc.ClaudeRunning() && !recentActivePoll()
}

// refreshActiveUsageIfStale polls the active account's usage from the endpoint and
// records it (to the global state file and the active profile's cache) when the
// local reading has gone stale. It is the VSCode-panel counterpart to the sensor:
// read-only against the live credentials (never refreshes or rewrites them) and
// best-effort — on any failure it leaves the cached reading untouched and stays
// silent, since this runs behind `usage`/`list` that the panel polls on a timer.
func refreshActiveUsageIfStale(force bool) {
	active := activeProfile()
	if active == "" {
		return
	}
	snap, ok := readUsageState()
	if !shouldPollActive(snap.UpdatedAt, ok && snap.Account == active, force) {
		return
	}
	markActivePoll() // stamp the attempt first, so a failing poll backs off too
	rep, err := fetchActiveOnline(context.Background())
	if err != nil {
		return
	}
	recordUsage(usageSnapshot{Account: active, FiveHour: rep.FiveHour, SevenDay: rep.SevenDay, UpdatedAt: time.Now()})
}

// evaluateOnline polls the active account's usage from the endpoint, records it
// (so `usage`/`list`/the panel reflect it too), and switches if it trips the
// threshold. It is the fallback used when the status-line sensor has no fresh
// reading — chiefly the VSCode panel, which never runs status-line commands.
func evaluateOnline(ctx context.Context, opts AutoSwitchOptions) {
	active := activeProfile()
	if active == "" {
		if opts.Once {
			fmt.Println("No saved profile matches the active account; nothing to watch.")
		}
		return
	}
	rep, err := fetchActiveOnline(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  cannot read active-account usage from the API: %v\n", err)
		return
	}
	recordUsage(usageSnapshot{Account: active, FiveHour: rep.FiveHour, SevenDay: rep.SevenDay, UpdatedAt: time.Now()})

	five, seven := windowPct(rep.FiveHour), windowPct(rep.SevenDay)
	fmt.Printf("[%s] %-16s 5h %3.0f%%  7d %3.0f%%  %s\n",
		time.Now().Local().Format("15:04:05"), active, five, seven, dim("(api)"))
	tripAndSwitch(ctx, opts, active, five, seven)
}

// chooseTargetOnline ranks switch candidates by their *current* endpoint usage:
// every saved account other than active is polled live (refreshing its token as
// needed), and the recorded reading also updates that profile's cache so `usage`
// and the panel reflect it. A candidate whose live 5-hour usage is below the
// threshold wins (lowest first). When a candidate cannot be polled (network
// error, rejected refresh) it falls back to its last cached reading, and failing
// that is treated as unknown — a last resort that refreshes on the switch itself.
func chooseTargetOnline(ctx context.Context, active string, threshold float64) (name, reason string, err error) {
	var below, unknown []candidate
	for _, n := range profileNames() {
		if n == active {
			continue
		}
		if rep, ferr := fetchProfileOnline(ctx, n); ferr == nil {
			recordProfileUsage(n, rep.FiveHour, rep.SevenDay)
			if c := (candidate{name: n, five: windowPct(rep.FiveHour), known: true}); c.five < threshold {
				below = append(below, c)
			}
			continue
		}
		// Live poll failed; fall back to the last cached reading if it is fresh.
		if snap, ok := readProfileUsageCache(n); ok && time.Since(snap.UpdatedAt) <= cacheStaleAfter {
			if c := (candidate{name: n, five: windowPct(snap.FiveHour), known: true}); c.five < threshold {
				below = append(below, c)
			}
			continue
		}
		unknown = append(unknown, candidate{name: n})
	}
	return pickCandidate(below, unknown, threshold)
}
