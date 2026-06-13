package install

import (
	"github.com/Samseys/anthropic-account-switcher/internal/cli"
	"github.com/Samseys/anthropic-account-switcher/internal/paths"
)

func Commands() []*cli.Command {
	return []*cli.Command{
		{
			Name:    "register",
			Summary: "Add '" + paths.Bin + "' to your PATH",
			Details: "Install '" + paths.Bin + "' onto your PATH so it is runnable from any shell,\n" +
				"and set up tab-completion. Run this once after downloading the binary.",
			Run: func(cli.Ctx) error { return Register() },
		},
		{
			Name: "unregister", Aliases: []string{"uninstall"},
			Summary: "Remove it from your PATH",
			Details: "Undo 'register': take '" + paths.Bin + "' off your PATH and remove the\n" +
				"completion setup. Saved profiles are kept unless you add --purge, which also\n" +
				"deletes every stored profile.",
			Flags: []cli.Flag{{Name: "--purge", Desc: "Also delete saved profiles"}},
			Run:   func(c cli.Ctx) error { return Unregister(c.Has("--purge")) },
		},
	}
}
