#!/usr/bin/env bash
#
# claude-acc.sh - switch between multiple Anthropic (Claude Code) accounts.
#
# For macOS and Linux. Credential swapping uses only built-in tools (cp on
# Linux, the `security` keychain CLI on macOS) and needs no dependencies.
#
# Patching the cached identity (oauthAccount + userID in ~/.claude.json) uses
# python3 when available. It does NOT fully re-parse the file: it splices only
# those two values, because ~/.claude.json can contain duplicate object keys
# (e.g. project paths differing only in case) that json.load would silently
# dedupe and drop. If python3 is absent, credentials are still swapped and
# Claude Code reconciles the identity from the new token on restart.

VERSION="3.0.0"
BIN="claude-acc"
CLAUDE_DIR="$HOME/.claude"
CRED_FILE="$CLAUDE_DIR/.credentials.json"
CONFIG_FILE="$HOME/.claude.json"
PROFILE_DIR="$CLAUDE_DIR/account-profiles"
KEYCHAIN_SERVICE="${CLAUDE_KEYCHAIN_SERVICE:-Claude Code-credentials}"
KEYCHAIN_ACCOUNT="$(id -un)"

case "$(uname -s)" in Darwin) IS_MAC=1 ;; *) IS_MAC=0 ;; esac
PYTHON="$(command -v python3 2>/dev/null || true)"
# Ignore a non-functional interpreter (e.g. the Windows Store python3 stub).
if [ -n "$PYTHON" ] && ! "$PYTHON" -c 'import sys' >/dev/null 2>&1; then PYTHON=""; fi

emit() { printf '%s\n' "$*"; }
die()  { printf 'ERROR: %s\n' "$*" >&2; exit 1; }
sanitize() { printf '%s' "$1" | LC_ALL=C tr -c 'A-Za-z0-9._@-' '_'; }

# ---- python helper (string-aware JSON splice; no full parse) ----

PYHELPER=""
if [ -n "$PYTHON" ]; then
  PYHELPER="$(mktemp)"
  trap 'rm -f "$PYHELPER"' EXIT
  cat > "$PYHELPER" <<'PY'
import sys, os, re, tempfile

def value_end(t, i):
    n = len(t)
    if i >= n: return i
    ch = t[i]
    if ch == '"':
        i += 1
        while i < n:
            c = t[i]
            if c == '\\': i += 2; continue
            if c == '"': return i + 1
            i += 1
        return i
    if ch == '{' or ch == '[':
        depth = 0; ins = False
        while i < n:
            c = t[i]
            if ins:
                if c == '\\': i += 2; continue
                if c == '"': ins = False
                i += 1; continue
            if c == '"': ins = True; i += 1; continue
            if c == '{' or c == '[': depth += 1; i += 1; continue
            if c == '}' or c == ']':
                depth -= 1; i += 1
                if depth == 0: return i
                continue
            i += 1
        return i
    while i < n and t[i] not in ',}]': i += 1
    return i

def top_span(t, key):
    i = 0; n = len(t); depth = 0
    while i < n:
        ch = t[i]
        if ch == '"':
            i += 1; buf = []
            while i < n:
                c = t[i]
                if c == '\\':
                    if i + 1 < n: buf.append(t[i + 1])
                    i += 2; continue
                if c == '"': break
                buf.append(c); i += 1
            i += 1
            if depth == 1:
                j = i
                while j < n and t[j].isspace(): j += 1
                if j < n and t[j] == ':' and ''.join(buf) == key:
                    j += 1
                    while j < n and t[j].isspace(): j += 1
                    return (j, value_end(t, j))
            continue
        if ch == '{' or ch == '[': depth += 1
        elif ch == '}' or ch == ']': depth -= 1
        i += 1
    return None

def get_text(t, key):
    s = top_span(t, key)
    return t[s[0]:s[1]] if s else None

def get_field(obj, field):
    if not obj: return None
    m = re.search(r'"' + re.escape(field) + r'"\s*:\s*"((?:[^"\\]|\\.)*)"', obj)
    return m.group(1) if m else None

def read(p):
    with open(p, encoding='utf-8') as f: return f.read()

def write(p, s):
    d = os.path.dirname(p) or '.'
    fd, tmp = tempfile.mkstemp(dir=d)
    with os.fdopen(fd, 'w', encoding='utf-8', newline='') as f: f.write(s)
    os.replace(tmp, p)

act = sys.argv[1]
if act == 'email':
    print(get_field(get_text(read(sys.argv[2]), 'oauthAccount'), 'emailAddress') or '')
elif act == 'userid':
    print(get_text(read(sys.argv[2]), 'userID') or '')
elif act == 'summary':
    o = get_text(read(sys.argv[2]), 'oauthAccount') or ''
    print(get_field(o, 'emailAddress') or 'unknown')
    print(get_field(o, 'organizationName') or get_field(o, 'organizationUuid') or 'unknown')
    print(get_field(o, 'seatTier') or get_field(o, 'billingType') or 'unknown')
elif act == 'snapshot':
    cfg = read(sys.argv[2]); outdir = sys.argv[3]
    os.makedirs(outdir, exist_ok=True)
    o = get_text(cfg, 'oauthAccount'); u = get_text(cfg, 'userID')
    if o is not None: write(os.path.join(outdir, 'oauthAccount.json'), o)
    if u is not None: write(os.path.join(outdir, 'userID.txt'), u)
    write(os.path.join(outdir, 'email.txt'), get_field(o, 'emailAddress') or '')
elif act == 'apply':
    cfgp = sys.argv[2]; prof = sys.argv[3]
    cfg = read(cfgp)
    op = os.path.join(prof, 'oauthAccount.json'); up = os.path.join(prof, 'userID.txt')
    if os.path.exists(op):
        s = top_span(cfg, 'oauthAccount')
        if s: cfg = cfg[:s[0]] + read(op).strip() + cfg[s[1]:]
    if os.path.exists(up):
        s = top_span(cfg, 'userID')
        if s: cfg = cfg[:s[0]] + read(up).strip() + cfg[s[1]:]
    write(cfgp, cfg)
PY
fi
pyrun() { "$PYTHON" "$PYHELPER" "$@"; }

# ---- credential storage ----

cred_store() {
  if [ "$IS_MAC" = 1 ]; then
    if security find-generic-password -a "$KEYCHAIN_ACCOUNT" -s "$KEYCHAIN_SERVICE" -w >/dev/null 2>&1; then
      echo keychain
    elif [ -f "$CRED_FILE" ]; then
      echo file
    else
      echo keychain
    fi
  else
    echo file
  fi
}

try_read_creds_to() { # $1 = destination; returns non-zero on failure, never exits
  local dest="$1" store
  store="$(cred_store)"
  if [ "$store" = keychain ]; then
    security find-generic-password -a "$KEYCHAIN_ACCOUNT" -s "$KEYCHAIN_SERVICE" -w > "$dest" 2>/dev/null
  else
    [ -f "$CRED_FILE" ] && cp "$CRED_FILE" "$dest"
  fi
}

read_creds_to() { # $1 = destination file; dies if no credentials
  local dest="$1"
  try_read_creds_to "$dest" && return 0
  if [ "$(cred_store)" = keychain ]; then
    die "No credentials in the Keychain (service \"$KEYCHAIN_SERVICE\"). Log in to Claude Code first."
  else
    die "No credentials at $CRED_FILE. Log in to Claude Code first."
  fi
}

write_creds_from() { # $1 = source file
  local src="$1" store
  store="$(cred_store)"
  if [ "$store" = keychain ]; then
    security add-generic-password -U -a "$KEYCHAIN_ACCOUNT" -s "$KEYCHAIN_SERVICE" -w "$(cat "$src")"
  else
    ( umask 177; cp "$src" "$CRED_FILE" )
  fi
}

# ---- profiles ----

SNAP_NAME=""
snapshot() { # $1 = name (may be empty), $2 = quiet (0/1)
  local name="$1" quiet="$2" email="" tmpd
  tmpd="$(mktemp -d)"
  read_creds_to "$tmpd/credentials.json"
  if [ -n "$PYHELPER" ] && [ -f "$CONFIG_FILE" ]; then email="$(pyrun email "$CONFIG_FILE")"; fi
  if [ -z "$name" ]; then name="${email:-default}"; fi
  name="$(sanitize "$name")"
  local dir="$PROFILE_DIR/$name"
  mkdir -p "$dir"
  cp "$tmpd/credentials.json" "$dir/credentials.json"
  chmod 600 "$dir/credentials.json" 2>/dev/null || true
  if [ -n "$PYHELPER" ] && [ -f "$CONFIG_FILE" ]; then
    pyrun snapshot "$CONFIG_FILE" "$dir"
  else
    printf '%s' "$email" > "$dir/email.txt"
  fi
  rm -rf "$tmpd"
  SNAP_NAME="$name"
  if [ "$quiet" != "1" ]; then
    emit "Saved current account (${email:-unknown email}) as profile \"$name\"."
    if [ -z "$PYHELPER" ]; then
      emit "(note: python3 not found - saved credentials only; identity reconciles on restart.)"
    fi
  fi
}

do_save() { snapshot "$1" 0; }

do_list() {
  local found=0 header=0 b email mark tag is_cur
  # Detect the active profile by stable identity (userID), since Claude Code
  # rotates the OAuth token in place and the credential blob drifts over time.
  # Fall back to a byte-for-byte credential compare when python is unavailable.
  local live_id="" have_id=0 livetmp live=""
  if [ -n "$PYHELPER" ] && [ -f "$CONFIG_FILE" ]; then
    live_id="$(pyrun userid "$CONFIG_FILE")"
    [ -n "$live_id" ] && have_id=1
  fi
  if [ "$have_id" = 0 ]; then
    livetmp="$(mktemp)"
    try_read_creds_to "$livetmp"
    [ -s "$livetmp" ] && live="$(cat "$livetmp")"
    rm -f "$livetmp"
  fi
  if [ -d "$PROFILE_DIR" ]; then
    for d in "$PROFILE_DIR"/*/; do
      [ -d "$d" ] || continue
      b="$(basename "$d")"
      found=1
      if [ "$header" = 0 ]; then emit "Saved account profiles:"; emit ""; header=1; fi
      email="unknown"; [ -f "$d/email.txt" ] && email="$(cat "$d/email.txt")"
      is_cur=0
      if [ "$have_id" = 1 ]; then
        [ -f "$d/userID.txt" ] && [ "$(cat "$d/userID.txt")" = "$live_id" ] && is_cur=1
      elif [ -n "$live" ] && [ -f "$d/credentials.json" ] && [ "$(cat "$d/credentials.json")" = "$live" ]; then
        is_cur=1
      fi
      mark="  "; tag=""
      if [ "$is_cur" = 1 ]; then mark="* "; tag="   [current]"; fi
      emit "  ${mark}${b}   (${email})${tag}"
    done
  fi
  if [ "$found" = 0 ]; then
    emit "No saved profiles yet. Run '$BIN save' to store the current account."
    return
  fi
  emit ""
  emit "* = currently active account"
}

do_switch() {
  local name="$1" dir email
  [ -n "$name" ] || die "Usage: $BIN switch <profile-name>   (see '$BIN list')"
  name="$(sanitize "$name")"
  dir="$PROFILE_DIR/$name"
  [ -f "$dir/credentials.json" ] || die "No profile named \"$name\". Run '$BIN list' to see options."
  write_creds_from "$dir/credentials.json"
  if [ -n "$PYHELPER" ] && [ -f "$CONFIG_FILE" ]; then pyrun apply "$CONFIG_FILE" "$dir"; fi
  email="unknown"; [ -f "$dir/email.txt" ] && email="$(cat "$dir/email.txt")"
  emit "Switched to \"$name\" ($email)."
  emit ""
  emit "IMPORTANT: fully quit Claude Code and reopen it for the new account to take"
  emit "effect. The current session is still authenticated as the previous account."
}

do_current() {
  if [ -n "$PYHELPER" ] && [ -f "$CONFIG_FILE" ]; then
    local s; s="$(pyrun summary "$CONFIG_FILE")"
    emit "Current account:"
    emit "  email: $(printf '%s\n' "$s" | sed -n '1p')"
    emit "  org:   $(printf '%s\n' "$s" | sed -n '2p')"
    emit "  plan:  $(printf '%s\n' "$s" | sed -n '3p')"
  else
    emit "Showing the active account needs python3 (not found)."
    emit "Credential switching still works without it."
  fi
}

do_remove() {
  local name="$1" dir
  [ -n "$name" ] || die "Usage: $BIN remove <profile-name>"
  name="$(sanitize "$name")"
  dir="$PROFILE_DIR/$name"
  [ -d "$dir" ] || die "No profile named \"$name\"."
  rm -rf "$dir"
  emit "Removed profile \"$name\"."
}

# ---- (un)register: wire a `claude-acc` command into the shell profile ----

shell_rc() {
  case "$(basename "${SHELL:-}")" in
    zsh)  echo "$HOME/.zshrc" ;;
    bash) echo "$HOME/.bashrc" ;;
    *)    echo "$HOME/.profile" ;;
  esac
}

script_path() {
  local src="${BASH_SOURCE[0]:-$0}" dir
  dir="$(cd "$(dirname "$src")" >/dev/null 2>&1 && pwd)"
  printf '%s/%s' "$dir" "$(basename "$src")"
}

strip_alias() { # remove our alias block from rc file $1 (idempotent)
  local rc="$1" tmp
  [ -f "$rc" ] || return 0
  tmp="$(mktemp)"
  grep -vE "^# $BIN\$|^alias $BIN=" "$rc" > "$tmp" 2>/dev/null || true
  cp "$tmp" "$rc"
  rm -f "$tmp"
}

do_register() {
  local rc path
  path="$(script_path)"
  chmod +x "$path" 2>/dev/null || true
  rc="$(shell_rc)"
  strip_alias "$rc"
  printf '# %s\nalias %s="%s"\n' "$BIN" "$BIN" "$path" >> "$rc"
  emit "Registered '$BIN' -> $path"
  emit "Added an alias to $rc."
  emit "Run 'source \"$rc\"' or open a new terminal, then use '$BIN'."
}

do_unregister() {
  local purge=0 rc
  [ "${1:-}" = "--purge" ] && purge=1
  rc="$(shell_rc)"
  strip_alias "$rc"
  emit "Unregistered '$BIN' (removed the alias from $rc)."
  emit "(Your active Claude Code login is not touched - this only removes the tool.)"
  if [ "$purge" = 1 ]; then
    rm -rf "$PROFILE_DIR" && emit "Removed saved profiles at $PROFILE_DIR"
  else
    emit "Saved profiles kept at $PROFILE_DIR (run '$BIN unregister --purge' to delete them too)."
  fi
  emit "You can now delete this script if you no longer need it."
}

do_help() {
  emit "$BIN v$VERSION - switch between Anthropic (Claude Code) accounts."
  emit ""
  emit "Usage: $BIN <command> [name]"
  emit ""
  emit "Commands:"
  emit "  save [name]      Save the current account (defaults to its email as the name)"
  emit "  list             List saved profiles; * marks the active one"
  emit "  switch <name>    Switch to a saved profile (then restart Claude Code)"
  emit "  current          Show the active account (email / org / plan)"
  emit "  remove <name>    Delete a saved profile"
  emit "  register         Add a '$BIN' alias to your shell profile"
  emit "  unregister       Remove the alias (add --purge to delete saved profiles too)"
  emit "  help             Show this help"
  emit "  version          Print version"
  emit ""
  emit "Notes:"
  emit "  - A switch requires a full restart of Claude Code to take effect."
}

# ---- dispatch ----

cmd="${1:-}"
arg="${2:-}"
case "$cmd" in
  save)               do_save "$arg" ;;
  list|ls)            do_list ;;
  switch|use)         do_switch "$arg" ;;
  current|whoami)     do_current ;;
  remove|rm)          do_remove "$arg" ;;
  register)           do_register ;;
  unregister|uninstall) do_unregister "$arg" ;;
  version|--version|-v) emit "$VERSION" ;;
  ""|help|--help|-h)  do_help ;;
  *)                  emit "Unknown command \"$cmd\"."; emit ""; do_help; exit 1 ;;
esac
