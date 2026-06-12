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

// Profile directory contents (under ~/.claude/account-profiles/<name>/):
//
//	credentials.json   OAuth tokens (DPAPI-encrypted on Windows)
//	oauthAccount.json  raw oauthAccount value from ~/.claude.json
//	userID.txt         raw userID value from ~/.claude.json
//	email.txt          account email, for display
const (
	fileCreds  = "credentials.json"
	fileOAuth  = "oauthAccount.json"
	fileUserID = "userID.txt"
	fileEmail  = "email.txt"
)

// profilePath is the sanitization boundary: callers need not pre-sanitize (Sanitize
// is idempotent). Commands on an existing profile should use resolveProfile instead.
func profilePath(name string) string {
	return filepath.Join(paths.ProfileDir, paths.Sanitize(name))
}

// resolveProfile returns the canonical name and directory of an existing profile.
// Empty names are rejected explicitly: Sanitize("") == "" maps to ProfileDir itself,
// so profileExists("") would spuriously succeed.
func resolveProfile(name string) (string, string, error) {
	name = paths.Sanitize(name)
	if name == "" || !profileExists(name) {
		return "", "", errNoProfile(name)
	}
	return name, profilePath(name), nil
}

// profileUserID returns the raw userID from the profile (quotes included), or "".
// This is a machine-wide analytics ID; identity matching uses profileAccountID instead.
func profileUserID(dir string) string {
	return paths.ReadTrim(filepath.Join(dir, fileUserID))
}

// profileAccountID returns the accountUuid from the profile's saved oauthAccount, or "".
func profileAccountID(dir string) string {
	return claudejson.Field(profileOAuth(dir), "accountUuid")
}

// profileOAuth returns the raw oauthAccount value from the profile, or "".
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

// Names returns the names of all saved profiles, sorted (used by shell completion).
func Names() []string { return profileNames() }

func profileNames() []string {
	entries, _ := os.ReadDir(paths.ProfileDir)
	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		// Skip ".tmp-*" (in-flight writes) and "*.bak" (crash remnants from mid-swap).
		if strings.HasPrefix(e.Name(), ".") || strings.HasSuffix(e.Name(), ".bak") {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names
}

// profileForAccount returns the name of the saved profile for accountID, or "".
func profileForAccount(accountID string) string {
	for _, name := range profileNames() {
		if profileAccountID(profilePath(name)) == accountID {
			return name
		}
	}
	return ""
}

func profileExists(name string) bool {
	info, err := os.Stat(profilePath(name))
	return err == nil && info.IsDir()
}

// previousFile records the profile that was active before the last switch,
// for `switch -`.
func previousFile() string {
	return filepath.Join(paths.ProfileDir, ".previous")
}

// availableHint returns "; available: personal, work" (or a save prompt when empty).
func availableHint() string {
	names := profileNames()
	if len(names) == 0 {
		return fmt.Sprintf("; no profiles saved yet (run '%s save')", paths.Bin)
	}
	return "; available: " + strings.Join(names, ", ")
}

func errNoProfile(name string) error {
	return fmt.Errorf("no profile named %q%s", name, availableHint())
}

type profileFile struct {
	name string
	data []byte
}

// writeProfileAtomic writes to a temp dir and swaps it into place; the previous
// copy is moved to .bak and removed only on success, rolled back on failure.
func writeProfileAtomic(name string, files []profileFile) error {
	if err := os.MkdirAll(paths.ProfileDir, 0o755); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(paths.ProfileDir, ".tmp-"+paths.Sanitize(name)+"-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp) // no-op after rename succeeds
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

// encryptCreds returns the at-rest form of a credential snapshot (DPAPI on Windows,
// plaintext elsewhere).
func encryptCreds(creds []byte) ([]byte, error) {
	data, err := store.ProtectCreds(creds)
	if err != nil {
		return nil, fmt.Errorf("encrypting profile credentials: %w", err)
	}
	return data, nil
}

// readProfileCreds returns the decrypted credential snapshot. Plaintext snapshots
// (leading '{') from older versions or other OSes are returned as-is.
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

// liveIdentity holds the cached account identity from ~/.claude.json.
// oauth and userID are verbatim (quotes/braces included).
type liveIdentity struct {
	oauth     string
	userID    string
	accountID string
	email     string
}

// readLiveIdentity extracts the cached identity from ~/.claude.json; returns zero
// value if the config is missing or unreadable.
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
	id.accountID = claudejson.Field(id.oauth, "accountUuid")
	return id
}

// liveAccountID returns oauthAccount.accountUuid from ~/.claude.json, or "".
func liveAccountID() string {
	return readLiveIdentity().accountID
}

// activeProfile returns the name of the saved profile matching the live account, or "".
// Matches on accountUuid (stable across token rotation); falls back to credential
// comparison for older profiles without a stored accountUuid.
func activeProfile() string {
	liveID := liveAccountID()
	var liveCred []byte
	if liveID == "" {
		liveCred, _ = store.TryReadCreds()
	}
	for _, name := range profileNames() {
		dir := profilePath(name)
		if liveID != "" {
			if pid := profileAccountID(dir); pid != "" && pid == liveID {
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

	// existing is the profile (if any) already tracking this account by accountUuid.
	// It pins the name to prevent one account ending up under two profiles.
	existing := activeProfile()

	if name == "" {
		switch {
		case existing != "":
			name = existing
		case email != "":
			name = email
		default:
			name = "default"
		}
	}
	name = paths.Sanitize(name)

	if existing != "" && existing != name {
		return "", fmt.Errorf("this account is already saved as profile %q; use '%s save' (no name) to update it, or '%s rename %s %s' to rename it",
			existing, paths.Bin, paths.Bin, existing, name)
	}
	// A different account is already saved under this name; don't clobber it.
	if existing != name && profileExists(name) {
		return "", fmt.Errorf("a profile named %q already exists for a different account; remove it first or pick another name", name)
	}

	if !quiet && profileExists(name) {
		fmt.Printf("Updating profile %q with the current account.\n", name)
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

var expiresAtRe = regexp.MustCompile(`"expiresAt"\s*:\s*(\d+)`)

// currentInfo is the --json payload for `current`. saved is not omitempty:
// consumers rely on it being present to distinguish tracked vs. unsaved accounts.
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
		return fmt.Errorf("profile %q: %w", name, err)
	}

	active := activeProfile()
	if active == name {
		// Keep tokens fresh even on a no-op switch.
		_, _ = snapshot(name, true)
		fmt.Printf("Profile %q is already active; refreshed its snapshot.\n", name)
		return nil
	}
	if pid := profileAccountID(dir); pid != "" && pid == liveAccountID() {
		fmt.Printf("Profile %q matches the currently active account; nothing to do.\n", name)
		return nil
	}

	// Re-save the leaving account before overwriting: Claude Code rotates tokens
	// in place, so its snapshot would otherwise go stale.
	resnapped := false
	if active != "" {
		if _, err := snapshot(active, true); err == nil {
			resnapped = true
		}
	}

	// Detection is heuristic; we only warn, never block.
	claudeRunning := proc.ClaudeRunning()

	// Compute the patched config before swapping credentials so any error surfaces
	// while the live state is still consistent.
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

	// Snapshot live creds so a later failure can roll back the swap.
	prevLive, hadLive := store.TryReadCreds()
	if err := store.WriteCreds(creds); err != nil {
		return err
	}

	// Read back the credentials to confirm the write stuck — an AV scanner can
	// revert the rename, a Keychain write can fail to take. On mismatch, roll the
	// swap back rather than report a success that didn't happen. Compares the raw
	// blob, not liveAccountID (that reads the not-yet-patched config). The config
	// patch below is intentionally excluded — it's cached display identity that
	// Claude Code refreshes from the token.
	got, ok := store.TryReadCreds()
	if !ok || strings.TrimSpace(string(got)) != strings.TrimSpace(string(creds)) {
		if hadLive {
			_ = store.WriteCreds(prevLive)
		}
		return fmt.Errorf("switch failed: credentials did not take, rolled back to the previous account")
	}

	// Config patch is best-effort: Claude Code refreshes oauthAccount/userID from
	// the token, so a failure here only warrants a warning.
	var cfgErr error
	if patchCfg {
		cfgErr = paths.WriteFileAtomic(paths.ConfigFile, []byte(newCfg), 0o600)
	}

	if active != "" {
		_ = paths.WriteFileAtomic(previousFile(), []byte(active), 0o600)
	}

	email := emailOrUnknown(profileEmail(dir))
	fmt.Printf("Switched to %q (%s).\n", name, email)
	if resnapped {
		fmt.Printf("\nSaved %q's current tokens before switching.\n", active)
	}
	if cfgErr != nil {
		fmt.Fprintf(os.Stderr, "\nWARNING: switched credentials but could not update cached identity in %s (Claude Code will refresh it on its next request): %v\n", paths.ConfigFile, cfgErr)
	}
	if claudeRunning {
		fmt.Fprintln(os.Stderr, "\nWARNING: Claude Code looks like it's running. A live session can overwrite")
		fmt.Fprintln(os.Stderr, "these credentials on its next token refresh and undo the switch. Quit it if")
		fmt.Fprintln(os.Stderr, "the new account doesn't stick.")
	}
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
