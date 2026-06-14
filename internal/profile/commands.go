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
			Details: "Snapshot the live account's OAuth credentials and cached identity into a\n" +
				"named profile. Defaults to the account's email; updates the existing\n" +
				"profile in place if this account is already saved.",
			Run: func(c cli.Ctx) error { return Save(c.Arg(0)) },
		},
		{
			Name: "list", Aliases: []string{"ls"}, Usage: "[--json] [--refresh]",
			Summary: "List saved profiles",
			Details: "List every saved profile with its email and save time; * marks the active\n" +
				"account. Stale or token-expired profiles are refreshed from Anthropic's\n" +
				"usage endpoint, reusing a still-valid token. The active account's token is\n" +
				"left to Claude Code, so only it can show [token expired].\n\n" +
				"--refresh forces a live usage poll of every account.",
			Flags: []cli.Flag{
				{Name: "--json", Desc: "Machine-readable output"},
				{Name: "--refresh", Desc: "Poll the usage endpoint for current usage"},
			},
			Run: func(c cli.Ctx) error { return List(c.Has("--json"), c.Has("--refresh")) },
		},
		{
			Name: "switch", Aliases: []string{"use"}, Usage: "[name|-]",
			Summary: "Switch to a profile",
			Details: "Restore a profile's credentials and identity, making it active. Pass '-' to\n" +
				"return to the previous profile, or no name to toggle between two saved\n" +
				"profiles. The account you leave is re-saved first to keep its rotated\n" +
				"tokens fresh.",
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
			Details: "Export one profile (or all with --all) to a portable bundle, written to a\n" +
				"file or stdout. Plaintext unless --passphrase encrypts it for safe\n" +
				"transfer between machines.",
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
			Details: "Import profiles from an 'export' bundle. Existing profiles are kept unless\n" +
				"--overwrite replaces name collisions. Prompts for the passphrase if the\n" +
				"bundle is encrypted.",
			Flags:    []cli.Flag{{Name: "--overwrite", Desc: "Replace existing profiles"}},
			Complete: cli.Args(files), // the bundle file
			Run:      func(c cli.Ctx) error { return Import(c.Arg(0), c.Has("--overwrite")) },
		},
		{
			Name: "usage", Usage: "[--all] [--json]",
			Summary: "Show the last recorded rate-limit usage",
			Details: "Show how much of the 5-hour and 7-day rate-limit windows the active account\n" +
				"has used, as last recorded by the 'statusline' sensor (no network call).\n" +
				"With --all, report every profile from its cached reading. Empty until\n" +
				"'statusline' has run once — see 'help statusline'.",
			Flags: []cli.Flag{
				{Name: "--all", Desc: "Report every saved profile"},
				{Name: "--json", Desc: "Machine-readable output"},
			},
			Run: func(c cli.Ctx) error { return Usage(c.Has("--json"), c.Has("--all")) },
		},
		{
			Name: "autoswitch", Aliases: []string{"watch"}, Usage: "[5h% [7d%]] [--once] [--dry-run]",
			Summary: "Auto-switch accounts when usage hits a threshold",
			Details: "Watch the active account and, when its 5-hour or 7-day window crosses a trip\n" +
				"threshold, switch to the profile with the most headroom. The local\n" +
				"'statusline' sensor is precise so it trips at 99%; the polled usage endpoint\n" +
				"is coarser, tripping at 95% (5h) / 98% (7d). The endpoint is the fallback\n" +
				"when no fresh sensor data exists (e.g. the VSCode panel). Candidates are\n" +
				"always ranked by current endpoint usage, since inactive accounts never run\n" +
				"the sensor.\n\n" +
				"--once checks once then exits (for cron); --dry-run reports without\n" +
				"switching. Positional thresholds override the defaults: one value covers\n" +
				"both windows ('autoswitch 85'), two set 5h then 7d ('autoswitch 90 96').",
			Flags: []cli.Flag{
				{Name: "--once", Desc: "Check once (switch if tripped) and exit"},
				{Name: "--dry-run", Desc: "Report the decision without switching"},
			},
			Run: func(c cli.Ctx) error {
				five, seven, err := parseThresholds(c.Arg(0), c.Arg(1))
				if err != nil {
					return err
				}
				return AutoSwitch(AutoSwitchOptions{
					FiveHour: five,
					SevenDay: seven,
					Once:     c.Has("--once"),
					DryRun:   c.Has("--dry-run"),
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
				"With no flags it is the sensor: Claude Code pipes session JSON on stdin, it\n" +
				"prints the status-bar line and records the active account's usage locally.\n" +
				"You normally never run this form by hand.",
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
