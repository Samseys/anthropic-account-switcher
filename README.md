# claude-acc

Switch between multiple Anthropic accounts in Claude Code — **no dependencies**.
Just a script: PowerShell on Windows, bash on macOS/Linux. Nothing to install,
no Node, no npm.

Useful when you juggle, say, a personal account and a work account and don't
want to re-authenticate every time.

## Install

Clone (or download) the repo — that's it:

```bash
git clone https://github.com/Samseys/anthropic-account-switcher.git
```

Then run the script for your OS directly, or add a short alias.

### Windows (PowerShell & cmd)

```powershell
# run directly
C:\path\to\anthropic-account-switcher\claude-acc.ps1 list

# or register a `claude-acc` command (one time)
C:\path\to\anthropic-account-switcher\claude-acc.ps1 register
. $PROFILE   # reload PowerShell, or open a new terminal
```

`register` wires up **both shells**:

- **PowerShell** — adds a `claude-acc` function to your `$PROFILE`.
- **cmd.exe** — creates a `claude-acc.cmd` shim next to the script and adds the
  script's folder to your user `PATH`, so `claude-acc` works in Command Prompt
  (and the Run dialog). Open a **new** cmd window for the PATH change to apply.

`unregister` removes the function, the shim, and the PATH entry.

If scripts are blocked, allow local scripts once:
`Set-ExecutionPolicy -Scope CurrentUser RemoteSigned`.

### macOS / Linux (bash)

```bash
chmod +x claude-acc.sh
./claude-acc.sh list

# or register a `claude-acc` alias in your shell profile (one time)
./claude-acc.sh register
source ~/.bashrc   # or ~/.zshrc, or open a new terminal
```

## Usage

```
claude-acc save [name]     # save the current account (defaults to its email)
claude-acc list            # list saved profiles; * marks the active one
claude-acc switch <name>   # switch to a profile, then restart Claude Code
claude-acc current         # show the active account (email / org / plan)
claude-acc remove <name>   # delete a saved profile
claude-acc register        # wire up `claude-acc` for your shell (PowerShell + cmd on Windows)
claude-acc unregister      # remove it (--purge also deletes saved profiles)
claude-acc help
```

### First-time setup

```
# while logged in as account A
claude-acc save work

# log out, log in as account B, then:
claude-acc save personal

# from now on:
claude-acc switch work     # then fully quit + reopen Claude Code
```

## How it works

A **profile** (stored in `~/.claude/account-profiles/<name>/`) snapshots:

- `credentials.json` — an exact copy of your OAuth tokens
- `oauthAccount.json` + `userID.txt` — the cached identity from `~/.claude.json`
- `email.txt` — for display

Token storage is OS-aware:

- **Windows / Linux** — `~/.claude/.credentials.json` (copied verbatim)
- **macOS** — the login Keychain, service `Claude Code-credentials`, via the
  built-in `security` CLI

**A switch requires a full restart of Claude Code** — the running session holds
the active credentials in memory.

### Why it splices `~/.claude.json` instead of rewriting it

`~/.claude.json` can contain duplicate object keys (e.g. project paths that
differ only in drive-letter case). A normal JSON parse-and-rewrite would either
fail (PowerShell) or silently drop one of the duplicates (python/jq). So the
identity patch surgically replaces *only* the `oauthAccount` and `userID`
values with a string-aware scanner, leaving everything else byte-for-byte
intact. This is verified lossless on real configs.

## Requirements

- **Windows**: nothing — Windows PowerShell 5.1 (built in) is enough.
- **macOS / Linux**: `python3` is used for the identity patch. It ships with
  macOS (via the Command Line Tools) and almost every Linux distro. Without it,
  credentials still switch fine and Claude Code reconciles the displayed
  identity from the new token on restart.

## Caveats

- **macOS Keychain prompts**: the first `security` read/write may pop a dialog —
  choose "Always Allow". Override the service name via `CLAUDE_KEYCHAIN_SERVICE`
  if needed.
- Profiles contain live OAuth tokens — treat `~/.claude/account-profiles/` as
  sensitive.

## License

MIT — see [LICENSE](LICENSE).
