# acc-claude

Switch between multiple Anthropic accounts in Claude Code. A single static
binary — no Node, Python, or PowerShell modules.

Useful when you juggle a personal and a work account and don't want to
re-authenticate every time.

## Install

A one-liner that downloads the right binary for your machine, verifies it
against `SHA256SUMS`, and runs `register`:

```powershell
# Windows (PowerShell)
irm https://raw.githubusercontent.com/Samseys/anthropic-account-switcher/main/install.ps1 | iex
```

```bash
# macOS / Linux
curl -fsSL https://raw.githubusercontent.com/Samseys/anthropic-account-switcher/main/install.sh | sh
```

Open a new terminal afterwards so the `PATH` change applies.

### Nightly builds

Every push to `main` publishes a [nightly pre-release](https://github.com/Samseys/anthropic-account-switcher/releases)
(one per build, newest on top; only the latest is kept). It is opt-in:
`acc-claude update` and the default installer always track stable releases, so
you never get nightly by accident. To install it:

```powershell
# Windows (PowerShell)
$env:ACC_CLAUDE_NIGHTLY = '1'; irm https://raw.githubusercontent.com/Samseys/anthropic-account-switcher/main/install.ps1 | iex
```

```bash
# macOS / Linux
curl -fsSL https://raw.githubusercontent.com/Samseys/anthropic-account-switcher/main/install.sh | sh -s -- --nightly
```

### Manual install

Download the binary for your platform from the
[latest release](https://github.com/Samseys/anthropic-account-switcher/releases/latest):

| Platform        | Asset                          |
| --------------- | ------------------------------ |
| Windows x64     | `acc-claude_windows_amd64.exe` |
| Windows ARM64   | `acc-claude_windows_arm64.exe` |
| macOS Intel     | `acc-claude_darwin_amd64`      |
| macOS Apple Si. | `acc-claude_darwin_arm64`      |
| Linux x64       | `acc-claude_linux_amd64`       |
| Linux ARM64     | `acc-claude_linux_arm64`       |

Then run `register` to copy the binary to a per-user location and add it to
your `PATH`:

```bash
# macOS / Linux
mv acc-claude_darwin_arm64 acc-claude && chmod +x acc-claude
./acc-claude register

# Windows (PowerShell)
.\acc-claude_windows_amd64.exe register
```

- **Windows** — installs to `%LOCALAPPDATA%\acc-claude\`. Open a new terminal.
- **macOS / Linux** — installs to `~/.local/bin/` and adds a `PATH` line to your
  shell rc if needed.

`unregister` removes the installed copy (`--purge` also deletes saved profiles).

### Build from source

Needs Go 1.25+. The binary is pure Go (`CGO_ENABLED=0`):

```bash
make build      # compile for the current platform into ./bin
make install    # build, install to the managed location, and register it
make test       # run tests
make dist       # cross-compile all targets + SHA256SUMS into ./dist
```

`make install` installs the build you just compiled exactly as the release
installer would (managed location + PATH + status line), so it's ready to use
immediately. Or `go install github.com/Samseys/anthropic-account-switcher@latest`.

## Usage

```
acc-claude save [name]         # save the current account (defaults to its email)
acc-claude list [--json]       # list saved profiles; * marks the active one
acc-claude switch [name|-]     # switch to a profile
acc-claude current [--json]    # show the active account
acc-claude remove <name>       # delete a saved profile
acc-claude rename <old> <new>  # rename a saved profile
acc-claude usage [--all]       # show last-recorded 5h/7d usage (--all: every profile)
acc-claude autoswitch [pct]    # auto-switch at a usage threshold (default 90%)
acc-claude register            # install onto your PATH (+ usage status line)
acc-claude unregister          # remove it (--purge also deletes saved profiles)
acc-claude update [--check]    # check for a newer release and show how to install it
acc-claude help
```

`switch` shortcuts: with exactly two profiles, a bare `acc-claude switch`
toggles to the other; `acc-claude switch -` returns to the previously active
profile. `--json` makes `list`/`current` scriptable.

### First-time setup

```
# while logged in as account A
acc-claude save work

# log out, log in as account B, then:
acc-claude save personal

# from now on:
acc-claude switch          # the new account is used on Claude Code's next request
```

No restart needed — Claude Code re-reads the credentials on its next request. If
a session is already running, quit it only if the switch doesn't stick: a live
session can overwrite the swapped credentials on its next token refresh.

### Usage limits and auto-switching

Show how much of your 5-hour and 7-day rate-limit windows each account has used,
and switch accounts automatically before you hit a limit.

```
acc-claude usage [--all]      # show 5h/7d usage (--all: every profile)
acc-claude autoswitch [pct]   # switch to a fresher account at a threshold (default 90%)
```

`acc-claude register` adds a usage readout to Claude Code's status bar, which
powers these commands (`unregister` removes it; `acc-claude statusline
--install` / `--uninstall` toggle just the readout). `autoswitch` picks the saved
profile with the most headroom; `--week` also trips on the 7-day window, `--once`
checks a single time, and `--dry-run` reports without switching.

`autoswitch` reads the status-line readout when it is available (the terminal CLI
feeds it for free) and **falls back to Anthropic's usage API** when it isn't — for
example the VSCode panel, which never runs status-line commands. It also polls the
API to read the *current* usage of your other saved accounts (which never run the
status line), so it switches to the one with the most real headroom rather than a
stale guess. The fallback is throttled and only runs while Claude Code is open.

## How it works

A **profile** (stored in `~/.claude/account-profiles/<name>/`) snapshots your
OAuth tokens plus the cached identity from `~/.claude.json`. Live tokens are
read from `~/.claude/.credentials.json` (Windows / Linux) or the login Keychain
(macOS, via the built-in `security` CLI).

Claude Code rotates OAuth tokens in place, so **every `switch` first re-saves
the account you are leaving**, keeping its profile fresh.

`CLAUDE_CONFIG_DIR` is honored the same way Claude Code honors it. The identity
patch surgically replaces only the `oauthAccount` and `userID` values in
`~/.claude.json` (which can contain duplicate keys), leaving the rest
byte-for-byte intact — see [`internal/claudejson`](internal/claudejson).

## Caveats

- **macOS Keychain prompts**: the first `security` read/write may pop a dialog —
  choose "Always Allow". Override the service name via `CLAUDE_KEYCHAIN_SERVICE`.
- Profiles contain live OAuth tokens — treat `~/.claude/account-profiles/` as
  sensitive. On Windows they are DPAPI-encrypted (not portable between
  machines/users); on macOS / Linux they are `0600` plain files.

## License

MIT — see [LICENSE](LICENSE).
