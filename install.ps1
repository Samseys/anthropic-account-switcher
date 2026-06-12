# One-line installer for acc-claude on Windows.
#   irm https://raw.githubusercontent.com/Samseys/anthropic-account-switcher/main/install.ps1 | iex
#
# To install the rolling nightly pre-release instead of the latest stable
# release, set the env var first (works through the piped one-liner):
#   $env:ACC_CLAUDE_NIGHTLY = '1'; irm https://.../install.ps1 | iex
#
# Downloads the selected release binary for this machine's architecture, verifies
# it against the published SHA256SUMS, installs it into %LOCALAPPDATA%\acc-claude,
# then runs `register` to put that directory on PATH. The installer owns file
# placement: the binary never copies or rewrites itself.
param([switch]$Nightly)

$ErrorActionPreference = 'Stop'
$repo = 'Samseys/anthropic-account-switcher'

$arch = if ($env:PROCESSOR_ARCHITECTURE -eq 'ARM64') { 'arm64' } else { 'amd64' }
$asset = "acc-claude_windows_$arch.exe"

# Nightly is an opt-in rolling pre-release; everyone else tracks latest stable.
if ($Nightly -or $env:ACC_CLAUDE_NIGHTLY -eq '1') {
  $channel = 'nightly'
  $base = "https://github.com/$repo/releases/download/nightly"
} else {
  $channel = 'latest'
  $base = "https://github.com/$repo/releases/latest/download"
}

$tmp = Join-Path ([System.IO.Path]::GetTempPath()) "acc-claude-install"
Remove-Item -Recurse -Force -ErrorAction SilentlyContinue -LiteralPath $tmp
New-Item -ItemType Directory -Path $tmp | Out-Null
$binPath  = Join-Path $tmp $asset
$sumsPath = Join-Path $tmp 'SHA256SUMS'

Write-Host "Downloading $asset ($channel) ..."
Invoke-WebRequest -Uri "$base/$asset"      -OutFile $binPath  -UseBasicParsing
Invoke-WebRequest -Uri "$base/SHA256SUMS"  -OutFile $sumsPath -UseBasicParsing

$want = ((Get-Content $sumsPath) |
  Where-Object { ($_ -split '\s+')[1].TrimStart('*') -eq $asset } |
  ForEach-Object { ($_ -split '\s+')[0] })
$got = (Get-FileHash -Algorithm SHA256 $binPath).Hash
if (-not $want) { throw "no checksum for $asset in SHA256SUMS" }
if ($got -ne $want) { throw "checksum mismatch for ${asset}: expected $want, got $got" }
Write-Host "Checksum verified."

# Install location must match the Go installDir(): %LOCALAPPDATA%\acc-claude.
$dir  = Join-Path $env:LOCALAPPDATA 'acc-claude'
$dest = Join-Path $dir 'acc-claude.exe'
New-Item -ItemType Directory -Force -Path $dir | Out-Null
Copy-Item -LiteralPath $binPath -Destination $dest -Force
Write-Host "Installed to $dest"

& $dest register

Remove-Item -Recurse -Force -ErrorAction SilentlyContinue -LiteralPath $tmp
