// Package profile implements the account-profile commands: snapshotting the
// live Claude Code account (OAuth credentials + cached identity) into named
// profiles and restoring them on demand.
package profile

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Samseys/anthropic-account-switcher/internal/claudejson"
	"github.com/Samseys/anthropic-account-switcher/internal/lock"
	"github.com/Samseys/anthropic-account-switcher/internal/paths"
	"github.com/Samseys/anthropic-account-switcher/internal/proc"
	"github.com/Samseys/anthropic-account-switcher/internal/store"
)

// A profile (stored in ~/.claude/account-profiles/<name>/) snapshots:
//
//	credentials.json   the OAuth tokens (DPAPI-encrypted on Windows)
//	oauthAccount.json  the raw oauthAccount value from ~/.claude.json
//	userID.txt         the raw userID value from ~/.claude.json
//	email.txt          the account email, for display
const (
	fileCreds  = "credentials.json"
	fileOAuth  = "oauthAccount.json"
	fileUserID = "userID.txt"
	fileEmail  = "email.txt"
)

// profilePath returns the directory of the named profile. It is the authoritative
// sanitization boundary: the name is sanitized here so it always maps to a single,
// filesystem-safe directory, and callers need not pre-sanitize for path purposes
// (Sanitize is idempotent). Commands acting on an existing profile should go
// through resolveProfile, which also validates existence.
func profilePath(name string) string {
	return filepath.Join(paths.ProfileDir, paths.Sanitize(name))
}

// resolveProfile is the single entry point for commands that operate on an
// existing profile. It returns the canonical (sanitized) name and its directory,
// erroring with the available-profiles hint when no such profile exists. Callers
// must reject an empty name first: Sanitize("") is "", which maps to ProfileDir.
func resolveProfile(name string) (string, string, error) {
	name = paths.Sanitize(name)
	if !profileExists(name) {
		return "", "", errNoProfile(name)
	}
	return name, profilePath(name), nil
}

// profileUserID returns the userID recorded in the profile at dir (quotes
// included, matching liveUserID), or "".
func profileUserID(dir string) string {
	return paths.ReadTrim(filepath.Join(dir, fileUserID))
}

// profileOAuth returns the raw oauthAccount value recorded in the profile at
// dir, or "".
func profileOAuth(dir string) string {
	return paths.ReadTrim(filepath.Join(dir, fileOAuth))
}

// profileEmail returns the account email recorded in the profile at dir, or "".
func profileEmail(dir string) string {
	return paths.ReadTrim(filepath.Join(dir, fileEmail))
}

// emailOrUnknown returns email, or "unknown" when it is empty.
func emailOrUnknown(email string) string {
	if email == "" {
		return "unknown"
	}
	return email
}

// profileNames returns the names of all saved profiles, sorted.
func profileNames() []string {
	entries, _ := os.ReadDir(paths.ProfileDir)
	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		// Skip our own scratch dirs: ".tmp-*" (in-flight writes) and "*.bak"
		// (a previous copy a crash may have left behind mid-swap).
		if strings.HasPrefix(e.Name(), ".") || strings.HasSuffix(e.Name(), ".bak") {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names
}

// profileExists reports whether a profile directory of the given name exists.
func profileExists(name string) bool {
	info, err := os.Stat(profilePath(name))
	return err == nil && info.IsDir()
}

// previousFile records the profile that was active before the last switch,
// for `switch -`.
func previousFile() string {
	return filepath.Join(paths.ProfileDir, ".previous")
}

// availableHint returns a human-readable list of saved profiles to append to an
// error, e.g. "; available: personal, work" (or a prompt to save if there are
// none).
func availableHint() string {
	names := profileNames()
	if len(names) == 0 {
		return fmt.Sprintf("; no profiles saved yet (run '%s save')", paths.Bin)
	}
	return "; available: " + strings.Join(names, ", ")
}

// errNoProfile builds the "no profile named X" error, appending the list of
// saved profiles as a hint.
func errNoProfile(name string) error {
	return fmt.Errorf("no profile named %q%s", name, availableHint())
}

// profileFile is one file to be written into a profile directory.
type profileFile struct {
	name string
	data []byte
}

// writeProfileAtomic builds the named profile in a temp directory and swaps it
// into place, so a failure mid-write never leaves a half-populated profile and
// never destroys the existing one. The previous copy is moved aside and only
// removed once the new directory is in place; on any error it is rolled back.
func writeProfileAtomic(name string, files []profileFile) error {
	if err := os.MkdirAll(paths.ProfileDir, 0o755); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(paths.ProfileDir, ".tmp-"+paths.Sanitize(name)+"-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp) // no-op once renamed into place
	for _, f := range files {
		if err := paths.WriteFileAtomic(filepath.Join(tmp, f.name), f.data, 0o600); err != nil {
			return err
		}
	}

	final := profilePath(name)
	backup := final + ".bak"
	_ = os.RemoveAll(backup)
	if _, err := os.Stat(final); err == nil {
		if err := os.Rename(final, backup); err != nil {
			return err
		}
	}
	if err := os.Rename(tmp, final); err != nil {
		_ = os.Rename(backup, final) // roll back to the previous copy
		return err
	}
	_ = os.RemoveAll(backup)
	return nil
}

// encryptCreds returns the at-rest form of a credential snapshot (DPAPI-encrypted
// on Windows, plaintext elsewhere; see internal/store).
func encryptCreds(creds []byte) ([]byte, error) {
	data, err := store.ProtectCreds(creds)
	if err != nil {
		return nil, fmt.Errorf("encrypting profile credentials: %w", err)
	}
	return data, nil
}

// readProfileCreds returns the decrypted credential snapshot of a profile.
// Plaintext snapshots from older versions (or other OSes) are recognized by
// their leading '{' and returned as-is.
func readProfileCreds(dir string) ([]byte, error) {
	b, err := os.ReadFile(filepath.Join(dir, fileCreds))
	if err != nil {
		return nil, err
	}
	if len(b) > 0 && b[0] == '{' {
		return b, nil
	}
	out, err := store.UnprotectCreds(b)
	if err != nil {
		return nil, fmt.Errorf("decrypting profile credentials (saved by a different user or machine?): %w", err)
	}
	return out, nil
}

// liveIdentity is the cached account identity spliced into ~/.claude.json: the
// raw oauthAccount and userID values (verbatim, quotes/braces included) plus the
// emailAddress pulled out of oauthAccount for display.
type liveIdentity struct {
	oauth  string
	userID string
	email  string
}

// readLiveIdentity extracts the cached identity from ~/.claude.json. A missing
// or unreadable config yields a zero liveIdentity (all fields "").
func readLiveIdentity() liveIdentity {
	var id liveIdentity
	cfg, ok := paths.ReadFileOpt(paths.ConfigFile)
	if !ok {
		return id
	}
	oauth, _ := claudejson.TopLevelValue(cfg, "oauthAccount")
	userID, _ := claudejson.TopLevelValue(cfg, "userID")
	id.oauth = strings.TrimSpace(oauth)
	id.userID = strings.TrimSpace(userID)
	id.email = claudejson.Field(id.oauth, "emailAddress")
	return id
}

// liveUserID returns the raw userID value cached in ~/.claude.json (quotes
// included), or "".
func liveUserID() string {
	return readLiveIdentity().userID
}

// activeProfile returns the name of the saved profile matching the live
// account, or "". Matching uses the stable userID: Claude Code rotates the
// OAuth token in place, so the credential blob drifts over time. It falls back
// to a credential compare if no userID is present.
func activeProfile() string {
	liveID := liveUserID()
	var liveCred []byte
	if liveID == "" {
		liveCred, _ = store.TryReadCreds()
	}
	for _, name := range profileNames() {
		dir := profilePath(name)
		if liveID != "" {
			if pid := profileUserID(dir); pid != "" && pid == liveID {
				return name
			}
		} else if len(liveCred) > 0 {
			if pc, err := readProfileCreds(dir); err == nil &&
				strings.TrimSpace(string(pc)) == strings.TrimSpace(string(liveCred)) {
				return name
			}
		}
	}
	return ""
}

func snapshot(name string, quiet bool) (string, error) {
	creds, err := store.ReadCreds()
	if err != nil {
		return "", err
	}

	id := readLiveIdentity()
	oauthText, userIDText, email := id.oauth, id.userID, id.email

	if name == "" {
		if email != "" {
			name = email
		} else {
			name = "default"
		}
	}
	name = paths.Sanitize(name)

	if !quiet && profileExists(name) {
		fmt.Printf("Profile %q already exists; overwriting it.\n", name)
	}

	encCreds, err := encryptCreds(creds)
	if err != nil {
		return "", err
	}
	files := []profileFile{
		{fileCreds, encCreds},
		{fileEmail, []byte(email)},
	}
	if oauthText != "" {
		files = append(files, profileFile{fileOAuth, []byte(oauthText)})
	}
	if userIDText != "" {
		files = append(files, profileFile{fileUserID, []byte(userIDText)})
	}
	if err := writeProfileAtomic(name, files); err != nil {
		return "", err
	}

	if !quiet {
		shown := email
		if shown == "" {
			shown = "unknown email"
		}
		fmt.Printf("Saved current account (%s) as profile %q.\n", shown, name)
	}
	return name, nil
}

// Save stores the current account as a named profile (defaulting to its email).
func Save(name string) error {
	release, err := lock.Acquire()
	if err != nil {
		return err
	}
	defer release()
	_, err = snapshot(name, false)
	return err
}

// expiresAtRe matches the numeric expiresAt field (ms since epoch) inside a
// credential blob.
var expiresAtRe = regexp.MustCompile(`"expiresAt"\s*:\s*(\d+)`)

// currentInfo is the --json payload for `current` when logged in. saved is not
// omitempty: consumers rely on it being present (true or false) to distinguish a
// tracked account from an unsaved one.
type currentInfo struct {
	LoggedIn     bool   `json:"loggedIn"`
	Saved        bool   `json:"saved"`
	Profile      string `json:"profile,omitempty"`
	Email        string `json:"email"`
	Organization string `json:"organization"`
	Plan         string `json:"plan"`
}

type profileInfo struct {
	Name           string     `json:"name"`
	Email          string     `json:"email,omitempty"`
	Active         bool       `json:"active"`
	SavedAt        *time.Time `json:"savedAt,omitempty"`
	TokenExpiresAt *time.Time `json:"tokenExpiresAt,omitempty"`
}

func gatherProfiles() []profileInfo {
	active := activeProfile()
	infos := []profileInfo{}
	for _, name := range profileNames() {
		dir := profilePath(name)
		p := profileInfo{
			Name:   name,
			Email:  profileEmail(dir),
			Active: name == active,
		}
		if fi, err := os.Stat(filepath.Join(dir, fileCreds)); err == nil {
			t := fi.ModTime()
			p.SavedAt = &t
		}
		if creds, err := readProfileCreds(dir); err == nil {
			if m := expiresAtRe.FindSubmatch(creds); m != nil {
				if ms, err := strconv.ParseInt(string(m[1]), 10, 64); err == nil {
					t := time.UnixMilli(ms)
					p.TokenExpiresAt = &t
				}
			}
		}
		infos = append(infos, p)
	}
	return infos
}

// List prints the saved profiles (or a JSON array when asJSON is set).
func List(asJSON bool) error {
	infos := gatherProfiles()

	if asJSON {
		return paths.PrintJSON(infos)
	}

	if len(infos) == 0 {
		fmt.Printf("No saved profiles yet. Run '%s save' to store the current account.\n", paths.Bin)
		return nil
	}

	maxName, maxEmail := 0, 0
	for _, p := range infos {
		maxName = max(maxName, len(p.Name))
		maxEmail = max(maxEmail, len(emailOrUnknown(p.Email)))
	}

	fmt.Println("Saved account profiles:")
	fmt.Println()
	anyExpired := false
	for _, p := range infos {
		mark := "  "
		if p.Active {
			mark = "* "
		}
		email := emailOrUnknown(p.Email)
		line := fmt.Sprintf("  %s%-*s  %-*s", mark, maxName, p.Name, maxEmail, email)
		if p.SavedAt != nil {
			line += "  saved " + p.SavedAt.Format("2006-01-02 15:04")
		}
		if p.TokenExpiresAt != nil && p.TokenExpiresAt.Before(time.Now()) {
			line += "  [token expired]"
			anyExpired = true
		}
		fmt.Println(strings.TrimRight(line, " "))
	}
	fmt.Println()
	fmt.Println("* = currently active account")
	if anyExpired {
		fmt.Println("[token expired] = access token past its expiry; Claude Code will refresh")
		fmt.Println("it or ask you to log in again after switching.")
	}
	return nil
}

// Switch activates the named profile (or "-" for previous, "" to toggle between
// exactly two profiles), restoring its credentials and cached identity.
func Switch(name string) error {
	release, err := lock.Acquire()
	if err != nil {
		return err
	}
	defer release()

	switch name {
	case "":
		// With exactly two profiles, a bare `switch` toggles to the other one.
		names := profileNames()
		if len(names) != 2 {
			return fmt.Errorf("usage: %s switch <profile-name>%s", paths.Bin, availableHint())
		}
		switch activeProfile() {
		case names[0]:
			name = names[1]
		case names[1]:
			name = names[0]
		default:
			return fmt.Errorf("cannot toggle: the current account does not match a saved profile; use '%s switch <name>'", paths.Bin)
		}
	case "-":
		name = paths.ReadTrim(previousFile())
		if name == "" {
			return fmt.Errorf("no previous profile recorded yet; use '%s switch <name>'", paths.Bin)
		}
	}
	name, dir, err := resolveProfile(name)
	if err != nil {
		return err
	}
	creds, err := readProfileCreds(dir)
	if err != nil {
		// The directory exists (resolveProfile checked), so this is a real
		// read/decrypt failure, not a missing profile.
		return fmt.Errorf("profile %q: %w", name, err)
	}

	active := activeProfile()
	if active == name {
		// Re-snapshot so the profile keeps the freshest rotated tokens.
		_, _ = snapshot(name, true)
		fmt.Printf("Profile %q is already active; refreshed its snapshot.\n", name)
		return nil
	}
	if pid := profileUserID(dir); pid != "" && pid == liveUserID() {
		fmt.Printf("Profile %q matches the currently active account; nothing to do.\n", name)
		return nil
	}

	// Claude Code rotates tokens in place, so the snapshot of the account we
	// are leaving goes stale; re-save it (best effort) before overwriting.
	if active != "" {
		if _, err := snapshot(active, true); err == nil {
			fmt.Printf("Updated profile %q with the current tokens before switching.\n", active)
		}
	}

	// A live Claude Code session can rewrite .credentials.json on a token
	// refresh and clobber the swap. Warn loudly but never block: detection is
	// heuristic, so a false positive must not stop the user.
	if proc.ClaudeRunning() {
		fmt.Fprintln(os.Stderr, "WARNING: Claude Code appears to be running. It may overwrite the")
		fmt.Fprintln(os.Stderr, "credentials on a token refresh and undo this switch. Fully quit it first,")
		fmt.Fprintln(os.Stderr, "then re-run if the switch doesn't take effect after restarting.")
	}

	// Compute the patched identity up front, before swapping credentials, so a
	// problem reading or splicing the config surfaces while the live state is
	// still consistent rather than half-switched.
	newCfg, patchCfg := "", false
	if cfg, ok := paths.ReadFileOpt(paths.ConfigFile); ok {
		if o := profileOAuth(dir); o != "" {
			cfg = claudejson.SetTopLevelValue(cfg, "oauthAccount", o)
		}
		if u := profileUserID(dir); u != "" {
			cfg = claudejson.SetTopLevelValue(cfg, "userID", u)
		}
		newCfg, patchCfg = cfg, true
	}

	// Snapshot the live creds so a hard failure in a later step can roll the
	// swap back, never leaving live creds and cached identity disagreeing.
	prevLive, hadLive := store.TryReadCreds()
	if err := store.WriteCreds(creds); err != nil {
		return err
	}

	// Future-proofing: run any post-commit steps as a transaction. The config
	// patch below is intentionally NOT in here — it is cached display identity
	// that Claude Code refreshes from the token, so it stays best-effort. A new
	// step that genuinely can't be left half-done belongs here, where a failure
	// rolls the credential swap back.
	var postCommit []func() error
	for _, step := range postCommit {
		if err := step(); err != nil {
			if hadLive {
				_ = store.WriteCreds(prevLive)
			}
			return fmt.Errorf("switch failed after writing credentials, rolled back: %w", err)
		}
	}

	// The credential swap (above) is the switch; the oauthAccount/userID in
	// ~/.claude.json is just cached display identity that Claude Code refreshes
	// from the token. So if this write fails we warn but don't fail the command,
	// rather than leaving the user with a hard error after the real work landed.
	if patchCfg {
		if err := paths.WriteFileAtomic(paths.ConfigFile, []byte(newCfg), 0o600); err != nil {
			fmt.Fprintf(os.Stderr, "WARNING: switched credentials but could not update cached identity in %s (Claude Code will refresh it on restart): %v\n", paths.ConfigFile, err)
		}
	}

	if active != "" {
		_ = paths.WriteFileAtomic(previousFile(), []byte(active), 0o600)
	}

	email := emailOrUnknown(profileEmail(dir))
	fmt.Printf("Switched to %q (%s).\n\n", name, email)
	fmt.Println("IMPORTANT: fully quit Claude Code and reopen it for the new account to take")
	fmt.Println("effect. The current session is still authenticated as the previous account.")
	return nil
}

// Current shows the active account (or a JSON object when asJSON is set).
func Current(asJSON bool) error {
	oauth := readLiveIdentity().oauth
	if oauth == "" {
		if asJSON {
			return paths.PrintJSON(struct {
				LoggedIn bool `json:"loggedIn"`
			}{LoggedIn: false})
		}
		fmt.Printf("No account info found in %s. You may not be logged in.\n", paths.ConfigFile)
		return nil
	}
	field := func(primary, fallback string) string {
		if v := claudejson.Field(oauth, primary); v != "" {
			return v
		}
		if fallback != "" {
			if v := claudejson.Field(oauth, fallback); v != "" {
				return v
			}
		}
		return "unknown"
	}
	profile := activeProfile()
	email := field("emailAddress", "")
	org := field("organizationName", "organizationUuid")
	plan := field("seatTier", "billingType")

	if asJSON {
		return paths.PrintJSON(currentInfo{
			LoggedIn:     true,
			Saved:        profile != "",
			Profile:      profile,
			Email:        email,
			Organization: org,
			Plan:         plan,
		})
	}

	fmt.Println("Current account:")
	if profile != "" {
		fmt.Printf("  profile: %s\n", profile)
	} else {
		fmt.Printf("  profile: (unsaved - run '%s save' to track this account)\n", paths.Bin)
	}
	fmt.Printf("  email:   %s\n", email)
	fmt.Printf("  org:     %s\n", org)
	fmt.Printf("  plan:    %s\n", plan)
	return nil
}

// Remove deletes a saved profile.
func Remove(name string) error {
	if name == "" {
		return fmt.Errorf("usage: %s remove <profile-name>", paths.Bin)
	}
	release, err := lock.Acquire()
	if err != nil {
		return err
	}
	defer release()
	name, dir, err := resolveProfile(name)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	fmt.Printf("Removed profile %q.\n", name)
	return nil
}

// Rename renames a saved profile, keeping the `switch -` marker in sync.
func Rename(oldName, newName string) error {
	if oldName == "" || newName == "" {
		return fmt.Errorf("usage: %s rename <old-name> <new-name>", paths.Bin)
	}
	release, err := lock.Acquire()
	if err != nil {
		return err
	}
	defer release()
	oldName, oldDir, err := resolveProfile(oldName)
	if err != nil {
		return err
	}
	newName = paths.Sanitize(newName)
	if oldName == newName {
		return fmt.Errorf("the new name is the same as the old one")
	}
	if profileExists(newName) {
		return fmt.Errorf("a profile named %q already exists; remove it first or pick another name", newName)
	}
	if err := os.Rename(oldDir, profilePath(newName)); err != nil {
		return err
	}
	if paths.ReadTrim(previousFile()) == oldName {
		_ = paths.WriteFileAtomic(previousFile(), []byte(newName), 0o600)
	}
	fmt.Printf("Renamed profile %q to %q.\n", oldName, newName)
	return nil
}
