# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

`acc-claude` is a single static Go binary (`CGO_ENABLED=0`, no runtime deps) that
switches between multiple Anthropic accounts in Claude Code. It snapshots a
logged-in account's OAuth credentials plus its cached identity into named
**profiles** under `~/.claude/account-profiles/<name>/` and restores them on
demand. See [README.md](README.md) for the user-facing docs.

## Commands

Build/test tooling is driven by `make` (recipes shell out to `./scripts/build`,
a tiny Go program, so they run identically under cmd.exe and POSIX shells):

```
make build      # compile for the current platform into ./bin
make install    # build, copy into the managed dir, and register (PATH + status line)
make test       # go test ./...
make check      # vet + test
make vet        # go vet ./...
make fmt        # gofmt -w .
make dist       # cross-compile every release target + SHA256SUMS into ./dist
make version    # print the version that would be embedded
```

Run a single test:

```
go test ./internal/profile -run TestSwitch -v
```

Needs Go 1.25+. Tests rely on `paths.ClaudeDir`/`ConfigFile`/etc. being `var`s
(not consts) so they can be pointed at a scratch directory — keep them mutable.

## Architecture

`main` is only a front end ([cmd/acc-claude/main.go](cmd/acc-claude/main.go)):
it assembles a command table from each package's own `Commands()` and hands it
to the `cli` framework. The real work lives in `internal/`.

**The one-source-of-truth command framework — [internal/cli](internal/cli/cli.go).**
Each subcommand is declared once as a `cli.Command` (name, aliases, flags,
positional completers, handler). Help text, the hidden `__complete` callback,
and the installable shell-completion scripts are all generated from that single
registry, so they cannot drift. When adding/changing a command or flag, edit its
`Command` declaration in the owning package's `commands.go` — do not hand-write
help or completion anywhere.

**Dependency layering.** [internal/paths](internal/paths/paths.go) is the leaf:
it owns the canonical Claude Code file locations, the binary name/version, and
the shared filesystem helpers (`WriteFileAtomic`, `Sanitize`, `PathEqual`,
`ReadFileOpt`). It imports nothing else in the module; everything else imports
it. Honors `CLAUDE_CONFIG_DIR` exactly as Claude Code does.

**Domain packages (each owns its commands + handlers):**
- [internal/profile](internal/profile/profile.go) — the account commands (`save`,
  `list`, `switch`, `current`, `remove`, `rename`), plus the usage feature in
  [usage.go](internal/profile/usage.go) (`usage` command) and
  [statusline.go](internal/profile/statusline.go) (`statusline` sensor +
  `autoswitch` watcher, with the online endpoint fallback in
  [autoswitch_online.go](internal/profile/autoswitch_online.go)). Owns the profile
  on-disk layout and the switch/save logic. The usage feature is **offline-first**
  (status line) with an online endpoint fallback (see below); `autoswitch` reuses
  `Switch` (and its rollback) verbatim. Imports `internal/usage` (parsing +
  endpoint), `internal/store` (live creds for the online active poll), and
  `internal/install` (the `statusline --install`/`--uninstall` flags delegate to
  the settings-file wiring that lives there) — never the reverse.
- [internal/store](internal/store/store.go) — reads/writes the *live* credentials,
  OS-aware: `~/.claude/.credentials.json` on Windows/Linux, the login Keychain
  via the built-in `security` CLI on macOS. Also encrypts profile snapshots at
  rest (DPAPI on Windows via `dpapi_windows.go`, plaintext `0600` elsewhere).
- [internal/install](internal/install/register.go) — `register`/`unregister`:
  per-user bin dir + PATH wiring (Windows registry `HKCU\Environment`, Unix shell
  rc), shell completion install, and the usage status-line wiring in settings.json
  ([statusline.go](internal/install/statusline.go): `InstallStatusLine`/
  `UninstallStatusLine`, which `register`/`unregister` and the `statusline
  --install`/`--uninstall` flags both call). All three are best-effort: a failure
  warns, never fails the command. Platform code is split across
  `pathenv_*`/`completion_*` files by build tag.
- [internal/update](internal/update/update.go) — checks the GitHub Releases API
  for a newer version; `MaybeNotify` is the once-a-day passive nudge run by
  `app.After`.

**Supporting packages:**
- [internal/claudejson](internal/claudejson/claudejson.go) — a string-aware,
  byte-offset splicer for top-level values in `~/.claude.json`. Deliberately NOT
  a full JSON parser: that file can contain duplicate object keys, so it surgically
  replaces only `oauthAccount`/`userID` and leaves the rest byte-for-byte intact.
- [internal/lock](internal/lock/lock.go) — cross-process advisory lock guarding
  the profile dir so concurrent invocations can't interleave a credential write
  with a config patch. An OS file lock (flock on Unix, LockFileEx on Windows,
  via x/sys) on `account-profiles/.lock`: the kernel releases it when the
  holder exits, however it exits, so there is no stale-lock state to detect or
  steal.
- [internal/proc](internal/proc/proc.go) — best-effort detection of a running
  Claude Code process, used only to *warn* before a switch (never to block).
- [internal/usage](internal/usage/usage.go) — a small, **offline-first** leaf: the
  `Report`/`Window` model plus two readers.
  - `ParseStatusLine` ([statusline.go](internal/usage/statusline.go)) — the
    **primary**, offline path: extracts 5h/7d window utilization from the JSON
    Claude Code pipes to a status-line command on stdin. Claude Code ≥ 2.1.x hands
    Pro/Max usage to the status line for free, so the terminal CLI reads it there.
    Note the status-line schema differs from the endpoint's: `used_percentage`
    (not `utilization`) and a Unix-timestamp `resets_at`; `ParseStatusLine`
    tolerates an RFC3339 `resets_at` too in case the shape drifts.
  - `Fetch`/`RefreshToken` ([endpoint.go](internal/usage/endpoint.go)) — the
    **fallback** online path: calls `/api/oauth/usage` (and the OAuth token
    endpoint to refresh) for contexts where the status line never runs (the VSCode
    panel) and to poll *inactive* accounts that never feed the status line. The
    load-bearing detail is the `User-Agent: claude-code/<ver>` (other UAs hit an
    aggressively rate-limited bucket); callers must cache/poll no faster than
    `MinInterval` (90s).

## Key invariants and conventions

- **Usage is offline-first, with a throttled online fallback.** The `statusline`
  command is a sensor: Claude Code pipes it session JSON on stdin, it prints the
  status-bar line and records the active account's usage to
  `account-profiles/.usage-state.json` (for trip detection) and a per-profile
  `usage.json`. The sensor must stay total — it prints a line and returns nil on
  any error, because Claude Code kills slow/failing status-line commands. The
  `usage` command reads only those local files. `list` reads them too but
  refreshes a profile online when its data is stale or its token expired (and
  `list --refresh` forces a live poll of all). `autoswitch` prefers the local
  files too, but falls back to polling `/api/oauth/usage` ([autoswitch_online.go](internal/profile/autoswitch_online.go))
  when there is no fresh sensor reading (the VSCode panel never runs status-line
  commands) — and **always** ranks candidate accounts from their *current*
  endpoint usage, since inactive accounts never feed the sensor. The fallback is
  throttled to `usage.MinInterval` (90s) and only polls while Claude Code is
  running (`proc.ClaudeRunning`). Keep the sensor path primary; the endpoint is a
  fallback, not the default.
- **Refresh/persist tokens only for *candidate* profiles, never the live account.**
  The online candidate poll may find a profile's access token expired; it calls
  `usage.RefreshToken` and writes the rotated token back into that profile's
  encrypted snapshot (`persistProfileToken`). This is safe because Claude Code is
  not holding those tokens. The *active* account is different — Claude Code owns
  and rotates its live credentials — so `fetchActiveOnline` never refreshes or
  rewrites them; an unauthorized response just skips that cycle.
- **The credential swap is the switch.** The `oauthAccount`/`userID` patch in
  `~/.claude.json` is just cached display identity that Claude Code refreshes from
  the token, so its write is best-effort (warns, never fails). Anything that
  genuinely can't be left half-done goes in the `postCommit` transaction in
  `Switch`, which rolls the credential swap back on failure.
- **Account identity keys on `accountUuid`** (per-account, stable across Claude
  Code's in-place OAuth token rotation), NOT on the top-level `userID` (a
  machine-wide analytics ID shared by every account) and not on the drifting
  credential blob. See `profileAccountID`/`liveAccountID`.
- **Every `switch` re-saves the account being left first**, because Claude Code
  rotates tokens in place and the leaving snapshot would otherwise go stale.
- **Pure Go, no platform API linking.** macOS Keychain and process scans shell out
  to OS tools (`security`, `ps`/`tasklist`) so the binary stays `CGO_ENABLED=0`
  and cross-compiles every target from one machine. Keep it that way.
- **No self-modifying binary.** `register`/`update` deliberately never download or
  copy an executable into place — that "dropper" shape is what endpoint security
  flags. File placement is the job of build/install tooling, never the shipped
  binary: `install.ps1`/`install.sh` for releases, and `make install`
  (`scripts/build install`) for a local build. Both copy the binary into the
  managed dir (`install.InstalledBinaryPath()`) and then invoke `register`; the Go
  command itself only wires up PATH/completion/status line and tells you how to
  upgrade.
- **Atomic writes everywhere** (`paths.WriteFileAtomic`, `writeProfileAtomic`):
  temp file + rename, with a Windows-specific rename retry loop to survive
  on-access AV scanners holding a handle on a freshly written file. Writes carry
  no BOM, which Claude Code's JSON parser requires.
- Profile names are run through `paths.Sanitize` at the path boundary
  (`profilePath`); commands acting on an existing profile go through
  `resolveProfile`, which also validates existence and rejects the empty name.

## Repository layout

```
├── .github/workflows/      # ci.yml (build/test), release.yml (tagged + nightly releases)
├── cmd/acc-claude/
│   ├── main.go             # front end: assembles the command table
│   ├── acc-claude.manifest # Windows app manifest
│   └── versioninfo.json    # Windows exe version resource
├── internal/
│   ├── cli/                # dependency-free command framework (dispatch/help/completion)
│   ├── claudejson/         # byte-offset splicer for ~/.claude.json top-level values
│   ├── install/            # register/unregister: PATH + completion (pathenv_*/completion_* per OS)
│   ├── lock/               # cross-process advisory lock on the profile dir
│   ├── paths/              # leaf: file locations, version, fs helpers
│   ├── proc/               # best-effort running-Claude-Code detection
│   ├── profile/            # account commands; usage.go (usage) + statusline.go (sensor + autoswitch) + autoswitch_online.go (endpoint fallback)
│   ├── store/              # live credential I/O (file vs Keychain) + at-rest encryption
│   ├── update/             # GitHub Releases version check
│   └── usage/              # usage model + status-line stdin parser (statusline.go) + online endpoint fallback (endpoint.go)
├── scripts/build/main.go   # build/install/dist/clean/version logic invoked by the Makefile
├── install.ps1 / install.sh
└── Makefile
```

Each platform-specific file uses Go build tags (`_windows.go` / `_unix.go`); when
touching one, check its sibling for the same package. Most packages have a
matching `_test.go`.
