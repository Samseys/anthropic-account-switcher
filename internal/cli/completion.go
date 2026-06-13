package cli

import (
	"fmt"
	"os"
	"sort"
	"strings"
)

// Directive tokens tell the shell what to do when our candidates do not apply.
const (
	dirNoFile  = ":nofile"  // do not fall back to filename completion
	dirDefault = ":default" // let the shell do its default (file) completion
)

// curMarker wraps the word under the cursor as the final argument the shell
// snippets pass (e.g. "--cur=" for an empty partial word). It exists because
// Windows PowerShell 5.1 silently drops bare empty-string arguments to native
// executables, so "completing a fresh, empty word" could not otherwise be
// transmitted. Wrapping keeps the partial word non-empty and makes the protocol
// identical across bash, zsh, fish and PowerShell.
const curMarker = "--cur="

// parseRequest unwraps the "--cur=<partial>" marker from the last word.
// If absent, args is used as-is (handy for debugging).
func parseRequest(args []string) []string {
	if n := len(args); n > 0 && strings.HasPrefix(args[n-1], curMarker) {
		partial := strings.TrimPrefix(args[n-1], curMarker)
		return append(args[:n-1:n-1], partial)
	}
	return args
}

// complete returns candidates for words (last = partial word). Candidates may
// be "name\tdescription"; the second return is the fallback directive.
func (a *App) complete(words []string) ([]string, string) {
	if len(words) == 0 {
		words = []string{""}
	}
	toComplete := words[len(words)-1]
	typed := words[:len(words)-1]

	if len(typed) == 0 { // first word: complete the subcommand name
		var out []string
		for _, c := range a.all() {
			if c.Hidden {
				continue
			}
			if strings.HasPrefix(c.Name, toComplete) {
				out = append(out, c.Name+"\t"+firstLine(c.Summary))
			}
		}
		return out, dirNoFile
	}

	cmd := a.lookup(typed[0])
	if cmd == nil {
		return nil, dirNoFile
	}

	if strings.HasPrefix(toComplete, "-") { // partial flag
		var out []string
		for _, f := range cmd.Flags {
			if strings.HasPrefix(f.Name, toComplete) {
				out = append(out, f.Name+"\t"+f.Desc)
			}
		}
		return out, dirNoFile
	}

	// Count earlier positionals (skipping flags) to find the current index.
	pos := 0
	has := map[string]bool{}
	for _, w := range typed[1:] {
		if len(w) > 1 && strings.HasPrefix(w, "-") {
			has[strings.ToLower(w)] = true
		} else {
			pos++
		}
	}

	if cmd.Complete == nil {
		return nil, dirNoFile
	}
	candidates, files := cmd.Complete(CompRequest{
		Pos:  pos,
		Word: toComplete,
		Has:  func(f string) bool { return has[strings.ToLower(f)] },
	})
	if files {
		return nil, dirDefault
	}
	return prefixFilter(candidates, toComplete), dirNoFile
}

// reply prints candidates then the directive; it is the body of __complete.
func (a *App) reply(words []string) {
	cands, directive := a.complete(parseRequest(words))
	for _, c := range cands {
		fmt.Println(c)
	}
	fmt.Println(directive)
}

func prefixFilter(values []string, prefix string) []string {
	var out []string
	for _, v := range values {
		if strings.HasPrefix(v, prefix) {
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}

// completionScript prints the snippet for shell, or usage and exits 1 if empty.
func (a *App) completionScript(shell string) error {
	if shell == "" {
		b := a.Name
		fmt.Fprintf(os.Stderr, "Usage: %s completion <bash|zsh|fish|powershell>\n\n", b)
		fmt.Fprintln(os.Stderr, "Load it for the current session, e.g.:")
		fmt.Fprintf(os.Stderr, "  bash:        source <(%s completion bash)\n", b)
		fmt.Fprintf(os.Stderr, "  zsh:         source <(%s completion zsh)\n", b)
		fmt.Fprintf(os.Stderr, "  fish:        %s completion fish | source\n", b)
		fmt.Fprintf(os.Stderr, "  powershell:  %s completion powershell | Out-String | Invoke-Expression\n", b)
		fmt.Fprintln(os.Stderr, "\nTo make it permanent, add the same line to your shell startup file")
		fmt.Fprintln(os.Stderr, "(~/.bashrc, ~/.zshrc, ~/.config/fish/config.fish, or your PowerShell $PROFILE).")
		os.Exit(1)
	}
	script, err := a.shellScript(shell)
	if err != nil {
		return err
	}
	fmt.Print(script)
	return nil
}

// CompletionScript returns the static completion snippet for binName and shell.
// It is exported so the installer can materialize the script to a file and
// dot-source it from the shell startup file, rather than executing live command
// output at every startup (which AMSI/antivirus flags). The script defers to the
// running binary at completion time, so a file written once never goes stale.
func CompletionScript(binName, shell string) (string, error) {
	return (&App{Name: binName}).shellScript(shell)
}

func (a *App) shellScript(shell string) (string, error) {
	b := a.Name
	switch strings.ToLower(shell) {
	case "bash":
		return fmt.Sprintf(bashScript, b, b, b), nil
	case "zsh":
		return fmt.Sprintf(zshScript, b, b, b, b), nil
	case "fish":
		return fmt.Sprintf(fishScript, b, b, b), nil
	case "powershell", "pwsh":
		return fmt.Sprintf(powershellScript, b, b), nil
	default:
		return "", fmt.Errorf("unsupported shell %q (want bash, zsh, fish, or powershell)", shell)
	}
}

// %[1]s etc. are the binary name. Each snippet passes "--cur=<partial>" as the
// last arg (see curMarker), calls __complete, strips the trailing directive, and
// falls back to filename completion on ":default".

const bashScript = `# bash completion for %[1]s
_%[2]s_complete() {
    local IFS=$'\n'
    local cur="${COMP_WORDS[COMP_CWORD]}"
    local words=("${COMP_WORDS[@]:1:COMP_CWORD-1}")
    local out directive lines line
    out="$(%[3]s __complete "${words[@]}" "--cur=$cur" 2>/dev/null)"
    directive="$(printf '%%s\n' "$out" | tail -n1)"
    lines="$(printf '%%s\n' "$out" | sed '$d')"
    if [[ "$directive" == ":default" && -z "$lines" ]]; then
        COMPREPLY=( $(compgen -f -- "$cur") )
        return
    fi
    local cands=()
    while IFS= read -r line; do
        [[ -z "$line" ]] && continue
        cands+=("${line%%%%$'\t'*}")
    done <<< "$lines"
    COMPREPLY=( $(compgen -W "${cands[*]}" -- "$cur") )
}
complete -F _%[2]s_complete %[1]s
`

const zshScript = `#compdef %[1]s
_%[2]s_complete() {
    local -a args lines cands
    local directive line cur
    cur="${words[CURRENT]}"
    args=("${words[2,CURRENT-1]}")
    lines=("${(@f)$(%[3]s __complete "${args[@]}" "--cur=$cur" 2>/dev/null)}")
    directive="${lines[-1]}"
    lines=("${lines[1,-2]}")
    if [[ "$directive" == ":default" && ${#lines} -eq 0 ]]; then
        _files
        return
    fi
    for line in "${lines[@]}"; do
        [[ -z "$line" ]] && continue
        cands+=("${line%%%%$'\t'*}")
    done
    compadd -- "${cands[@]}"
}
compdef _%[4]s_complete %[1]s
`

const fishScript = `# fish completion for %[1]s
function __%[2]s_complete
    set -l args (commandline -opc)
    set -e args[1]
    set -l cur (commandline -ct)
    set -l out (%[3]s __complete $args "--cur=$cur" 2>/dev/null)
    test (count $out) -eq 0; and return
    set -l directive $out[-1]
    set -e out[-1]
    for line in $out
        echo $line
    end
    if test "$directive" = ":default"
        __fish_complete_path $cur
    end
end
complete -c %[1]s -f -a '(__%[2]s_complete)'
`

const powershellScript = `# PowerShell completion for %[1]s. Add this to your $PROFILE.
Register-ArgumentCompleter -Native -CommandName %[1]s -ScriptBlock {
    param($wordToComplete, $commandAst, $cursorPosition)
    # CommandElements includes the word under the cursor when it is non-empty;
    # drop it so only the already-completed words are passed, then send the
    # partial word separately (always non-empty, so PowerShell 5.1 keeps it).
    # Note: PowerShell does not call this completer for a bare '-' or '--' (it
    # handles those itself), so flag names complete only once a letter follows,
    # e.g. '--a' -> '--all'. Command and profile-name completion are unaffected.
    $elements = @($commandAst.CommandElements | Select-Object -Skip 1 | ForEach-Object { $_.ToString() })
    if ($wordToComplete -ne '') {
        if ($elements.Count -gt 1) { $elements = $elements[0..($elements.Count - 2)] } else { $elements = @() }
    }
    $out = @(& %[2]s __complete @elements "--cur=$wordToComplete" 2>$null)
    if ($out.Count -lt 1) { return }
    $lines = if ($out.Count -gt 1) { $out[0..($out.Count - 2)] } else { @() }
    foreach ($line in $lines) {
        if ([string]::IsNullOrEmpty($line)) { continue }
        $parts = $line -split "` + "`" + `t", 2
        $name = $parts[0]
        $desc = if ($parts.Count -gt 1) { $parts[1] } else { $parts[0] }
        [System.Management.Automation.CompletionResult]::new($name, $name, 'ParameterValue', $desc)
    }
}
`
