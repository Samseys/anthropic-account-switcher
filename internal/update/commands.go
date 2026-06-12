package update

import "github.com/Samseys/anthropic-account-switcher/internal/cli"

// Commands returns the self-update subcommand, declared next to its handler so
// main only wires it in with app.Add(update.Commands()...).
func Commands() []*cli.Command {
	return []*cli.Command{
		{
			Name: "update", Aliases: []string{"upgrade", "self-update"}, Usage: "[--check]", Meta: true,
			Summary: "Update to the latest release",
			Details: "Check for a newer release and install it. Use --check to only report whether\n" +
				"an update is available without installing, or --force to reinstall even when\n" +
				"already on the latest version.",
			Flags: []cli.Flag{
				{Name: "--check", Desc: "Only report whether an update is available"},
				{Name: "--force", Desc: "Reinstall even if already current"},
			},
			Run: func(c cli.Ctx) error { return Update(c.Has("--check"), c.Has("--force")) },
		},
	}
}
