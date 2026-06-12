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
  `list`, `switch`, `current`, `remove`, `rename`). Owns the profile on-disk
  layout and the switch/save logic.
- [internal/store](internal/store/store.go) — reads/writes the *live* credentials,
  OS-aware: `~/.claude/.credentials.json` on Windows/Linux, the login Keychain
  via the built-in `security` CLI on macOS. Also encrypts profile snapshots at
  rest (DPAPI on Windows via `dpapi_windows.go`, plaintext `0600` elsewhere).
- [internal/install](internal/install/register.go) — `register`/`unregister`:
  per-user bin dir + PATH wiring (Windows registry `HKCU\Environment`, Unix shell
  rc), and shell completion install. Platform code is split across
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

## Key invariants and conventions

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
  flags. File placement is the installer scripts' job (`install.ps1`/`install.sh`);
  the Go code only wires up PATH/completion and tells you how to upgrade.
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
│   ├── profile/            # the account commands + on-disk profile layout
│   ├── store/              # live credential I/O (file vs Keychain) + at-rest encryption
│   └── update/             # GitHub Releases version check
├── scripts/build/main.go   # build/install/dist/clean/version logic invoked by the Makefile
├── install.ps1 / install.sh
└── Makefile
```

Each platform-specific file uses Go build tags (`_windows.go` / `_unix.go`); when
touching one, check its sibling for the same package. Most packages have a
matching `_test.go`.
