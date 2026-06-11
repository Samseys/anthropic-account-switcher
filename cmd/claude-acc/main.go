// Command claude-acc switches between multiple Anthropic (Claude Code)
// accounts. It snapshots the OAuth credentials and the cached identity
// (oauthAccount + userID in ~/.claude.json) into named profiles and restores
// them on demand.
//
// Single static binary, no runtime dependencies: on macOS it shells out to the
// built-in `security` CLI for the Keychain; on Windows it edits the user PATH
// directly in the registry during `register`. Nothing to install.
//
// This file is just the command-line front end; the work lives in the
// internal/ packages: profile (the account commands), store (credential I/O),
// install (register/PATH), update (self-update), and paths (shared config).
package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/Samseys/anthropic-account-switcher/internal/install"
	"github.com/Samseys/anthropic-account-switcher/internal/paths"
	"github.com/Samseys/anthropic-account-switcher/internal/profile"
	"github.com/Samseys/anthropic-account-switcher/internal/update"
)

func main() {
	args := os.Args[1:]
	cmd := ""
	if len(args) >= 1 {
		cmd = args[0]
	}

	// Split the remaining args into flags (--json, --purge, ...) and
	// positionals. A lone "-" is positional: `switch -` means "previous".
	flags := map[string]bool{}
	var pos []string
	var rest []string
	if len(args) > 1 {
		rest = args[1:]
	}
	for _, a := range rest {
		if len(a) > 1 && strings.HasPrefix(a, "-") {
			flags[strings.ToLower(a)] = true
		} else {
			pos = append(pos, a)
		}
	}
	arg := func(i int) string {
		if i < len(pos) {
			return pos[i]
		}
		return ""
	}

	var err error
	switch strings.ToLower(cmd) {
	case "save":
		err = profile.Save(arg(0))
	case "list", "ls":
		err = profile.List(flags["--json"])
	case "switch", "use":
		err = profile.Switch(arg(0))
	case "current", "whoami":
		err = profile.Current(flags["--json"])
	case "remove", "rm":
		err = profile.Remove(arg(0))
	case "rename", "mv":
		err = profile.Rename(arg(0), arg(1))
	case "export":
		// `export --all [file]` vs `export <name> [file]`: with --all the lone
		// positional is the file, otherwise it's the name and the file follows.
		if flags["--all"] {
			err = profile.Export("", true, arg(0), flags["--passphrase"])
		} else {
			err = profile.Export(arg(0), false, arg(1), flags["--passphrase"])
		}
	case "import":
		err = profile.Import(arg(0), flags["--overwrite"])
	case "update", "upgrade", "self-update":
		err = update.Update(flags["--check"], flags["--force"])
	case "register":
		err = install.Register()
	case "unregister", "uninstall":
		err = install.Unregister(flags["--purge"])
	case "version", "--version", "-v":
		fmt.Println(paths.VersionString())
	case "", "help", "--help", "-h":
		help()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command %q.\n\n", cmd)
		help()
		os.Exit(1)
	}

	if err != nil {
		paths.Die("%s", err)
	}

	// Best-effort "a new version is available" nudge, at most once per day.
	// It runs after a successful command except those where it would be noise
	// (update itself, version/help) or would pollute machine-readable output
	// (--json on list/current). The hint goes to stderr, never stdout.
	if notifiesUpdate(cmd) && !flags["--json"] {
		update.MaybeNotify()
	}
}

// notifiesUpdate reports whether the passive update check should run for cmd.
func notifiesUpdate(cmd string) bool {
	switch strings.ToLower(cmd) {
	case "update", "upgrade", "self-update",
		"version", "--version", "-v",
		"", "help", "--help", "-h":
		return false
	default:
		return true
	}
}

func help() {
	fmt.Printf("%s v%s - switch between Anthropic (Claude Code) accounts.\n\n", paths.Bin, paths.VersionString())
	fmt.Printf("Usage: %s <command> [args...]\n\n", paths.Bin)
	fmt.Println("Commands:")
	fmt.Println("  save [name]         Save the current account (defaults to its email as the name)")
	fmt.Println("  list [--json]       List saved profiles; * marks the active one")
	fmt.Println("  switch [name|-]     Switch to a profile ('-' = previous; no name toggles")
	fmt.Println("                      between two saved profiles), then restart Claude Code")
	fmt.Println("  current [--json]    Show the active account (profile / email / org / plan)")
	fmt.Println("  remove <name>       Delete a saved profile")
	fmt.Println("  rename <old> <new>  Rename a saved profile")
	fmt.Println("  export <name|--all> [file] [--passphrase]")
	fmt.Println("                      Export profile(s) to a portable bundle (stdout if no")
	fmt.Println("                      file); --passphrase encrypts it, else it is plaintext")
	fmt.Println("  import <file> [--overwrite]")
	fmt.Println("                      Import profiles from a bundle (--overwrite replaces")
	fmt.Println("                      existing ones)")
	fmt.Printf("  register            Install '%s' onto your PATH for any shell\n", paths.Bin)
	fmt.Println("  unregister          Remove it (add --purge to delete saved profiles too)")
	fmt.Println("  update [--check]    Update to the latest release (--check only reports)")
	fmt.Println("  help                Show this help")
	fmt.Println("  version             Print version")
	fmt.Println()
	fmt.Println("Notes:")
	fmt.Println("  - A switch requires a full restart of Claude Code to take effect.")
	fmt.Println("  - Switching re-saves the account you are leaving, so its tokens stay fresh.")
}
