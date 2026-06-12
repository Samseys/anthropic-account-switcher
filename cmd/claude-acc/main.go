// Command claude-acc switches between multiple Anthropic (Claude Code)
// accounts. It snapshots the OAuth credentials and the cached identity
// (oauthAccount + userID in ~/.claude.json) into named profiles and restores
// them on demand.
//
// Single static binary, no runtime dependencies: on macOS it shells out to the
// built-in `security` CLI for the Keychain; on Windows it edits the user PATH
// directly in the registry during `register`. Nothing to install.
//
// This file is just the command-line front end: it declares the commands and
// hands them to the internal/cli framework, which drives dispatch, help, and
// shell completion from that one table. The work lives in the internal/
// packages: profile (the account commands), store (credential I/O), install
// (register/PATH), update (self-update), and paths (shared config).
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
		"A switch requires a full restart of Claude Code to take effect.",
		"Switching re-saves the account you are leaving, so its tokens stay fresh.",
	}
	app.SetProfileSource(profile.Names)

	// After a normal command succeeds, nudge about a new version at most once a
	// day. Skipped for Meta commands (update/version/help/completion) by the
	// framework, and suppressed here when output is machine-readable (--json).
	app.After = func(_ *cli.Command, ctx cli.Ctx) {
		if !ctx.Has("--json") {
			update.MaybeNotify()
		}
	}

	app.Add(
		&cli.Command{
			Name: "save", Usage: "[name]",
			Summary: "Save the current account (defaults to its email as the name;\n" +
				"updates the existing profile if this account is already saved)",
			Run: func(c cli.Ctx) error { return profile.Save(c.Arg(0)) },
		},
		&cli.Command{
			Name: "list", Aliases: []string{"ls"}, Usage: "[--json]",
			Summary: "List saved profiles; * marks the active one",
			Flags:   []cli.Flag{{Name: "--json", Desc: "Machine-readable output"}},
			Run:     func(c cli.Ctx) error { return profile.List(c.Has("--json")) },
		},
		&cli.Command{
			Name: "switch", Aliases: []string{"use"}, Usage: "[name|-]",
			Summary: "Switch to a profile ('-' = previous; no name toggles\n" +
				"between two saved profiles), then restart Claude Code",
			Args: []cli.ArgKind{cli.ArgProfile},
			Run:  func(c cli.Ctx) error { return profile.Switch(c.Arg(0)) },
		},
		&cli.Command{
			Name: "current", Aliases: []string{"whoami"}, Usage: "[--json]",
			Summary: "Show the active account (profile / email / org / plan)",
			Flags:   []cli.Flag{{Name: "--json", Desc: "Machine-readable output"}},
			Run:     func(c cli.Ctx) error { return profile.Current(c.Has("--json")) },
		},
		&cli.Command{
			Name: "remove", Aliases: []string{"rm"}, Usage: "<name>",
			Summary: "Delete a saved profile",
			Args:    []cli.ArgKind{cli.ArgProfile},
			Run:     func(c cli.Ctx) error { return profile.Remove(c.Arg(0)) },
		},
		&cli.Command{
			Name: "rename", Aliases: []string{"mv"}, Usage: "<old> <new>",
			Summary: "Rename a saved profile",
			Args:    []cli.ArgKind{cli.ArgProfile, cli.ArgProfile},
			Run:     func(c cli.Ctx) error { return profile.Rename(c.Arg(0), c.Arg(1)) },
		},
		&cli.Command{
			Name: "export", Usage: "<name|--all> [file] [--passphrase]",
			Summary: "Export profile(s) to a portable bundle (stdout if no\n" +
				"file); --passphrase encrypts it, else it is plaintext",
			Flags: []cli.Flag{
				{Name: "--all", Desc: "Export every saved profile"},
				{Name: "--passphrase", Desc: "Encrypt the bundle"},
			},
			// With --all the lone positional is the file; otherwise the first
			// positional is a profile name and the file follows.
			ArgKindOverride: func(pos int, has func(string) bool) cli.ArgKind {
				if has("--all") {
					return cli.ArgFile
				}
				if pos == 0 {
					return cli.ArgProfile
				}
				return cli.ArgFile
			},
			Run: func(c cli.Ctx) error {
				if c.Has("--all") {
					return profile.Export("", true, c.Arg(0), c.Has("--passphrase"))
				}
				return profile.Export(c.Arg(0), false, c.Arg(1), c.Has("--passphrase"))
			},
		},
		&cli.Command{
			Name: "import", Usage: "<file> [--overwrite]",
			Summary: "Import profiles from a bundle (--overwrite replaces\n" +
				"existing ones)",
			Flags: []cli.Flag{{Name: "--overwrite", Desc: "Replace existing profiles"}},
			Args:  []cli.ArgKind{cli.ArgFile},
			Run:   func(c cli.Ctx) error { return profile.Import(c.Arg(0), c.Has("--overwrite")) },
		},
		&cli.Command{
			Name:    "register",
			Summary: "Install '" + paths.Bin + "' onto your PATH for any shell",
			Run:     func(cli.Ctx) error { return install.Register() },
		},
		&cli.Command{
			Name: "unregister", Aliases: []string{"uninstall"},
			Summary: "Remove it (add --purge to delete saved profiles too)",
			Flags:   []cli.Flag{{Name: "--purge", Desc: "Also delete saved profiles"}},
			Run:     func(c cli.Ctx) error { return install.Unregister(c.Has("--purge")) },
		},
		&cli.Command{
			Name: "update", Aliases: []string{"upgrade", "self-update"}, Usage: "[--check]", Meta: true,
			Summary: "Update to the latest release (--check only reports)",
			Flags: []cli.Flag{
				{Name: "--check", Desc: "Only report whether an update is available"},
				{Name: "--force", Desc: "Reinstall even if already current"},
			},
			Run: func(c cli.Ctx) error { return update.Update(c.Has("--check"), c.Has("--force")) },
		},
	)
	return app
}
