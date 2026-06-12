package cli

import (
	"slices"
	"strings"
	"testing"
)

// fixture builds an App shaped like a real one: a profile source plus commands
// exercising every argument kind (profile, file, override, static values, none)
// and flags. The completion and dispatch tests run against it.
func fixture(profiles ...string) *App {
	a := New("demo")
	a.Version = "9.9.9"
	a.Tagline = "demo app"
	a.SetProfileSource(func() []string { return profiles })
	a.Add(
		&Command{Name: "save", Usage: "[name]", Summary: "Save it", Run: ok},
		&Command{Name: "switch", Aliases: []string{"use"}, Summary: "Switch",
			Args: []ArgKind{ArgProfile}, Run: ok},
		&Command{Name: "remove", Aliases: []string{"rm"}, Summary: "Remove",
			Args: []ArgKind{ArgProfile}, Run: ok},
		&Command{Name: "rename", Summary: "Rename",
			Args: []ArgKind{ArgProfile, ArgProfile}, Run: ok},
		&Command{Name: "list", Summary: "List",
			Flags: []Flag{{Name: "--json", Desc: "json"}}, Run: ok},
		&Command{Name: "export", Summary: "Export",
			Flags: []Flag{{Name: "--all", Desc: "all"}, {Name: "--passphrase", Desc: "pw"}},
			ArgKindOverride: func(pos int, has func(string) bool) ArgKind {
				if has("--all") {
					return ArgFile
				}
				if pos == 0 {
					return ArgProfile
				}
				return ArgFile
			}, Run: ok},
		&Command{Name: "import", Summary: "Import", Args: []ArgKind{ArgFile}, Run: ok},
	)
	return a
}

func ok(Ctx) error { return nil }

// names strips the "\tdescription" suffix from candidates.
func names(cands []string) []string {
	out := make([]string, len(cands))
	for i, c := range cands {
		out[i], _, _ = strings.Cut(c, "\t")
	}
	return out
}

func TestCompleteFirstWord(t *testing.T) {
	a := fixture()
	cands, dir := a.complete([]string{"re"})
	got := names(cands)
	for _, want := range []string{"remove", "rename"} {
		if !slices.Contains(got, want) {
			t.Errorf("first-word completion of %q missing %q; got %v", "re", want, got)
		}
	}
	if slices.Contains(got, "save") {
		t.Errorf("prefix %q should not match save; got %v", "re", got)
	}
	if dir != dirNoFile {
		t.Errorf("directive = %q, want %q", dir, dirNoFile)
	}
}

func TestCompleteIncludesBuiltins(t *testing.T) {
	a := fixture()
	cands, _ := a.complete([]string{"c"})
	if got := names(cands); !slices.Contains(got, "completion") {
		t.Errorf("first-word completion of %q should include the built-in completion; got %v", "c", got)
	}
}

func TestCompleteProfileArg(t *testing.T) {
	a := fixture("work", "personal", "play")

	cands, dir := a.complete([]string{"switch", ""})
	if got, want := names(cands), []string{"personal", "play", "work"}; !slices.Equal(got, want) {
		t.Errorf("switch candidates = %v, want %v", got, want)
	}
	if dir != dirNoFile {
		t.Errorf("directive = %q, want %q", dir, dirNoFile)
	}

	// Prefix filtering, through the `use` alias.
	cands, _ = a.complete([]string{"use", "p"})
	if got, want := names(cands), []string{"personal", "play"}; !slices.Equal(got, want) {
		t.Errorf("use p candidates = %v, want %v", got, want)
	}

	// A second positional to switch is not a profile name.
	if cands, _ := a.complete([]string{"switch", "work", ""}); len(cands) != 0 {
		t.Errorf("second switch positional should yield no profiles; got %v", names(cands))
	}
}

func TestCompleteRenameBothArgs(t *testing.T) {
	a := fixture("work", "personal")
	for _, args := range [][]string{
		{"rename", ""},
		{"rename", "work", "p"},
	} {
		if cands, _ := a.complete(args); len(cands) == 0 {
			t.Errorf("rename %v offered no profiles", args)
		}
	}
}

func TestCompleteFlags(t *testing.T) {
	a := fixture()
	cands, dir := a.complete([]string{"export", "--"})
	if got, want := names(cands), []string{"--all", "--passphrase"}; !slices.Equal(got, want) {
		t.Errorf("export flags = %v, want %v", got, want)
	}
	if dir != dirNoFile {
		t.Errorf("directive = %q, want %q", dir, dirNoFile)
	}
}

func TestCompleteFileFallback(t *testing.T) {
	a := fixture("work")
	if _, dir := a.complete([]string{"import", ""}); dir != dirDefault {
		t.Errorf("import directive = %q, want %q", dir, dirDefault)
	}
	if _, dir := a.complete([]string{"export", "work", ""}); dir != dirDefault {
		t.Errorf("export file directive = %q, want %q", dir, dirDefault)
	}
	// With --all, the lone positional is the file, not a profile name.
	if cands, dir := a.complete([]string{"export", "--all", ""}); len(cands) != 0 || dir != dirDefault {
		t.Errorf("export --all = (%v, %q), want (nil, %q)", names(cands), dir, dirDefault)
	}
}

func TestCompleteStaticArgValues(t *testing.T) {
	a := fixture()
	cands, dir := a.complete([]string{"completion", ""})
	if got, want := names(cands), []string{"bash", "fish", "powershell", "zsh"}; !slices.Equal(got, want) {
		t.Errorf("completion shell candidates = %v, want %v", got, want)
	}
	if dir != dirNoFile {
		t.Errorf("directive = %q, want %q", dir, dirNoFile)
	}
}

func TestParseRequest(t *testing.T) {
	cases := []struct {
		in   []string
		want []string
	}{
		{[]string{"switch", "--cur="}, []string{"switch", ""}},
		{[]string{"switch", "--cur=wo"}, []string{"switch", "wo"}},
		{[]string{"--cur=re"}, []string{"re"}},
		{[]string{"export", "--cur=--al"}, []string{"export", "--al"}},
		{[]string{"switch", "wo"}, []string{"switch", "wo"}},
		{nil, nil},
	}
	for _, c := range cases {
		if got := parseRequest(c.in); !slices.Equal(got, c.want) {
			t.Errorf("parseRequest(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestReplyEmptyPartialOffersProfiles(t *testing.T) {
	// The wrapped empty partial word must route back to profile completion —
	// the case that exposed the PowerShell empty-argument bug.
	a := fixture("work", "personal")
	got, dir := a.complete(parseRequest([]string{"switch", "--cur="}))
	if want := []string{"personal", "work"}; !slices.Equal(names(got), want) {
		t.Errorf("switch (empty partial) = %v, want %v", names(got), want)
	}
	if dir != dirNoFile {
		t.Errorf("directive = %q, want %q", dir, dirNoFile)
	}
}

func TestShellScripts(t *testing.T) {
	a := fixture()
	for _, sh := range []string{"bash", "zsh", "fish", "powershell", "pwsh"} {
		s, err := a.shellScript(sh)
		if err != nil {
			t.Errorf("shellScript(%q) error: %v", sh, err)
		}
		if !strings.Contains(s, a.Name) {
			t.Errorf("shellScript(%q) does not mention the binary name", sh)
		}
	}
	if _, err := a.shellScript("tcsh"); err == nil {
		t.Error("shellScript(tcsh) should error on an unsupported shell")
	}
}

func TestDispatch(t *testing.T) {
	a := fixture()
	var ran string
	var afterRan bool
	a.After = func(c *Command, _ Ctx) { afterRan = true }
	a.index["save"].Run = func(c Ctx) error { ran = "save:" + c.Arg(0); return nil }

	if err := a.Run([]string{"save", "work"}); err != nil {
		t.Fatal(err)
	}
	if ran != "save:work" {
		t.Errorf("dispatch ran = %q, want save:work", ran)
	}
	if !afterRan {
		t.Error("After hook should run for a non-Meta command")
	}

	// Meta commands skip the After hook.
	afterRan = false
	if err := a.Run([]string{"version"}); err != nil {
		t.Fatal(err)
	}
	if afterRan {
		t.Error("After hook must be skipped for Meta commands")
	}
}

func TestParseFlagsAndPositionals(t *testing.T) {
	ctx := parse([]string{"work", "--json", "-", "--ALL"})
	if want := []string{"work", "-"}; !slices.Equal(ctx.Pos, want) {
		t.Errorf("positionals = %v, want %v (a lone '-' is positional)", ctx.Pos, want)
	}
	if !ctx.Has("--json") || !ctx.Has("--all") {
		t.Errorf("flags not parsed case-insensitively: %v", ctx.flags)
	}
}
