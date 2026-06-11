// Command claude-acc switches between multiple Anthropic (Claude Code)
// accounts. It snapshots the OAuth credentials and the cached identity
// (oauthAccount + userID in ~/.claude.json) into named profiles and restores
// them on demand.
//
// Single static binary, no runtime dependencies: on macOS it shells out to the
// built-in `security` CLI for the Keychain; on Windows it uses the built-in
// `powershell` to edit the user PATH during `register`. Nothing to install.
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
	arg1 := ""
	if len(args) >= 2 {
		arg1 = args[1]
	}

	var err error
	switch strings.ToLower(cmd) {
	case "save":
		err = cmdSave(arg1)
	case "list", "ls":
		err = cmdList()
	case "switch", "use":
		err = cmdSwitch(arg1)
	case "current", "whoami":
		err = cmdCurrent()
	case "remove", "rm":
		err = cmdRemove(arg1)
	case "register":
		err = cmdRegister()
	case "unregister", "uninstall":
		err = cmdUnregister(arg1)
	case "version", "--version", "-v":
		fmt.Println(version)
	case "", "help", "--help", "-h":
		help()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command %q.\n\n", cmd)
		help()
		os.Exit(1)
	}

	if err != nil {
		die("%s", err)
	}
}

func help() {
	fmt.Printf("%s v%s - switch between Anthropic (Claude Code) accounts.\n\n", bin, version)
	fmt.Printf("Usage: %s <command> [name]\n\n", bin)
	fmt.Println("Commands:")
	fmt.Println("  save [name]      Save the current account (defaults to its email as the name)")
	fmt.Println("  list             List saved profiles; * marks the active one")
	fmt.Println("  switch <name>    Switch to a saved profile (then restart Claude Code)")
	fmt.Println("  current          Show the active account (email / org / plan)")
	fmt.Println("  remove <name>    Delete a saved profile")
	fmt.Printf("  register         Install '%s' onto your PATH for any shell\n", bin)
	fmt.Println("  unregister       Remove it (add --purge to delete saved profiles too)")
	fmt.Println("  help             Show this help")
	fmt.Println("  version          Print version")
	fmt.Println()
	fmt.Println("Notes:")
	fmt.Println("  - A switch requires a full restart of Claude Code to take effect.")
}
