# One-line installer for claude-acc on Windows.
#   irm https://raw.githubusercontent.com/Samseys/anthropic-account-switcher/main/install.ps1 | iex
#
# Downloads the latest release binary for this machine's architecture, verifies
# it against the published SHA256SUMS, then runs `register` to place it on PATH.

$ErrorActionPreference = 'Stop'
$repo = 'Samseys/anthropic-account-switcher'

$arch = if ($env:PROCESSOR_ARCHITECTURE -eq 'ARM64') { 'arm64' } else { 'amd64' }
$asset = "claude-acc_windows_$arch.exe"
$base  = "https://github.com/$repo/releases/latest/download"

$tmp = Join-Path ([System.IO.Path]::GetTempPath()) "claude-acc-install"
New-Item -ItemType Directory -Force -Path $tmp | Out-Null
$binPath  = Join-Path $tmp $asset
$sumsPath = Join-Path $tmp 'SHA256SUMS'

Write-Host "Downloading $asset ..."
Invoke-WebRequest -Uri "$base/$asset"      -OutFile $binPath  -UseBasicParsing
Invoke-WebRequest -Uri "$base/SHA256SUMS"  -OutFile $sumsPath -UseBasicParsing

$want = ((Get-Content $sumsPath) |
  Where-Object { ($_ -split '\s+')[1].TrimStart('*') -eq $asset } |
  ForEach-Object { ($_ -split '\s+')[0] })
$got = (Get-FileHash -Algorithm SHA256 $binPath).Hash
if (-not $want) { throw "no checksum for $asset in SHA256SUMS" }
if ($got -ne $want) { throw "checksum mismatch for ${asset}: expected $want, got $got" }
Write-Host "Checksum verified."

& $binPath register
Remove-Item -Recurse -Force $tmp
