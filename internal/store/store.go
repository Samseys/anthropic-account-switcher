// Package store reads and writes the live Claude Code OAuth credentials, and
// encrypts profile snapshots at rest. Credential storage is OS-aware:
//
//	Windows / Linux : ~/.claude/.credentials.json, copied verbatim.
//	macOS           : the login Keychain, via the built-in `security` CLI.
//
// We exec `security` rather than linking the Security framework so the binary
// stays pure Go (CGO_ENABLED=0) and cross-compiles for every target from one
// machine.
package store

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"runtime"
	"strings"

	"github.com/Samseys/anthropic-account-switcher/internal/paths"
)

const keychainServiceDefault = "Claude Code-credentials"

func keychainService() string {
	if v := os.Getenv("CLAUDE_KEYCHAIN_SERVICE"); v != "" {
		return v
	}
	return keychainServiceDefault
}

func keychainAccount() string {
	if u, err := user.Current(); err == nil && u.Username != "" {
		name := u.Username
		if i := strings.LastIndexAny(name, `\/`); i >= 0 {
			name = name[i+1:] // strip any DOMAIN\ prefix
		}
		return name
	}
	return os.Getenv("USER")
}

func keychainRead() ([]byte, bool) {
	out, err := exec.Command("security", "find-generic-password",
		"-a", keychainAccount(), "-s", keychainService(), "-w").Output()
	if err != nil {
		return nil, false
	}
	// `security -w` appends a trailing newline; drop it so round-trips match.
	return bytes.TrimRight(out, "\r\n"), true
}

// useKeychain mirrors the precedence used by the original shell tool: on macOS,
// prefer the Keychain when an item exists, fall back to the file if one is
// present, otherwise default to the Keychain.
func useKeychain() bool {
	if runtime.GOOS != "darwin" {
		return false
	}
	if _, ok := keychainRead(); ok {
		return true
	}
	if _, err := os.Stat(paths.CredFile); err == nil {
		return false
	}
	return true
}

// ReadCreds returns the live OAuth credential bytes, or an error explaining
// that the user is not logged in.
func ReadCreds() ([]byte, error) {
	if useKeychain() {
		if b, ok := keychainRead(); ok {
			return b, nil
		}
		return nil, fmt.Errorf("no credentials in the Keychain (service %q); log in to Claude Code first", keychainService())
	}
	b, err := os.ReadFile(paths.CredFile)
	if err != nil {
		return nil, fmt.Errorf("no credentials at %s; log in to Claude Code first", paths.CredFile)
	}
	return b, nil
}

// TryReadCreds is the non-fatal variant used for active-profile detection.
func TryReadCreds() ([]byte, bool) {
	b, err := ReadCreds()
	if err != nil {
		return nil, false
	}
	return b, true
}

// securityQuote quotes an argument for `security -i`'s command tokenizer,
// which honors double quotes and backslash escapes.
func securityQuote(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}

func WriteCreds(data []byte) error {
	if useKeychain() {
		// -U updates the item if it already exists. Fed via stdin so the token
		// never appears in the process argument list.
		line := fmt.Sprintf("add-generic-password -U -a %s -s %s -w %s\n",
			securityQuote(keychainAccount()), securityQuote(keychainService()), securityQuote(string(data)))
		cmd := exec.Command("security", "-i")
		cmd.Stdin = strings.NewReader(line)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("writing to the Keychain: %v (%s)", err, strings.TrimSpace(stderr.String()))
		}
		return nil
	}
	return paths.WriteFileAtomic(paths.CredFile, data, 0o600)
}
