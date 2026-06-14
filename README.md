# acc-claude

Switch between multiple Anthropic accounts in Claude Code without logging in
again each time.

`acc-claude` saves each logged-in account as a named **profile** and swaps
between them in one command. It can also switch automatically when an account
gets close to its rate limit, which is useful if you keep a personal and a work
account or move to a fresh account when one runs low.

It's a single binary with no Node, Python, or PowerShell dependencies.

## Quick start

**1. Install** (open a new terminal afterwards so your `PATH` updates):

```powershell
# Windows (PowerShell)
irm https://raw.githubusercontent.com/Samseys/anthropic-account-switcher/main/install.ps1 | iex
```

```bash
# macOS / Linux
curl -fsSL https://raw.githubusercontent.com/Samseys/anthropic-account-switcher/main/install.sh | sh
```

**2. Save the account you're logged into right now:**

```
acc-claude save work
```

**3. Log out of Claude Code, log in as your other account, and save that one too:**

```
acc-claude save personal
```

**4. From now on, switch whenever you like:**

```
acc-claude switch          # toggles to the other account
```

No restart needed; Claude Code picks up the new account on its next request.

> **Tip:** If a switch doesn't seem to stick while a Claude Code session is
> live, quit and reopen it. A running session can overwrite the swapped
> credentials when it next refreshes its token.

## Everyday commands

```
acc-claude save [name]         # save the current account (name defaults to its email)
acc-claude switch [name]       # switch to a profile
acc-claude switch              # with two profiles, toggles to the other one
acc-claude switch -            # switch back to the previously active profile
acc-claude list                # list saved profiles; * marks the active one
acc-claude current             # show which account is active right now
acc-claude rename <old> <new>  # rename a profile
acc-claude remove <name>       # delete a profile
acc-claude usage               # show how much of your rate limits you've used
acc-claude autoswitch          # auto-switch to a fresher account near a limit
acc-claude update              # check for a newer version
acc-claude help
```

Add `--json` to `list` or `current` if you want to script around them.

## Usage limits & auto-switching

Anthropic accounts have a 5-hour and a 7-day rate-limit window. `acc-claude` can
show how much of each you've used and switch you to a fresher account before you
get cut off.

```
acc-claude usage              # 5h / 7d usage for the active account
acc-claude usage --all        # ...for every saved profile
acc-claude autoswitch         # switch to the account with the most headroom near a limit
acc-claude autoswitch 75      # ...trip both windows earlier, at 75%
acc-claude autoswitch 90 96   # ...or set the 5h and 7d windows separately
```

When you install `acc-claude` it adds a small usage readout to Claude Code's
status bar. That readout is what powers these commands, so usage tracking works
without any extra setup.

`autoswitch` watches both windows and, when either crosses its trip threshold,
swaps to the saved account with the most room left. The thresholds depend on the
source: the status-bar reading is precise, so it trips at 99%; the usage API is
coarser and polled, so it trips more conservatively (95% on 5h, 98% on 7d). A
positional argument overrides both sources: one value (`autoswitch 75`) covers
both windows, two (`autoswitch 90 96`) set 5h then 7d. Two flags:

- `--once` runs a single check, switches if a window is over threshold, then
  exits. Use it from cron or Task Scheduler instead of leaving a watcher running.
- `--dry-run` reports the decision without switching.

It reads the status-bar number when available (the terminal CLI provides it for
free) and falls back to Anthropic's usage API when it isn't, such as in the
VSCode panel, which shows no status line. It also checks your other accounts'
real usage, so it switches to one with genuine headroom rather than a stale
guess.

## More install options

### Build from source

Needs Go 1.25+. The binary is pure Go (`CGO_ENABLED=0`):

```bash
make build      # compile into ./bin
make install    # build, install to the managed location, and register it
make test       # run tests
```

`make install` sets things up exactly as the release installer would (PATH +
status line), so it's ready to use immediately. Or:
`go install github.com/Samseys/anthropic-account-switcher@latest`.

### Manual download

Grab the binary for your platform from the
[latest release](https://github.com/Samseys/anthropic-account-switcher/releases/latest):

| Platform        | Asset                          |
| --------------- | ------------------------------ |
| Windows x64     | `acc-claude_windows_amd64.exe` |
| Windows ARM64   | `acc-claude_windows_arm64.exe` |
| macOS Intel     | `acc-claude_darwin_amd64`      |
| macOS Apple Si. | `acc-claude_darwin_arm64`      |
| Linux x64       | `acc-claude_linux_amd64`       |
| Linux ARM64     | `acc-claude_linux_arm64`       |

Then run `register` to install it to a per-user location and add it to your
`PATH`:

```bash
# macOS / Linux  (installs to ~/.local/bin)
mv acc-claude_darwin_arm64 acc-claude && chmod +x acc-claude
./acc-claude register

# Windows  (installs to %LOCALAPPDATA%\acc-claude\)
.\acc-claude_windows_amd64.exe register
```

`acc-claude unregister` removes the installed copy; add `--purge` to also delete
your saved profiles.

### Nightly builds

Every push to `main` publishes a
[nightly pre-release](https://github.com/Samseys/anthropic-account-switcher/releases).
It's opt-in — `acc-claude update` and the default installer always track stable
releases, so you'll never get a nightly by accident:

```powershell
# Windows (PowerShell)
$env:ACC_CLAUDE_NIGHTLY = '1'; irm https://raw.githubusercontent.com/Samseys/anthropic-account-switcher/main/install.ps1 | iex
```

```bash
# macOS / Linux
curl -fsSL https://raw.githubusercontent.com/Samseys/anthropic-account-switcher/main/install.sh | sh -s -- --nightly
```

## How it works

A **profile** lives in `~/.claude/account-profiles/<name>/` and snapshots your
OAuth tokens plus the cached identity from `~/.claude.json`. Live tokens come
from `~/.claude/.credentials.json` (Windows / Linux) or the login Keychain
(macOS).

Claude Code rotates OAuth tokens in place over time, so **every `switch` first
re-saves the account you're leaving** — that way its profile never goes stale.
`CLAUDE_CONFIG_DIR` is honored the same way Claude Code honors it.

## Good to know

- **Your profiles contain live OAuth tokens** — treat
  `~/.claude/account-profiles/` as sensitive. On Windows they're DPAPI-encrypted
  (and not portable between machines or users); on macOS / Linux they're `0600`
  files.
- **macOS Keychain prompts**: the first time `acc-claude` reads or writes the
  Keychain you may get a dialog — choose "Always Allow". You can override the
  service name with `CLAUDE_KEYCHAIN_SERVICE`.

## License

MIT — see [LICENSE](LICENSE).
