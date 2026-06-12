package update

import "github.com/Samseys/anthropic-account-switcher/internal/cli"

// Commands returns the update/upgrade/self-update subcommand.
func Commands() []*cli.Command {
	return []*cli.Command{
		{
			Name: "update", Aliases: []string{"upgrade", "self-update"}, Usage: "[--check] [--force]", Meta: true,
			Summary: "Check for a newer release",
			Details: "Check for a newer release and, if one exists, print the one-line installer\n" +
				"command that upgrades the tool (the binary is never replaced in place).\n" +
				"--force prints the installer command even when already on the latest version.",
			Flags: []cli.Flag{
				{Name: "--check", Desc: "Accepted for compatibility; checking is all this command does"},
				{Name: "--force", Desc: "Show the installer command even if already current"},
			},
			Run: func(c cli.Ctx) error { return Update(c.Has("--check"), c.Has("--force")) },
		},
	}
}
