// Command claude-acc switches between multiple Anthropic (Claude Code)
// accounts. It snapshots the OAuth credentials and the cached identity
// (oauthAccount + userID in ~/.claude.json) into named profiles and restores
// them on demand.
//
// Single static binary, no runtime dependencies: on macOS it shells out to the
// built-in `security` CLI for the Keychain; on Windows it edits the user PATH
// directly in the registry during `register`. Nothing to install.
package main

import (
	"fmt"
	"os"
	"strings"
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
	for _, a := range args[1:] {
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
		err = cmdSave(arg(0))
	case "list", "ls":
		err = cmdList(flags["--json"])
	case "switch", "use":
		err = cmdSwitch(arg(0))
	case "current", "whoami":
		err = cmdCurrent(flags["--json"])
	case "remove", "rm":
		err = cmdRemove(arg(0))
	case "rename", "mv":
		err = cmdRename(arg(0), arg(1))
	case "update", "upgrade", "self-update":
		err = cmdUpdate(flags["--check"], flags["--force"])
	case "register":
		err = cmdRegister()
	case "unregister", "uninstall":
		err = cmdUnregister(flags["--purge"])
	case "version", "--version", "-v":
		fmt.Println(versionString())
	case "", "help", "--help", "-h":
		help()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command %q.\n\n", cmd)
		help()
		os.Exit(1)
	}

	// Best-effort "a new version is available" nudge, at most once per day.
	// It runs after every command except those where it would be noise
	// (update itself, version/help) or would pollute machine-readable output
	// (--json on list/current). The hint goes to stderr, never stdout.
	if notifiesUpdate(cmd) && !flags["--json"] {
		maybeNotifyUpdate()
	}

	if err != nil {
		die("%s", err)
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
	fmt.Printf("%s v%s - switch between Anthropic (Claude Code) accounts.\n\n", bin, versionString())
	fmt.Printf("Usage: %s <command> [args...]\n\n", bin)
	fmt.Println("Commands:")
	fmt.Println("  save [name]         Save the current account (defaults to its email as the name)")
	fmt.Println("  list [--json]       List saved profiles; * marks the active one")
	fmt.Println("  switch [name|-]     Switch to a profile ('-' = previous; no name toggles")
	fmt.Println("                      between two saved profiles), then restart Claude Code")
	fmt.Println("  current [--json]    Show the active account (profile / email / org / plan)")
	fmt.Println("  remove <name>       Delete a saved profile")
	fmt.Println("  rename <old> <new>  Rename a saved profile")
	fmt.Printf("  register            Install '%s' onto your PATH for any shell\n", bin)
	fmt.Println("  unregister          Remove it (add --purge to delete saved profiles too)")
	fmt.Println("  update [--check]    Update to the latest release (--check only reports)")
	fmt.Println("  help                Show this help")
	fmt.Println("  version             Print version")
	fmt.Println()
	fmt.Println("Notes:")
	fmt.Println("  - A switch requires a full restart of Claude Code to take effect.")
	fmt.Println("  - Switching re-saves the account you are leaving, so its tokens stay fresh.")
}
