package profile

import "github.com/Samseys/anthropic-account-switcher/internal/cli"

// Commands returns the account-profile subcommands, each declared next to the
// handler it drives. main wires them in with app.Add(profile.Commands()...),
// so the command surface lives with its implementation rather than in main.
func Commands() []*cli.Command {
	return []*cli.Command{
		{
			Name: "save", Usage: "[name]",
			Summary: "Save the current account as a profile",
			Details: "Snapshot the live Claude Code account (its OAuth credentials and cached\n" +
				"identity) into a named profile. With no name, defaults to the account's\n" +
				"email. If this account is already saved, updates that profile in place\n" +
				"instead of creating a duplicate.",
			Run: func(c cli.Ctx) error { return Save(c.Arg(0)) },
		},
		{
			Name: "list", Aliases: []string{"ls"}, Usage: "[--json]",
			Summary: "List saved profiles",
			Details: "List every saved profile with its email and when it was saved; * marks the\n" +
				"currently active account, and [token expired] flags a profile whose access\n" +
				"token has lapsed (Claude Code refreshes it after you switch).",
			Flags: []cli.Flag{{Name: "--json", Desc: "Machine-readable output"}},
			Run:   func(c cli.Ctx) error { return List(c.Has("--json")) },
		},
		{
			Name: "switch", Aliases: []string{"use"}, Usage: "[name|-]",
			Summary: "Switch to a profile",
			Details: "Restore a profile's credentials and cached identity, making it the active\n" +
				"account. Pass '-' to return to the previously active profile, or no name to\n" +
				"toggle between exactly two saved profiles. The account you are leaving is\n" +
				"re-saved first so its rotated tokens stay fresh.",
			Complete: cli.Args(profiles),
			Run:      func(c cli.Ctx) error { return Switch(c.Arg(0)) },
		},
		{
			Name: "current", Aliases: []string{"whoami"}, Usage: "[--json]",
			Summary: "Show the active account",
			Details: "Show the account Claude Code is currently logged in as: its profile (or\n" +
				"unsaved), email, organization and plan.",
			Flags: []cli.Flag{{Name: "--json", Desc: "Machine-readable output"}},
			Run:   func(c cli.Ctx) error { return Current(c.Has("--json")) },
		},
		{
			Name: "remove", Aliases: []string{"rm"}, Usage: "<name>",
			Summary: "Delete a saved profile",
			Details: "Permanently delete a saved profile. The live account is untouched; this only\n" +
				"removes the stored snapshot.",
			Complete: cli.Args(profiles),
			Run:      func(c cli.Ctx) error { return Remove(c.Arg(0)) },
		},
		{
			Name: "rename", Aliases: []string{"mv"}, Usage: "<old> <new>",
			Summary: "Rename a saved profile",
			Details: "Rename a saved profile, keeping the 'switch -' previous-profile marker in\n" +
				"sync. Fails if a profile with the new name already exists.",
			Complete: cli.Args(profiles, profiles),
			Run:      func(c cli.Ctx) error { return Rename(c.Arg(0), c.Arg(1)) },
		},
		{
			Name: "export", Usage: "<name|--all> [file] [--passphrase]",
			Summary: "Export profiles to a portable bundle",
			Details: "Export one profile (or every profile with --all) to a portable bundle,\n" +
				"written to a file or to stdout when no file is given. The bundle is\n" +
				"plaintext unless --passphrase is set, which encrypts it so it can be moved\n" +
				"safely between machines.",
			Flags: []cli.Flag{
				{Name: "--all", Desc: "Export every saved profile"},
				{Name: "--passphrase", Desc: "Encrypt the bundle"},
			},
			// The flag changes what the positionals mean, so this is a CompleteFunc
			// rather than a fixed Args list: with --all the lone positional is the
			// bundle file; otherwise it is <profile> then the file.
			Complete: func(r cli.CompRequest) ([]string, bool) {
				if r.Has("--all") {
					return cli.Args(files)(r)
				}
				return cli.Args(profiles, files)(r)
			},
			Run: func(c cli.Ctx) error {
				if c.Has("--all") {
					return Export("", true, c.Arg(0), c.Has("--passphrase"))
				}
				return Export(c.Arg(0), false, c.Arg(1), c.Has("--passphrase"))
			},
		},
		{
			Name: "import", Usage: "<file> [--overwrite]",
			Summary: "Import profiles from a bundle",
			Details: "Import profiles from a bundle produced by 'export'. Existing profiles are\n" +
				"kept untouched unless --overwrite is set, which replaces those whose names\n" +
				"collide. You are prompted for the passphrase if the bundle is encrypted.",
			Flags:    []cli.Flag{{Name: "--overwrite", Desc: "Replace existing profiles"}},
			Complete: cli.Args(files), // the bundle file
			Run:      func(c cli.Ctx) error { return Import(c.Arg(0), c.Has("--overwrite")) },
		},
	}
}

// profiles and files are this package's per-argument completers, used with
// cli.Args. profiles reads the saved profile list fresh on each call, so
// completion always reflects what is on disk; files defers to the shell's own
// filename completion.
func profiles() ([]string, bool) { return Names(), false }
func files() ([]string, bool)    { return nil, true }
