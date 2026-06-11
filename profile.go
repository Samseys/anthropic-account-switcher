package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Samseys/anthropic-account-switcher/internal/claudejson"
)

// A profile (stored in ~/.claude/account-profiles/<name>/) snapshots:
//
//	credentials.json   exact copy of the OAuth tokens
//	oauthAccount.json  the raw oauthAccount value from ~/.claude.json
//	userID.txt         the raw userID value from ~/.claude.json
//	email.txt          the account email, for display

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
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	if err := writeFileAtomic(filepath.Join(dir, "credentials.json"), creds, 0o600); err != nil {
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

func cmdList() error {
	entries, _ := os.ReadDir(profileDir)
	var dirs []string
	for _, e := range entries {
		if e.IsDir() {
			dirs = append(dirs, e.Name())
		}
	}
	if len(dirs) == 0 {
		fmt.Printf("No saved profiles yet. Run '%s save' to store the current account.\n", bin)
		return nil
	}

	// Detect the active profile by stable identity (userID): Claude Code
	// rotates the OAuth token in place, so the credential blob drifts over
	// time. Fall back to a byte-for-byte credential compare if no userID is
	// present.
	var liveID string
	if cfg, ok := readFileOpt(configFile); ok {
		liveID, _ = claudejson.TopLevelValue(cfg, "userID")
	}
	liveID = strings.TrimSpace(liveID)
	var liveCred []byte
	if liveID == "" {
		liveCred, _ = tryReadCreds()
	}

	fmt.Println("Saved account profiles:")
	fmt.Println()
	for _, name := range dirs {
		dir := filepath.Join(profileDir, name)
		email := readTrim(filepath.Join(dir, "email.txt"))
		if email == "" {
			email = "unknown"
		}
		isCur := false
		if liveID != "" {
			if pid := readTrim(filepath.Join(dir, "userID.txt")); pid != "" && pid == liveID {
				isCur = true
			}
		} else if len(liveCred) > 0 {
			if pc, ok := readFileOpt(filepath.Join(dir, "credentials.json")); ok &&
				strings.TrimSpace(pc) == strings.TrimSpace(string(liveCred)) {
				isCur = true
			}
		}
		mark, tag := "  ", ""
		if isCur {
			mark, tag = "* ", "   [current]"
		}
		fmt.Printf("  %s%s   (%s)%s\n", mark, name, email, tag)
	}
	fmt.Println()
	fmt.Println("* = currently active account")
	return nil
}

func cmdSwitch(name string) error {
	if name == "" {
		return fmt.Errorf("usage: %s switch <profile-name>   (see '%s list')", bin, bin)
	}
	name = sanitize(name)
	dir := filepath.Join(profileDir, name)
	creds, err := os.ReadFile(filepath.Join(dir, "credentials.json"))
	if err != nil {
		return fmt.Errorf("no profile named %q; run '%s list' to see options", name, bin)
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

	email := readTrim(filepath.Join(dir, "email.txt"))
	if email == "" {
		email = "unknown"
	}
	fmt.Printf("Switched to %q (%s).\n\n", name, email)
	fmt.Println("IMPORTANT: fully quit Claude Code and reopen it for the new account to take")
	fmt.Println("effect. The current session is still authenticated as the previous account.")
	return nil
}

func cmdCurrent() error {
	cfg, ok := readFileOpt(configFile)
	if !ok {
		fmt.Printf("No account info found in %s. You may not be logged in.\n", configFile)
		return nil
	}
	oauth, _ := claudejson.TopLevelValue(cfg, "oauthAccount")
	if oauth == "" {
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
	fmt.Println("Current account:")
	fmt.Printf("  email: %s\n", field("emailAddress", ""))
	fmt.Printf("  org:   %s\n", field("organizationName", "organizationUuid"))
	fmt.Printf("  plan:  %s\n", field("seatTier", "billingType"))
	return nil
}

func cmdRemove(name string) error {
	if name == "" {
		return fmt.Errorf("usage: %s remove <profile-name>", bin)
	}
	name = sanitize(name)
	dir := filepath.Join(profileDir, name)
	if _, err := os.Stat(dir); err != nil {
		return fmt.Errorf("no profile named %q", name)
	}
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	fmt.Printf("Removed profile %q.\n", name)
	return nil
}
