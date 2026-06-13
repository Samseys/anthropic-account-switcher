package profile

import (
	"fmt"

	"github.com/Samseys/anthropic-account-switcher/internal/cli"
	"github.com/Samseys/anthropic-account-switcher/internal/install"
)

// Commands returns the account-profile subcommands. Each is declared next to
// its handler so the command surface lives with its implementation.
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
			Name: "list", Aliases: []string{"ls"}, Usage: "[--json] [--refresh]",
			Summary: "List saved profiles",
			Details: "List every saved profile with its email and when it was saved; * marks the\n" +
				"currently active account. A profile whose usage is stale or whose access\n" +
				"token has expired is refreshed automatically from Anthropic's usage endpoint,\n" +
				"reusing the existing access token when it is still valid and only renewing it\n" +
				"when it has (nearly) expired. The active account's token is left to Claude\n" +
				"Code, so it alone can still show [token expired].\n\n" +
				"--refresh forces a live usage poll of every account regardless of staleness.",
			Flags: []cli.Flag{
				{Name: "--json", Desc: "Machine-readable output"},
				{Name: "--refresh", Desc: "Poll the usage endpoint for current usage"},
			},
			Run: func(c cli.Ctx) error { return List(c.Has("--json"), c.Has("--refresh")) },
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
			// --all shifts the positionals (lone arg is the file, not the profile),
			// so completion must branch on the flag rather than use a fixed Args list.
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
		{
			Name: "usage", Usage: "[--all] [--json]",
			Summary: "Show the last recorded rate-limit usage",
			Details: "Show how much of the 5-hour and 7-day rate-limit windows the active account\n" +
				"has consumed, as last recorded by the 'statusline' sensor (no network\n" +
				"call). With --all, report every saved profile from its cached reading. Empty\n" +
				"until the statusline command has run at least once — see 'help statusline'.",
			Flags: []cli.Flag{
				{Name: "--all", Desc: "Report every saved profile"},
				{Name: "--json", Desc: "Machine-readable output"},
			},
			Run: func(c cli.Ctx) error { return Usage(c.Has("--json"), c.Has("--all")) },
		},
		{
			Name: "autoswitch", Aliases: []string{"watch"}, Usage: "[threshold] [--week] [--once] [--dry-run]",
			Summary: "Auto-switch accounts when usage hits a threshold",
			Details: "Watch the active account's usage and, when its 5-hour window crosses the\n" +
				"threshold (default 90%), switch to the saved profile with the most headroom.\n" +
				"It prefers the reading the 'statusline' sensor records locally (free, never\n" +
				"rate-limited; see 'help statusline') and falls back to Anthropic's usage\n" +
				"endpoint when no fresh sensor data is available — e.g. the VSCode panel,\n" +
				"which never runs status-line commands. Candidate accounts are always ranked\n" +
				"by their current endpoint usage, since inactive accounts never run the sensor.\n\n" +
				"--week also trips on the 7-day window; --once checks a single time and\n" +
				"exits (for cron); --dry-run reports the decision without switching. The\n" +
				"threshold is an optional positional ('autoswitch 85').",
			Flags: []cli.Flag{
				{Name: "--week", Desc: "Also switch on the 7-day window"},
				{Name: "--once", Desc: "Check once and exit"},
				{Name: "--dry-run", Desc: "Report the decision without switching"},
			},
			Run: func(c cli.Ctx) error {
				threshold, err := parseThreshold(c.Arg(0))
				if err != nil {
					return err
				}
				return AutoSwitch(AutoSwitchOptions{
					Threshold: threshold,
					Week:      c.Has("--week"),
					Once:      c.Has("--once"),
					DryRun:    c.Has("--dry-run"),
				})
			},
		},
		{
			Name: "statusline", Meta: true, Usage: "[--install|--uninstall]",
			Summary: "Status-line sensor for usage / autoswitch",
			Details: "Powers the usage status line that 'usage' and 'autoswitch' read from.\n\n" +
				"  --install     add it to ~/.claude/settings.json (preserves other settings;\n" +
				"                won't overwrite a custom status line)\n" +
				"  --uninstall   remove it again\n\n" +
				"With no flags it is the sensor itself: Claude Code pipes its session JSON on\n" +
				"stdin, and it prints the status-bar line and records the active account's\n" +
				"usage locally. You normally never run this form by hand.",
			Flags: []cli.Flag{
				{Name: "--install", Desc: "Add the status line to settings.json"},
				{Name: "--uninstall", Desc: "Remove the status line from settings.json"},
			},
			Run: func(c cli.Ctx) error {
				switch {
				case c.Has("--install"):
					msg, err := install.InstallStatusLine()
					if err != nil {
						return err
					}
					fmt.Print(msg)
					return nil
				case c.Has("--uninstall"):
					msg, err := install.UninstallStatusLine()
					if err != nil {
						return err
					}
					if msg == "" {
						msg = "No usage status line was configured.\n"
					}
					fmt.Print(msg)
					return nil
				default:
					return StatusLine()
				}
			},
		},
	}
}

// profiles reads the saved profile list fresh on each call so completion
// reflects the current disk state; files defers to the shell's filename completion.
func profiles() ([]string, bool) { return Names(), false }
func files() ([]string, bool)    { return nil, true }
