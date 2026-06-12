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

// ArgKind says what a positional argument at a given index completes to.
type ArgKind int

const (
	ArgNone    ArgKind = iota // no completion offered
	ArgProfile                // a saved profile name (see SetProfileSource)
	ArgFile                   // defer to the shell's own filename completion
)

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
	Name    string    // canonical name, e.g. "switch"
	Aliases []string  // alternative spellings, e.g. "use"; never shown in help
	Usage   string    // the argument spec shown in help after the name, e.g. "[name|-]"
	Summary string    // help description; may contain '\n' for curated line breaks
	Flags   []Flag    // accepted flags, offered when the partial word starts with '-'
	Args    []ArgKind // positional kinds by index; indices past the end are ArgNone

	// ArgKindOverride, when set, replaces Args for completion: it returns the
	// kind of the positional at index pos given the flags already present. Use
	// it for commands whose argument meaning depends on a flag (e.g. export,
	// where --all turns the first positional into the bundle file).
	ArgKindOverride func(pos int, has func(string) bool) ArgKind

	// ArgValues, when set, lists static completion candidates for the FIRST
	// positional (used for small enums like the shell name of `completion`).
	ArgValues []string

	Hidden bool // omit from help and first-word completion
	Meta   bool // administrative command; the App.After hook is skipped for it

	Run func(Ctx) error
}

// argKind resolves the completion kind for the positional at pos.
func (c *Command) argKind(pos int, has func(string) bool) ArgKind {
	if c.ArgKindOverride != nil {
		return c.ArgKindOverride(pos, has)
	}
	if pos >= 0 && pos < len(c.Args) {
		return c.Args[pos]
	}
	return ArgNone
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
	Name    string   // binary name, e.g. "claude-acc"
	Version string   // shown by the built-in `version` command and in help
	Tagline string   // one-line description in the help header
	Notes   []string // trailing "Notes:" bullets in help

	// After runs after a non-Meta command succeeds, e.g. a passive update check.
	After func(c *Command, ctx Ctx)

	cmds     []*Command // domain commands, in registration order
	builtin  []*Command // help/version/completion, always rendered last
	index    map[string]*Command
	profiles func() []string
}

// all returns the domain commands followed by the built-ins — the order used by
// both help and first-word completion.
func (a *App) all() []*Command { return append(append([]*Command{}, a.cmds...), a.builtin...) }

// completeCmd is the hidden command the shell snippets invoke for candidates.
const completeCmd = "__complete"

// New returns an App pre-populated with the built-in help, version, completion
// and (hidden) __complete commands. Set Version/Tagline/Notes and call Add.
func New(name string) *App {
	a := &App{Name: name, index: map[string]*Command{}, profiles: func() []string { return nil }}
	a.builtin = []*Command{
		{
			Name: "completion", Usage: "<shell>", Meta: true,
			Summary:   "Print a tab-completion script (bash|zsh|fish|powershell)",
			ArgValues: []string{"bash", "zsh", "fish", "powershell"},
			Run:       func(c Ctx) error { return a.completionScript(c.Arg(0)) },
		},
		{
			Name: "help", Aliases: []string{"--help", "-h", ""}, Meta: true,
			Summary: "Show this help",
			Run:     func(Ctx) error { a.Help(); return nil },
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

// SetProfileSource supplies the function completion uses to list saved profile
// names for ArgProfile positionals. Kept injectable so the framework needn't
// import the profile package.
func (a *App) SetProfileSource(f func() []string) { a.profiles = f }

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
}
