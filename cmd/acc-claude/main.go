// Command acc-claude switches between multiple Anthropic (Claude Code) accounts
// by snapshotting OAuth credentials and cached identity into named profiles.
// Single static binary; macOS shells out to `security` for Keychain access,
// Windows edits the user PATH in the registry during `register`.
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

// buildApp is extracted from main so tests can exercise the command registry.
func buildApp() *cli.App {
	app := cli.New(paths.Bin)
	app.Version = paths.VersionString()
	app.Tagline = "switch between Anthropic (Claude Code) accounts."
	app.Notes = []string{
		"Claude Code picks up the switch on its next request; no restart needed.",
		"Switching re-saves the account you are leaving, so its tokens stay fresh.",
	}
	// Passive version nudge, at most once a day; skip for machine-readable output.
	app.After = func(_ *cli.Command, ctx cli.Ctx) {
		if !ctx.Has("--json") {
			update.MaybeNotify()
		}
	}

	app.Add(profile.Commands()...)
	app.Add(install.Commands()...)
	app.Add(update.Commands()...)
	return app
}
