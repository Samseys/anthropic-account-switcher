# claude-acc

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

### Manual install

Download the binary for your platform from the
[latest release](https://github.com/Samseys/anthropic-account-switcher/releases/latest):

| Platform        | Asset                          |
| --------------- | ------------------------------ |
| Windows x64     | `claude-acc_windows_amd64.exe` |
| Windows ARM64   | `claude-acc_windows_arm64.exe` |
| macOS Intel     | `claude-acc_darwin_amd64`      |
| macOS Apple Si. | `claude-acc_darwin_arm64`      |
| Linux x64       | `claude-acc_linux_amd64`       |
| Linux ARM64     | `claude-acc_linux_arm64`       |

Then run `register` to copy the binary to a per-user location and add it to
your `PATH`:

```bash
# macOS / Linux
mv claude-acc_darwin_arm64 claude-acc && chmod +x claude-acc
./claude-acc register

# Windows (PowerShell)
.\claude-acc_windows_amd64.exe register
```

- **Windows** — installs to `%LOCALAPPDATA%\claude-acc\`. Open a new terminal.
- **macOS / Linux** — installs to `~/.local/bin/` and adds a `PATH` line to your
  shell rc if needed.

`unregister` removes the installed copy (`--purge` also deletes saved profiles).

### Build from source

Needs Go 1.25+. The binary is pure Go (`CGO_ENABLED=0`):

```bash
make build      # compile for the current platform into ./bin
make test       # run tests
make dist       # cross-compile all targets + SHA256SUMS into ./dist
```

Or `go install github.com/Samseys/anthropic-account-switcher@latest`.

## Usage

```
claude-acc save [name]         # save the current account (defaults to its email)
claude-acc list [--json]       # list saved profiles; * marks the active one
claude-acc switch [name|-]     # switch to a profile
claude-acc current [--json]    # show the active account
claude-acc remove <name>       # delete a saved profile
claude-acc rename <old> <new>  # rename a saved profile
claude-acc register            # install onto your PATH
claude-acc unregister          # remove it (--purge also deletes saved profiles)
claude-acc update [--check]    # check for a newer release and show how to install it
claude-acc help
```

`switch` shortcuts: with exactly two profiles, a bare `claude-acc switch`
toggles to the other; `claude-acc switch -` returns to the previously active
profile. `--json` makes `list`/`current` scriptable.

### First-time setup

```
# while logged in as account A
claude-acc save work

# log out, log in as account B, then:
claude-acc save personal

# from now on:
claude-acc switch          # the new account is used on Claude Code's next request
```

No restart needed — Claude Code re-reads the credentials on its next request. If
a session is already running, quit it only if the switch doesn't stick: a live
session can overwrite the swapped credentials on its next token refresh.

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
