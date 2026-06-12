// Command acc-claude switches between multiple Anthropic (Claude Code)
// accounts. It snapshots the OAuth credentials and the cached identity
// (oauthAccount + userID in ~/.claude.json) into named profiles and restores
// them on demand.
//
// Single static binary, no runtime dependencies: on macOS it shells out to the
// built-in `security` CLI for the Keychain; on Windows it edits the user PATH
// directly in the registry during `register`. Nothing to install.
//
// This file is just the command-line front end: it assembles the command table
// from each package's own Commands() and hands it to the internal/cli framework,
// which drives dispatch, help, and shell completion from that one table. The
// work — and each command's own declaration — lives in the internal/ packages:
// profile (the account commands), store (credential I/O), install (register/
// PATH), update (release version check), and paths (shared config).
package main

import (
	"os"

	"github.com/Samseys/anthropic-account-switcher/internal/cli"
	"github.com/Samseys/anthropic-account-switcher/internal/install"
	"github.com/Samseys/anthropic-account-switcher/internal/paths"
	"github.com/Samseys/anthropic-account-switcher/internal/profile"
	"github.com/Samseys/anthropic-account-switcher/internal/update"
)

func main() {
	if err := buildApp().Run(os.Args[1:]); err != nil {
		paths.Die("%s", err)
	}
}

// buildApp constructs the command registry. It is a function (not inline in
// main) so tests can exercise the real table's dispatch and completion.
func buildApp() *cli.App {
	app := cli.New(paths.Bin)
	app.Version = paths.VersionString()
	app.Tagline = "switch between Anthropic (Claude Code) accounts."
	app.Notes = []string{
		"Claude Code picks up the switch on its next request; no restart needed.",
		"Switching re-saves the account you are leaving, so its tokens stay fresh.",
	}
	// After a normal command succeeds, nudge about a new version at most once a
	// day. Skipped for Meta commands (update/version/help/completion) by the
	// framework, and suppressed here when output is machine-readable (--json).
	app.After = func(_ *cli.Command, ctx cli.Ctx) {
		if !ctx.Has("--json") {
			update.MaybeNotify()
		}
	}

	// Each package declares its own commands next to their handlers; main just
	// assembles them, in the order they should appear in help.
	app.Add(profile.Commands()...)
	app.Add(install.Commands()...)
	app.Add(update.Commands()...)
	return app
}
