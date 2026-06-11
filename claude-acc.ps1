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
$Previous   = '_previous'

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
    $dirs = Get-ChildItem -LiteralPath $ProfileDir -Directory -ErrorAction SilentlyContinue |
            Where-Object { $_.Name -ne $Previous }
  }
  if (-not $dirs -or $dirs.Count -eq 0) {
    Emit "No saved profiles yet. Run '$Bin save' to store the current account."
    return
  }
  $liveCred = Read-Text $CredFile
  Emit "Saved account profiles:"
  Emit ""
  foreach ($d in $dirs) {
    $email = Get-ProfileEmail $d.FullName
    $profCred = Read-Text (Join-Path $d.FullName 'credentials.json')
    $isCur = ($liveCred -and $profCred -and ($liveCred.Trim() -eq $profCred.Trim()))
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

  # Save the account we are leaving so a switch is always undoable.
  try { New-Snapshot $Previous $true } catch { }

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
  Emit "(Run '$Bin switch $Previous' to undo this switch.)"
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

function Invoke-Uninstall([string]$flag) {
  $purge = ($flag -eq '--purge')
  Emit "Uninstalling $Bin (script edition)..."
  Emit "(Your active Claude Code login is not touched - this only removes the tool's data.)"
  if ($purge) {
    if (Test-Path -LiteralPath $ProfileDir) {
      Remove-Item -Recurse -Force -LiteralPath $ProfileDir
      Emit "Removed saved profiles at $ProfileDir"
    }
  } else {
    Emit "Saved profiles kept at $ProfileDir (run '$Bin uninstall --purge' to delete them too)."
  }
  Emit "To finish: delete this script folder and remove any 'claude-acc' function from your PowerShell profile (`$PROFILE)."
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
  Emit "  uninstall        Remove the tool's data (add --purge to delete profiles too)"
  Emit "  help             Show this help"
  Emit "  version          Print version"
  Emit ""
  Emit "Notes:"
  Emit "  - A switch requires a full restart of Claude Code to take effect."
  Emit "  - Every switch saves a '$Previous' profile, so '$Bin switch $Previous' undoes it."
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
  'remove'    { Invoke-Remove $arg1 }
  'rm'        { Invoke-Remove $arg1 }
  'uninstall' { Invoke-Uninstall $arg1 }
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
