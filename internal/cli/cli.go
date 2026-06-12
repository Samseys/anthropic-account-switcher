// Package cli is a tiny, dependency-free command framework. You declare each
// command once — its name, aliases, accepted flags, the kinds of its positional
// arguments, and a handler — and the framework drives everything else from that
// single registry: argument parsing and dispatch, the help screen, and shell
// tab-completion (both the hidden __complete callback and the installable
// `completion <shell>` script).
//
// The point is to have one source of truth. Adding a command, a flag, or
// changing what an argument completes to is a single edit to its Command; help
// and completion follow automatically and cannot drift.
package cli

import (
	"fmt"
	"os"
	"strings"
)

// CompRequest describes a positional-argument completion query handed to a
// command's Complete function.
type CompRequest struct {
	Pos  int               // index of the positional being completed (flags skipped)
	Word string            // the partial word currently under the cursor
	Has  func(string) bool // whether the named flag (e.g. "--all") is already present
}

// CompleteFunc returns a command's completion candidates for the positional in
// r. The framework prefix-filters the candidates by the partial word, so a
// command may simply return its full set (e.g. every saved profile name). A true
// second return tells the shell to fall back to its own filename completion, in
// which case the candidates are ignored.
//
// This is the framework's only completion hook: it carries no domain concepts of
// its own (no notion of "profile" or "file"), so each command owns — and can
// compute dynamically — exactly what its arguments complete to.
type CompleteFunc func(r CompRequest) (candidates []string, files bool)

// ArgCompleter completes a single positional argument: its candidates, plus
// whether to fall back to filename completion (the same convention as
// CompleteFunc's second return).
type ArgCompleter func() (candidates []string, files bool)

// Args builds a CompleteFunc from one ArgCompleter per positional index, so each
// argument can complete to a different thing — e.g. Args(profiles, files)
// completes the first argument to profile names and the second to file paths.
// Positionals beyond the provided list complete to nothing. Commands whose
// argument meaning depends on a flag (not just its position) should use a
// CompleteFunc directly instead.
func Args(perPos ...ArgCompleter) CompleteFunc {
	return func(r CompRequest) ([]string, bool) {
		if r.Pos >= 0 && r.Pos < len(perPos) {
			return perPos[r.Pos]()
		}
		return nil, false
	}
}

// Flag is a boolean (presence-only) flag a command accepts, e.g. --json. The
// tool has no value-taking flags, which keeps parsing and completion trivial.
type Flag struct {
	Name string // including dashes, e.g. "--json"
	Desc string // shown as the completion description
}

// Ctx is handed to a command's Run: the parsed positional arguments and the set
// of flags that were present on the command line.
type Ctx struct {
	Pos   []string
	flags map[string]bool
}

// Arg returns the i-th positional argument, or "" if there are fewer than i+1.
func (c Ctx) Arg(i int) string {
	if i >= 0 && i < len(c.Pos) {
		return c.Pos[i]
	}
	return ""
}

// Has reports whether the named flag (e.g. "--json") was present.
func (c Ctx) Has(flag string) bool { return c.flags[strings.ToLower(flag)] }

// Command is one subcommand. The zero value is not useful; at minimum set Name,
// Summary, and Run.
type Command struct {
	Name    string   // canonical name, e.g. "switch"
	Aliases []string // alternative spellings, e.g. "use"; never shown in help
	Usage   string   // the argument spec shown in help after the name, e.g. "[name|-]"
	Summary string   // one-line description for the help list; may contain '\n' for curated breaks
	Details string   // longer prose shown by `help <command>`; falls back to Summary when empty
	Flags   []Flag   // accepted flags, offered when the partial word starts with '-'

	// Complete, when set, computes this command's positional-argument
	// completions (see CompleteFunc). Leave nil for commands whose arguments
	// complete to nothing (e.g. a free-form new name).
	Complete CompleteFunc

	Hidden bool // omit from help and first-word completion
	Meta   bool // administrative command; the App.After hook is skipped for it

	Run func(Ctx) error
}

// invocation is the "name [args]" form shown in help.
func (c *Command) invocation() string {
	if c.Usage == "" {
		return c.Name
	}
	return c.Name + " " + c.Usage
}

// App is a registered set of commands plus the metadata help needs.
type App struct {
	Name    string   // binary name, e.g. "acc-claude"
	Version string   // shown by the built-in `version` command and in help
	Tagline string   // one-line description in the help header
	Notes   []string // trailing "Notes:" bullets in help

	// After runs after a non-Meta command succeeds, e.g. a passive update check.
	After func(c *Command, ctx Ctx)

	cmds    []*Command // domain commands, in registration order
	builtin []*Command // help/version/completion, always rendered last
	index   map[string]*Command
}

// all returns the domain commands followed by the built-ins — the order used by
// both help and first-word completion.
func (a *App) all() []*Command { return append(append([]*Command{}, a.cmds...), a.builtin...) }

// completeCmd is the hidden command the shell snippets invoke for candidates.
const completeCmd = "__complete"

// New returns an App pre-populated with the built-in help, version, completion
// and (hidden) __complete commands. Set Version/Tagline/Notes and call Add.
func New(name string) *App {
	a := &App{Name: name, index: map[string]*Command{}}
	a.builtin = []*Command{
		{
			Name: "completion", Usage: "<shell>", Meta: true,
			Summary: "Print a tab-completion script (bash|zsh|fish|powershell)",
			Complete: func(r CompRequest) ([]string, bool) {
				if r.Pos == 0 {
					return []string{"bash", "zsh", "fish", "powershell"}, false
				}
				return nil, false
			},
			Run: func(c Ctx) error { return a.completionScript(c.Arg(0)) },
		},
		{
			Name: "help", Aliases: []string{"--help", "-h", ""}, Usage: "[command]", Meta: true,
			Summary:  "Show this help, or detailed help for one command",
			Complete: Args(a.commandNames),
			Run: func(c Ctx) error {
				if name := c.Arg(0); name != "" {
					return a.HelpCommand(name)
				}
				a.Help()
				return nil
			},
		},
		{
			Name: "version", Aliases: []string{"--version", "-v"}, Meta: true,
			Summary: "Print version",
			Run:     func(Ctx) error { fmt.Println(a.Version); return nil },
		},
	}
	for _, c := range a.builtin {
		a.indexCmd(c)
	}
	return a
}

// Add registers domain commands (and indexes their names and aliases). A later
// command may intentionally override an earlier registration of the same name.
func (a *App) Add(cmds ...*Command) *App {
	for _, c := range cmds {
		a.cmds = append(a.cmds, c)
		a.indexCmd(c)
	}
	return a
}

// indexCmd maps a command's name and aliases to it for lookup.
func (a *App) indexCmd(c *Command) {
	a.index[strings.ToLower(c.Name)] = c
	for _, al := range c.Aliases {
		a.index[strings.ToLower(al)] = c
	}
}

// lookup resolves a command by name or alias, case-insensitively.
func (a *App) lookup(name string) *Command { return a.index[strings.ToLower(name)] }

// parse splits the words after the command name into positionals and the set of
// present flags. A token longer than one character and starting with '-' is a
// flag; everything else (including a lone "-") is positional.
func parse(args []string) Ctx {
	ctx := Ctx{flags: map[string]bool{}}
	for _, a := range args {
		if len(a) > 1 && strings.HasPrefix(a, "-") {
			ctx.flags[strings.ToLower(a)] = true
		} else {
			ctx.Pos = append(ctx.Pos, a)
		}
	}
	return ctx
}

// Run dispatches the raw argument list (os.Args[1:]). It returns the handler's
// error for the caller to report; unknown commands print help and exit 1.
func (a *App) Run(args []string) error {
	name := ""
	var rest []string
	if len(args) > 0 {
		name, rest = args[0], args[1:]
	}

	// The completion callback needs the raw words (flags inline, plus the
	// --cur marker), so it bypasses the positional/flag split below.
	if strings.EqualFold(name, completeCmd) {
		a.reply(rest)
		return nil
	}

	cmd := a.lookup(name)
	if cmd == nil {
		fmt.Fprintf(os.Stderr, "Unknown command %q.\n\n", name)
		a.Help()
		os.Exit(1)
	}

	ctx := parse(rest)
	if err := cmd.Run(ctx); err != nil {
		return err
	}
	if a.After != nil && !cmd.Meta {
		a.After(cmd, ctx)
	}
	return nil
}

// help layout: the command invocation occupies a fixed field; summaries start
// two columns past it, and continuation lines align to the same column.
const (
	helpField  = 18
	helpIndent = helpField + 4 // 2 leading spaces + field + 2 gap
)

// Help prints the usage screen, generated entirely from the registry.
func (a *App) Help() {
	fmt.Printf("%s v%s - %s\n\n", a.Name, a.Version, a.Tagline)
	fmt.Printf("Usage: %s <command> [args...]\n\n", a.Name)
	fmt.Println("Commands:")
	pad := strings.Repeat(" ", helpIndent)
	for _, c := range a.all() {
		if c.Hidden {
			continue
		}
		lines := strings.Split(c.Summary, "\n")
		if inv := c.invocation(); len(inv) <= helpField {
			fmt.Printf("  %-*s  %s\n", helpField, inv, lines[0])
			lines = lines[1:]
		} else {
			fmt.Printf("  %s\n", inv)
		}
		for _, l := range lines {
			fmt.Println(pad + l)
		}
	}
	if len(a.Notes) > 0 {
		fmt.Println("\nNotes:")
		for _, n := range a.Notes {
			fmt.Printf("  - %s\n", n)
		}
	}
	fmt.Printf("\nRun '%s help <command>' for details on a single command.\n", a.Name)
}

// HelpCommand prints detailed help for a single command — its usage, full
// (possibly multi-line) summary, any aliases and any flags — or returns an error
// if no such command is registered. It backs `<bin> help <command>`.
func (a *App) HelpCommand(name string) error {
	cmd := a.lookup(name)
	if cmd == nil {
		return fmt.Errorf("unknown command %q; run '%s help' for the full list", name, a.Name)
	}
	fmt.Printf("Usage: %s %s\n", a.Name, cmd.invocation())
	if desc := cmd.Details; desc != "" {
		fmt.Printf("\n%s\n", desc)
	} else if cmd.Summary != "" {
		fmt.Printf("\n%s\n", cmd.Summary)
	}
	// Skip the empty-string and dash aliases the built-ins use internally; only
	// real alternative spellings are worth showing.
	var aliases []string
	for _, al := range cmd.Aliases {
		if al != "" && !strings.HasPrefix(al, "-") {
			aliases = append(aliases, al)
		}
	}
	if len(aliases) > 0 {
		fmt.Printf("\nAliases: %s\n", strings.Join(aliases, ", "))
	}
	if len(cmd.Flags) > 0 {
		fmt.Println("\nFlags:")
		for _, f := range cmd.Flags {
			fmt.Printf("  %-14s %s\n", f.Name, f.Desc)
		}
	}
	return nil
}

// commandNames is the ArgCompleter behind `help <command>`: every command a user
// can ask for help on, i.e. the visible (non-Hidden) commands and built-ins.
func (a *App) commandNames() ([]string, bool) {
	var out []string
	for _, c := range a.all() {
		if !c.Hidden {
			out = append(out, c.Name)
		}
	}
	return out, false
}
