# Local cross-compile of all release binaries into .\dist (mirrors CI).
# Usage:  .\build.ps1 [-Version 4.0.0]
param([string]$Version = "dev")

$ErrorActionPreference = 'Stop'
$env:CGO_ENABLED = '0'

$targets = @(
  'windows/amd64', 'windows/arm64',
  'darwin/amd64',  'darwin/arm64',
  'linux/amd64',   'linux/arm64'
)

$dist = Join-Path $PSScriptRoot 'dist'
New-Item -ItemType Directory -Force -Path $dist | Out-Null

foreach ($t in $targets) {
  $os, $arch = $t.Split('/')
  $ext = if ($os -eq 'windows') { '.exe' } else { '' }
  $out = Join-Path $dist "claude-acc_${os}_${arch}$ext"
  Write-Host "building $out"
  $env:GOOS = $os; $env:GOARCH = $arch
  go build -trimpath -ldflags "-s -w -X main.version=$Version" -o $out .
}

Get-ChildItem $dist -File | Get-FileHash -Algorithm SHA256 |
  ForEach-Object { "{0}  {1}" -f $_.Hash.ToLower(), (Split-Path $_.Path -Leaf) } |
  Set-Content -Encoding ascii (Join-Path $dist 'SHA256SUMS')

Write-Host "done -> $dist"
