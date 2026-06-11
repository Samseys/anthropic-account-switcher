# claude-acc

Switch between multiple Anthropic accounts in Claude Code — a **single static
binary**, no runtime dependencies. Nothing to install, no Node, no Python, no
PowerShell modules.

Useful when you juggle, say, a personal account and a work account and don't
want to re-authenticate every time.

## Install

Download the binary for your platform from the
[latest release](https://github.com/Samseys/anthropic-account-switcher/releases/latest),
then put it on your PATH.

| Platform        | Asset                              |
| --------------- | ---------------------------------- |
| Windows x64     | `claude-acc_windows_amd64.exe`     |
| Windows ARM64   | `claude-acc_windows_arm64.exe`     |
| macOS Intel     | `claude-acc_darwin_amd64`          |
| macOS Apple Si. | `claude-acc_darwin_arm64`          |
| Linux x64       | `claude-acc_linux_amd64`           |
| Linux ARM64     | `claude-acc_linux_arm64`           |

Verify the download against `SHA256SUMS` on the release if you like, then:

```bash
# macOS / Linux
mv claude-acc_darwin_arm64 claude-acc && chmod +x claude-acc
./claude-acc register      # copy onto your PATH; open a new terminal

# Windows (PowerShell)
.\claude-acc_windows_amd64.exe register   # adds itself to your user PATH
```

`register` copies the binary to a per-user location and puts it on your `PATH`
so `claude-acc` works in any shell:

- **Windows** — `%LOCALAPPDATA%\claude-acc\claude-acc.exe`, added to your user
  `PATH`. Open a **new** terminal for the change to apply.
- **macOS / Linux** — `~/.local/bin/claude-acc`, with a `PATH` line added to
  your shell rc (`~/.zshrc`, `~/.bashrc`, or `~/.profile`) if needed.

`unregister` removes the installed copy and the `PATH` entry (add `--purge` to
also delete saved profiles).

### Build from source

Needs Go 1.25+. The binary is pure Go (`CGO_ENABLED=0`), so one machine can
build every platform:

```bash
go build -o claude-acc .          # current platform
go test ./...                     # run the splicer tests
./build.ps1 -Version 4.0.0        # cross-compile all targets into .\dist
```

Or `go install github.com/Samseys/anthropic-account-switcher@latest`.

## Usage

```
claude-acc save [name]     # save the current account (defaults to its email)
claude-acc list [--json]   # list saved profiles; * marks the active one
claude-acc switch [name|-] # switch to a profile, then restart Claude Code
claude-acc current [--json]  # show the active account (profile/email/org/plan)
claude-acc remove <name>   # delete a saved profile
claude-acc rename <old> <new>  # rename a saved profile
claude-acc register        # install onto your PATH for any shell
claude-acc unregister      # remove it (--purge also deletes saved profiles)
claude-acc help
```

`switch` shortcuts: with exactly two saved profiles, a bare `claude-acc switch`
toggles to the other one; `claude-acc switch -` returns to the previously
active profile. Switching to the already-active profile just refreshes its
snapshot. `--json` makes `list`/`current` scriptable (e.g. a prompt segment
showing which account you're on).

### First-time setup

```
# while logged in as account A
claude-acc save work

# log out, log in as account B, then:
claude-acc save personal

# from now on (with two profiles, a bare `switch` toggles):
claude-acc switch          # then fully quit + reopen Claude Code
```

## How it works

A **profile** (stored in `~/.claude/account-profiles/<name>/`) snapshots:

- `credentials.json` — your OAuth tokens (DPAPI-encrypted on Windows)
- `oauthAccount.json` + `userID.txt` — the cached identity from `~/.claude.json`
- `email.txt` — for display

Live token storage is OS-aware:

- **Windows / Linux** — `~/.claude/.credentials.json` (copied verbatim)
- **macOS** — the login Keychain, service `Claude Code-credentials`, via the
  built-in `security` CLI

Claude Code rotates the OAuth tokens in place, so a snapshot goes stale over
time. To compensate, **every `switch` first re-saves the account you are
leaving**, so its profile always holds the freshest tokens.

If `CLAUDE_CONFIG_DIR` is set, it is honored the same way Claude Code honors
it: profiles, credentials and `.claude.json` are read from that directory
instead of `~/.claude`.

**A switch requires a full restart of Claude Code** — the running session holds
the active credentials in memory.

### Why it splices `~/.claude.json` instead of rewriting it

`~/.claude.json` can contain duplicate object keys (e.g. project paths that
differ only in drive-letter case). A normal JSON parse-and-rewrite would either
fail or silently drop one of the duplicates. So the identity patch surgically
replaces *only* the `oauthAccount` and `userID` values with a string-aware
scanner, leaving everything else byte-for-byte intact. The scanner lives in
[`internal/claudejson`](internal/claudejson) and is covered by tests.

## Requirements

- **At runtime**: nothing. The binary is self-contained. On macOS it calls the
  built-in `security` CLI for the Keychain; on Windows `register` edits your
  user PATH directly in the registry (preserving `%VAR%`-style entries).
- **To build**: Go 1.25+.

## Caveats

- **macOS Keychain prompts**: the first `security` read/write may pop a dialog —
  choose "Always Allow". Override the service name via `CLAUDE_KEYCHAIN_SERVICE`
  if needed.
- Profiles contain live OAuth tokens — treat `~/.claude/account-profiles/` as
  sensitive. On **Windows** the tokens are DPAPI-encrypted (readable only by
  your user account on that machine, so profiles are not portable); on
  **macOS / Linux** they are plain files with `0600` permissions.

## License

MIT — see [LICENSE](LICENSE).
