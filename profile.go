package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Samseys/anthropic-account-switcher/internal/claudejson"
)

// A profile (stored in ~/.claude/account-profiles/<name>/) snapshots:
//
//	credentials.json   the OAuth tokens (DPAPI-encrypted on Windows)
//	oauthAccount.json  the raw oauthAccount value from ~/.claude.json
//	userID.txt         the raw userID value from ~/.claude.json
//	email.txt          the account email, for display

// profileNames returns the names of all saved profiles, sorted.
func profileNames() []string {
	entries, _ := os.ReadDir(profileDir)
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names
}

// profileExists reports whether a profile directory of the given name exists.
func profileExists(name string) bool {
	info, err := os.Stat(filepath.Join(profileDir, name))
	return err == nil && info.IsDir()
}

// previousFile records the profile that was active before the last switch,
// for `switch -`.
func previousFile() string {
	return filepath.Join(profileDir, ".previous")
}

// availableHint returns a human-readable list of saved profiles to append to an
// error, e.g. "; available: personal, work" (or a prompt to save if there are
// none).
func availableHint() string {
	names := profileNames()
	if len(names) == 0 {
		return fmt.Sprintf("; no profiles saved yet (run '%s save')", bin)
	}
	return "; available: " + strings.Join(names, ", ")
}

// writeProfileCreds stores the credential snapshot for a profile, encrypting
// it on Windows (see dpapi_windows.go).
func writeProfileCreds(dir string, creds []byte) error {
	data, err := protectCreds(creds)
	if err != nil {
		return fmt.Errorf("encrypting profile credentials: %w", err)
	}
	return writeFileAtomic(filepath.Join(dir, "credentials.json"), data, 0o600)
}

// readProfileCreds returns the decrypted credential snapshot of a profile.
// Plaintext snapshots from older versions (or other OSes) are recognized by
// their leading '{' and returned as-is.
func readProfileCreds(dir string) ([]byte, error) {
	b, err := os.ReadFile(filepath.Join(dir, "credentials.json"))
	if err != nil {
		return nil, err
	}
	if len(b) > 0 && b[0] == '{' {
		return b, nil
	}
	out, err := unprotectCreds(b)
	if err != nil {
		return nil, fmt.Errorf("decrypting profile credentials (saved by a different user or machine?): %w", err)
	}
	return out, nil
}

// liveUserID returns the raw userID value cached in ~/.claude.json (quotes
// included), or "".
func liveUserID() string {
	cfg, ok := readFileOpt(configFile)
	if !ok {
		return ""
	}
	id, _ := claudejson.TopLevelValue(cfg, "userID")
	return strings.TrimSpace(id)
}

// activeProfile returns the name of the saved profile matching the live
// account, or "". Matching uses the stable userID: Claude Code rotates the
// OAuth token in place, so the credential blob drifts over time. It falls back
// to a credential compare if no userID is present.
func activeProfile() string {
	liveID := liveUserID()
	var liveCred []byte
	if liveID == "" {
		liveCred, _ = tryReadCreds()
	}
	for _, name := range profileNames() {
		dir := filepath.Join(profileDir, name)
		if liveID != "" {
			if pid := readTrim(filepath.Join(dir, "userID.txt")); pid != "" && pid == liveID {
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
	creds, err := readCreds()
	if err != nil {
		return "", err
	}

	var oauthText, userIDText, email string
	if cfg, ok := readFileOpt(configFile); ok {
		oauthText, _ = claudejson.TopLevelValue(cfg, "oauthAccount")
		userIDText, _ = claudejson.TopLevelValue(cfg, "userID")
		email = claudejson.Field(oauthText, "emailAddress")
	}

	if name == "" {
		if email != "" {
			name = email
		} else {
			name = "default"
		}
	}
	name = sanitize(name)

	dir := filepath.Join(profileDir, name)
	if !quiet && profileExists(name) {
		fmt.Printf("Profile %q already exists; overwriting it.\n", name)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	if err := writeProfileCreds(dir, creds); err != nil {
		return "", err
	}
	if oauthText != "" {
		if err := writeFileAtomic(filepath.Join(dir, "oauthAccount.json"), []byte(oauthText), 0o600); err != nil {
			return "", err
		}
	}
	if userIDText != "" {
		if err := writeFileAtomic(filepath.Join(dir, "userID.txt"), []byte(userIDText), 0o600); err != nil {
			return "", err
		}
	}
	if err := writeFileAtomic(filepath.Join(dir, "email.txt"), []byte(email), 0o600); err != nil {
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

func cmdSave(name string) error {
	_, err := snapshot(name, false)
	return err
}

// expiresAtRe matches the numeric expiresAt field (ms since epoch) inside a
// credential blob.
var expiresAtRe = regexp.MustCompile(`"expiresAt"\s*:\s*(\d+)`)

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
		dir := filepath.Join(profileDir, name)
		p := profileInfo{
			Name:   name,
			Email:  readTrim(filepath.Join(dir, "email.txt")),
			Active: name == active,
		}
		if fi, err := os.Stat(filepath.Join(dir, "credentials.json")); err == nil {
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

func cmdList(asJSON bool) error {
	infos := gatherProfiles()

	if asJSON {
		b, err := json.MarshalIndent(infos, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(b))
		return nil
	}

	if len(infos) == 0 {
		fmt.Printf("No saved profiles yet. Run '%s save' to store the current account.\n", bin)
		return nil
	}

	maxName, maxEmail := 0, 0
	for _, p := range infos {
		if p.Email == "" {
			p.Email = "unknown"
		}
		maxName = max(maxName, len(p.Name))
		maxEmail = max(maxEmail, len(p.Email))
	}

	fmt.Println("Saved account profiles:")
	fmt.Println()
	anyExpired := false
	for _, p := range infos {
		mark := "  "
		if p.Active {
			mark = "* "
		}
		email := p.Email
		if email == "" {
			email = "unknown"
		}
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

func cmdSwitch(name string) error {
	switch name {
	case "":
		// With exactly two profiles, a bare `switch` toggles to the other one.
		names := profileNames()
		if len(names) != 2 {
			return fmt.Errorf("usage: %s switch <profile-name>%s", bin, availableHint())
		}
		switch activeProfile() {
		case names[0]:
			name = names[1]
		case names[1]:
			name = names[0]
		default:
			return fmt.Errorf("cannot toggle: the current account does not match a saved profile; use '%s switch <name>'", bin)
		}
	case "-":
		name = readTrim(previousFile())
		if name == "" {
			return fmt.Errorf("no previous profile recorded yet; use '%s switch <name>'", bin)
		}
	}
	name = sanitize(name)
	dir := filepath.Join(profileDir, name)
	creds, err := readProfileCreds(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("no profile named %q%s", name, availableHint())
		}
		return fmt.Errorf("profile %q: %w", name, err)
	}

	active := activeProfile()
	if active == name {
		// Re-snapshot so the profile keeps the freshest rotated tokens.
		_, _ = snapshot(name, true)
		fmt.Printf("Profile %q is already active; refreshed its snapshot.\n", name)
		return nil
	}
	if pid := readTrim(filepath.Join(dir, "userID.txt")); pid != "" && pid == liveUserID() {
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

	if err := writeCreds(creds); err != nil {
		return err
	}

	// Patch the cached identity by splicing only oauthAccount/userID.
	if cfg, ok := readFileOpt(configFile); ok {
		if o := readTrim(filepath.Join(dir, "oauthAccount.json")); o != "" {
			cfg = claudejson.SetTopLevelValue(cfg, "oauthAccount", o)
		}
		if u := readTrim(filepath.Join(dir, "userID.txt")); u != "" {
			cfg = claudejson.SetTopLevelValue(cfg, "userID", u)
		}
		if err := writeFileAtomic(configFile, []byte(cfg), 0o600); err != nil {
			return err
		}
	}

	if active != "" {
		_ = writeFileAtomic(previousFile(), []byte(active), 0o600)
	}

	email := readTrim(filepath.Join(dir, "email.txt"))
	if email == "" {
		email = "unknown"
	}
	fmt.Printf("Switched to %q (%s).\n\n", name, email)
	fmt.Println("IMPORTANT: fully quit Claude Code and reopen it for the new account to take")
	fmt.Println("effect. The current session is still authenticated as the previous account.")
	return nil
}

func cmdCurrent(asJSON bool) error {
	cfg, ok := readFileOpt(configFile)
	oauth := ""
	if ok {
		oauth, _ = claudejson.TopLevelValue(cfg, "oauthAccount")
	}
	if oauth == "" {
		if asJSON {
			fmt.Println(`{"loggedIn": false}`)
			return nil
		}
		fmt.Printf("No account info found in %s. You may not be logged in.\n", configFile)
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

	if asJSON {
		out := struct {
			LoggedIn     bool   `json:"loggedIn"`
			Profile      string `json:"profile,omitempty"`
			Email        string `json:"email"`
			Organization string `json:"organization"`
			Plan         string `json:"plan"`
		}{
			LoggedIn:     true,
			Profile:      profile,
			Email:        field("emailAddress", ""),
			Organization: field("organizationName", "organizationUuid"),
			Plan:         field("seatTier", "billingType"),
		}
		b, err := json.MarshalIndent(out, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(b))
		return nil
	}

	fmt.Println("Current account:")
	if profile != "" {
		fmt.Printf("  profile: %s\n", profile)
	}
	fmt.Printf("  email:   %s\n", field("emailAddress", ""))
	fmt.Printf("  org:     %s\n", field("organizationName", "organizationUuid"))
	fmt.Printf("  plan:    %s\n", field("seatTier", "billingType"))
	return nil
}

func cmdRemove(name string) error {
	if name == "" {
		return fmt.Errorf("usage: %s remove <profile-name>", bin)
	}
	name = sanitize(name)
	dir := filepath.Join(profileDir, name)
	if _, err := os.Stat(dir); err != nil {
		return fmt.Errorf("no profile named %q%s", name, availableHint())
	}
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	fmt.Printf("Removed profile %q.\n", name)
	return nil
}

func cmdRename(oldName, newName string) error {
	if oldName == "" || newName == "" {
		return fmt.Errorf("usage: %s rename <old-name> <new-name>", bin)
	}
	oldName = sanitize(oldName)
	newName = sanitize(newName)
	if oldName == newName {
		return fmt.Errorf("the new name is the same as the old one")
	}
	if !profileExists(oldName) {
		return fmt.Errorf("no profile named %q%s", oldName, availableHint())
	}
	if profileExists(newName) {
		return fmt.Errorf("a profile named %q already exists; remove it first or pick another name", newName)
	}
	if err := os.Rename(filepath.Join(profileDir, oldName), filepath.Join(profileDir, newName)); err != nil {
		return err
	}
	if readTrim(previousFile()) == oldName {
		_ = writeFileAtomic(previousFile(), []byte(newName), 0o600)
	}
	fmt.Printf("Renamed profile %q to %q.\n", oldName, newName)
	return nil
}
