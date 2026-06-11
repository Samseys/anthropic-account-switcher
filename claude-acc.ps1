<#
  claude-acc.ps1 - switch between multiple Anthropic (Claude Code) accounts.

  Pure PowerShell, no external dependencies (PowerShell ships with Windows).
  Saves/restores the OAuth credentials (~/.claude/.credentials.json) plus the
  cached identity (oauthAccount + userID in ~/.claude.json).

  The identity patch uses a string-aware scanner that splices only the
  oauthAccount/userID values, rather than re-serializing the whole file. This
  is deliberate: ~/.claude.json can contain duplicate object keys (e.g. project
  paths differing only in case) that PowerShell's ConvertFrom-Json rejects and
  that a full rewrite would drop. Splicing preserves the rest byte-for-byte.

  Usage:  .\claude-acc.ps1 <command> [name]      (run `help` for the list)
#>

$ErrorActionPreference = 'Stop'

$Version    = '3.0.0'
$Bin        = 'claude-acc'
$ClaudeDir  = Join-Path $HOME '.claude'
$CredFile   = Join-Path $ClaudeDir '.credentials.json'
$ConfigFile = Join-Path $HOME '.claude.json'
$ProfileDir = Join-Path $ClaudeDir 'account-profiles'

function Emit([string]$m) { Write-Output $m }
function Die([string]$m)  { Write-Output "ERROR: $m"; exit 1 }

# Write UTF-8 *without* a BOM (Claude Code's JSON parser rejects a leading BOM),
# atomically via a temp file.
function Write-TextNoBom([string]$Path, [string]$Text) {
  $tmp = "$Path.tmp"
  [System.IO.File]::WriteAllText($tmp, $Text, (New-Object System.Text.UTF8Encoding $false))
  Move-Item -Force -LiteralPath $tmp -Destination $Path
}

function Read-Text([string]$Path) {
  if (Test-Path -LiteralPath $Path) { return [System.IO.File]::ReadAllText($Path) }
  return $null
}

function Sanitize([string]$n) { return ($n -replace '[^a-zA-Z0-9._@-]', '_') }

# ---- string-aware JSON value locator (no full parse) ----

# Returns the index just past the JSON value that starts at index $i.
function Get-ValueEnd([string]$t, [int]$i) {
  $n = $t.Length
  if ($i -ge $n) { return $i }
  $ch = $t[$i]
  if ($ch -eq '"') {
    $i++
    while ($i -lt $n) {
      $c = $t[$i]
      if ($c -eq '\') { $i += 2; continue }
      if ($c -eq '"') { return $i + 1 }
      $i++
    }
    return $i
  }
  if ($ch -eq '{' -or $ch -eq '[') {
    $depth = 0; $inStr = $false
    while ($i -lt $n) {
      $c = $t[$i]
      if ($inStr) {
        if ($c -eq '\') { $i += 2; continue }
        if ($c -eq '"') { $inStr = $false }
        $i++; continue
      }
      if ($c -eq '"') { $inStr = $true; $i++; continue }
      if ($c -eq '{' -or $c -eq '[') { $depth++; $i++; continue }
      if ($c -eq '}' -or $c -eq ']') { $depth--; $i++; if ($depth -eq 0) { return $i }; continue }
      $i++
    }
    return $i
  }
  # primitive: number / true / false / null
  while ($i -lt $n) { $c = $t[$i]; if ($c -eq ',' -or $c -eq '}' -or $c -eq ']') { break }; $i++ }
  return $i
}

# Locate the value of top-level property $key. Returns @{Start;End} or $null.
# Only matches at object depth 1, so duplicate/nested keys elsewhere are ignored.
function Get-TopLevelValueSpan([string]$t, [string]$key) {
  $i = 0; $n = $t.Length; $depth = 0
  while ($i -lt $n) {
    $ch = $t[$i]
    if ($ch -eq '"') {
      $i++
      $sb = New-Object System.Text.StringBuilder
      while ($i -lt $n) {
        $c = $t[$i]
        if ($c -eq '\') { if ($i + 1 -lt $n) { [void]$sb.Append($t[$i + 1]) }; $i += 2; continue }
        if ($c -eq '"') { break }
        [void]$sb.Append($c); $i++
      }
      $i++  # past closing quote
      if ($depth -eq 1) {
        $j = $i
        while ($j -lt $n -and [char]::IsWhiteSpace($t[$j])) { $j++ }
        if ($j -lt $n -and $t[$j] -eq ':' -and $sb.ToString() -eq $key) {
          $j++
          while ($j -lt $n -and [char]::IsWhiteSpace($t[$j])) { $j++ }
          return @{ Start = $j; End = (Get-ValueEnd $t $j) }
        }
      }
      continue
    }
    if ($ch -eq '{' -or $ch -eq '[') { $depth++; $i++; continue }
    if ($ch -eq '}' -or $ch -eq ']') { $depth--; $i++; continue }
    $i++
  }
  return $null
}

function Get-TopLevelValueText([string]$t, [string]$key) {
  $span = Get-TopLevelValueSpan $t $key
  if ($null -eq $span) { return $null }
  return $t.Substring($span.Start, $span.End - $span.Start)
}

# Replace the value of top-level $key with $newText; returns the new document
# (or the unchanged document if the key is absent).
function Set-TopLevelValue([string]$t, [string]$key, [string]$newText) {
  $span = Get-TopLevelValueSpan $t $key
  if ($null -eq $span) { return $t }
  return $t.Substring(0, $span.Start) + $newText + $t.Substring($span.End)
}

function Get-FieldFromObjectText([string]$objText, [string]$field) {
  if (-not $objText) { return $null }
  $m = [regex]::Match($objText, '"' + [regex]::Escape($field) + '"\s*:\s*"((?:[^"\\]|\\.)*)"')
  if ($m.Success) { return $m.Groups[1].Value }
  return $null
}

# ---- profiles ----

function Get-ProfileEmail([string]$dir) {
  $f = Join-Path $dir 'email.txt'
  if (Test-Path -LiteralPath $f) { return ([System.IO.File]::ReadAllText($f)).Trim() }
  return 'unknown'
}

# Snapshot the current account into a profile directory:
#   <name>/credentials.json   exact copy of the live credential file
#   <name>/oauthAccount.json  raw oauthAccount value text from ~/.claude.json
#   <name>/userID.txt         raw userID value text from ~/.claude.json
#   <name>/email.txt          account email, for display
function New-Snapshot([string]$name, [bool]$quiet) {
  if (-not (Test-Path -LiteralPath $CredFile)) {
    Die "No credentials at $CredFile. Log in to Claude Code first."
  }
  $cfg = Read-Text $ConfigFile
  $oauthText = $null; $userIdText = $null; $email = ''
  if ($cfg) {
    $oauthText = Get-TopLevelValueText $cfg 'oauthAccount'
    $userIdText = Get-TopLevelValueText $cfg 'userID'
    $email = [string](Get-FieldFromObjectText $oauthText 'emailAddress')
  }
  if (-not $name) { if ($email) { $name = $email } else { $name = 'default' } }
  $name = Sanitize $name

  $dir = Join-Path $ProfileDir $name
  New-Item -ItemType Directory -Force -Path $dir | Out-Null
  Copy-Item -LiteralPath $CredFile -Destination (Join-Path $dir 'credentials.json') -Force
  if ($oauthText)  { Write-TextNoBom (Join-Path $dir 'oauthAccount.json') $oauthText }
  if ($userIdText) { Write-TextNoBom (Join-Path $dir 'userID.txt') $userIdText }
  Write-TextNoBom (Join-Path $dir 'email.txt') $email

  if (-not $quiet) {
    $shown = if ($email) { $email } else { 'unknown email' }
    Emit "Saved current account ($shown) as profile `"$name`"."
  }
}

function Invoke-Save([string]$name) { New-Snapshot $name $false }

function Invoke-List {
  $dirs = @()
  if (Test-Path -LiteralPath $ProfileDir) {
    $dirs = Get-ChildItem -LiteralPath $ProfileDir -Directory -ErrorAction SilentlyContinue
  }
  if (-not $dirs -or $dirs.Count -eq 0) {
    Emit "No saved profiles yet. Run '$Bin save' to store the current account."
    return
  }
  # Detect the active profile by stable identity (userID), since Claude Code
  # rotates the OAuth token in place and the credential blob drifts over time.
  # Fall back to a byte-for-byte credential compare when no identity is present.
  $cfg = Read-Text $ConfigFile
  $liveId = if ($cfg) { Get-TopLevelValueText $cfg 'userID' } else { $null }
  $liveCred = if (-not $liveId) { Read-Text $CredFile } else { $null }
  Emit "Saved account profiles:"
  Emit ""
  foreach ($d in $dirs) {
    $email = Get-ProfileEmail $d.FullName
    $isCur = $false
    if ($liveId) {
      $profId = Read-Text (Join-Path $d.FullName 'userID.txt')
      if ($profId -and ($profId.Trim() -eq $liveId.Trim())) { $isCur = $true }
    } elseif ($liveCred) {
      $profCred = Read-Text (Join-Path $d.FullName 'credentials.json')
      if ($profCred -and ($liveCred.Trim() -eq $profCred.Trim())) { $isCur = $true }
    }
    $mark = if ($isCur) { '* ' } else { '  ' }
    $tag  = if ($isCur) { '   [current]' } else { '' }
    Emit "  $mark$($d.Name)   ($email)$tag"
  }
  Emit ""
  Emit "* = currently active account"
}

function Invoke-Switch([string]$name) {
  if (-not $name) { Die "Usage: $Bin switch <profile-name>   (see '$Bin list')" }
  $name = Sanitize $name
  $dir = Join-Path $ProfileDir $name
  $credSrc = Join-Path $dir 'credentials.json'
  if (-not (Test-Path -LiteralPath $credSrc)) {
    Die "No profile named `"$name`". Run '$Bin list' to see options."
  }

  # Restore credentials (byte-exact copy).
  Copy-Item -LiteralPath $credSrc -Destination $CredFile -Force

  # Patch cached identity by splicing only oauthAccount/userID.
  $cfg = Read-Text $ConfigFile
  if ($cfg) {
    $oauthText = Read-Text (Join-Path $dir 'oauthAccount.json')
    $userIdText = Read-Text (Join-Path $dir 'userID.txt')
    if ($oauthText)  { $cfg = Set-TopLevelValue $cfg 'oauthAccount' $oauthText.Trim() }
    if ($userIdText) { $cfg = Set-TopLevelValue $cfg 'userID' $userIdText.Trim() }
    Write-TextNoBom $ConfigFile $cfg
  }

  $email = Get-ProfileEmail $dir
  Emit "Switched to `"$name`" ($email)."
  Emit ""
  Emit "IMPORTANT: fully quit Claude Code and reopen it for the new account to take"
  Emit "effect. The current session is still authenticated as the previous account."
}

function Invoke-Current {
  $cfg = Read-Text $ConfigFile
  $oauth = if ($cfg) { Get-TopLevelValueText $cfg 'oauthAccount' } else { $null }
  if (-not $oauth) { Emit "No account info found in $ConfigFile. You may not be logged in."; return }
  $mail = Get-FieldFromObjectText $oauth 'emailAddress'
  $org  = Get-FieldFromObjectText $oauth 'organizationName'
  if (-not $org) { $org = Get-FieldFromObjectText $oauth 'organizationUuid' }
  $plan = Get-FieldFromObjectText $oauth 'seatTier'
  if (-not $plan) { $plan = Get-FieldFromObjectText $oauth 'billingType' }
  Emit "Current account:"
  Emit "  email: $(if ($mail) { $mail } else { 'unknown' })"
  Emit "  org:   $(if ($org) { $org } else { 'unknown' })"
  Emit "  plan:  $(if ($plan) { $plan } else { 'unknown' })"
}

function Invoke-Remove([string]$name) {
  if (-not $name) { Die "Usage: $Bin remove <profile-name>" }
  $name = Sanitize $name
  $dir = Join-Path $ProfileDir $name
  if (-not (Test-Path -LiteralPath $dir)) { Die "No profile named `"$name`"." }
  Remove-Item -Recurse -Force -LiteralPath $dir
  Emit "Removed profile `"$name`"."
}

# ---- (un)register: wire a `claude-acc` command into PowerShell *and* cmd.exe ----
#
# PowerShell gets a `function claude-acc` in $PROFILE. cmd.exe has no equivalent
# profile, so it gets a `claude-acc.cmd` shim placed next to this script, with
# the script's folder added to the user PATH. The shim also makes `claude-acc`
# work from the Run dialog and any other shell that honours PATH.

function Get-ShimPath {
  $dir = Split-Path -Parent $PSCommandPath
  return (Join-Path $dir "$Bin.cmd")
}

# Compare two filesystem paths for equality (case-insensitive, trailing-slash
# insensitive) the way Windows treats them.
function Test-PathEqual([string]$a, [string]$b) {
  if (-not $a -or -not $b) { return $false }
  return ($a.TrimEnd('\', '/') -ieq $b.TrimEnd('\', '/'))
}

function Remove-Registration {
  if (-not (Test-Path -LiteralPath $PROFILE)) { return }
  $esc = [regex]::Escape($Bin)
  $lines = [System.IO.File]::ReadAllLines($PROFILE)
  $kept = $lines | Where-Object {
    ($_ -notmatch ('^\s*#\s*' + $esc + '\s*$')) -and ($_ -notmatch ('^\s*function\s+' + $esc + '\b'))
  }
  [System.IO.File]::WriteAllLines($PROFILE, $kept)
}

# Add $dir to the persisted *user* PATH (and the current process) if absent.
function Add-ToUserPath([string]$dir) {
  $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
  $parts = @(); if ($userPath) { $parts = $userPath -split ';' | Where-Object { $_ -ne '' } }
  if ($parts | Where-Object { Test-PathEqual $_ $dir }) { return $false }
  $newPath = (@($parts) + $dir) -join ';'
  [Environment]::SetEnvironmentVariable('Path', $newPath, 'User')
  if (-not ($env:Path -split ';' | Where-Object { Test-PathEqual $_ $dir })) {
    $env:Path = $env:Path.TrimEnd(';') + ';' + $dir
  }
  return $true
}

# Remove $dir from the persisted user PATH (and the current process) if present.
function Remove-FromUserPath([string]$dir) {
  $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
  if (-not $userPath) { return }
  $parts = $userPath -split ';' | Where-Object { $_ -ne '' -and -not (Test-PathEqual $_ $dir) }
  [Environment]::SetEnvironmentVariable('Path', ($parts -join ';'), 'User')
  $env:Path = (($env:Path -split ';' | Where-Object { $_ -ne '' -and -not (Test-PathEqual $_ $dir) }) -join ';')
}

function Invoke-Register {
  $path = $PSCommandPath
  $scriptDir = Split-Path -Parent $path
  $leaf = Split-Path -Leaf $path

  # 1) PowerShell profile function.
  $dir = Split-Path -Parent $PROFILE
  if ($dir -and -not (Test-Path -LiteralPath $dir)) {
    New-Item -ItemType Directory -Force -Path $dir | Out-Null
  }
  Remove-Registration
  $block = "# $Bin" + [Environment]::NewLine + "function $Bin { & `"$path`" @args }"
  Add-Content -LiteralPath $PROFILE -Value $block

  # 2) cmd.exe shim next to the script. %~dp0 keeps it relative to itself, so it
  #    stays valid even if the folder is moved.
  $shim = Get-ShimPath
  $nl = "`r`n"
  $cmd = "@echo off$nl" +
         "powershell -NoProfile -ExecutionPolicy Bypass -File `"%~dp0$leaf`" %*$nl"
  Write-TextNoBom $shim $cmd

  # 3) Put the script's folder on the user PATH so the shim resolves by name.
  $added = Add-ToUserPath $scriptDir

  Emit "Registered '$Bin' -> $path"
  Emit ""
  Emit "PowerShell: added a function to your profile ($PROFILE)."
  Emit "            run '. `$PROFILE' or open a new terminal."
  Emit "cmd.exe:    created shim $shim"
  if ($added) {
    Emit "            added '$scriptDir' to your user PATH."
    Emit "            open a NEW cmd window for PATH changes to apply."
  } else {
    Emit "            '$scriptDir' is already on your user PATH."
  }
  Emit ""
  Emit "If PowerShell blocks scripts, run: Set-ExecutionPolicy -Scope CurrentUser RemoteSigned"
}

function Invoke-Unregister([string]$flag) {
  $purge = ($flag -eq '--purge')

  # 1) PowerShell profile function.
  Remove-Registration

  # 2) cmd.exe shim + PATH entry.
  $shim = Get-ShimPath
  if (Test-Path -LiteralPath $shim) { Remove-Item -Force -LiteralPath $shim }
  Remove-FromUserPath (Split-Path -Parent $PSCommandPath)

  Emit "Unregistered '$Bin' (removed the PowerShell function, cmd shim, and PATH entry)."
  Emit "(Your active Claude Code login is not touched - this only removes the tool.)"
  if ($purge) {
    if (Test-Path -LiteralPath $ProfileDir) {
      Remove-Item -Recurse -Force -LiteralPath $ProfileDir
      Emit "Removed saved profiles at $ProfileDir"
    }
  } else {
    Emit "Saved profiles kept at $ProfileDir (run '$Bin unregister --purge' to delete them too)."
  }
  Emit "You can now delete this script if you no longer need it."
}

function Invoke-Help {
  Emit "$Bin v$Version - switch between Anthropic (Claude Code) accounts."
  Emit ""
  Emit "Usage: $Bin <command> [name]"
  Emit ""
  Emit "Commands:"
  Emit "  save [name]      Save the current account (defaults to its email as the name)"
  Emit "  list             List saved profiles; * marks the active one"
  Emit "  switch <name>    Switch to a saved profile (then restart Claude Code)"
  Emit "  current          Show the active account (email / org / plan)"
  Emit "  remove <name>    Delete a saved profile"
  Emit "  register         Wire up '$Bin' for PowerShell (profile fn) and cmd (PATH shim)"
  Emit "  unregister       Remove both (add --purge to delete saved profiles too)"
  Emit "  help             Show this help"
  Emit "  version          Print version"
  Emit ""
  Emit "Notes:"
  Emit "  - A switch requires a full restart of Claude Code to take effect."
}

# ---- dispatch (parse `$args` manually so leading-dash tokens don't break) ----

$cmd  = if ($args.Count -ge 1) { [string]$args[0] } else { '' }
$arg1 = if ($args.Count -ge 2) { [string]$args[1] } else { '' }

switch ($cmd.ToLower()) {
  'save'      { Invoke-Save $arg1 }
  'list'      { Invoke-List }
  'ls'        { Invoke-List }
  'switch'    { Invoke-Switch $arg1 }
  'use'       { Invoke-Switch $arg1 }
  'current'   { Invoke-Current }
  'whoami'    { Invoke-Current }
  'remove'     { Invoke-Remove $arg1 }
  'rm'         { Invoke-Remove $arg1 }
  'register'   { Invoke-Register }
  'unregister' { Invoke-Unregister $arg1 }
  'uninstall'  { Invoke-Unregister $arg1 }
  'version'   { Emit $Version }
  '--version' { Emit $Version }
  '-v'        { Emit $Version }
  ''          { Invoke-Help }
  'help'      { Invoke-Help }
  '--help'    { Invoke-Help }
  '-h'        { Invoke-Help }
  default {
    Emit "Unknown command `"$cmd`"."
    Emit ""
    Invoke-Help
    exit 1
  }
}
