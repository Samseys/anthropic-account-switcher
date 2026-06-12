#!/bin/sh
# One-line installer for claude-acc on macOS and Linux.
#   curl -fsSL https://raw.githubusercontent.com/Samseys/anthropic-account-switcher/main/install.sh | sh
#
# To install the rolling nightly pre-release instead of the latest stable
# release, set the env var or pass --nightly:
#   curl -fsSL https://.../install.sh | CLAUDE_ACC_NIGHTLY=1 sh
#   curl -fsSL https://.../install.sh | sh -s -- --nightly
#
# Downloads the selected release binary, verifies it against the published
# SHA256SUMS, installs it into ~/.local/bin, then runs `register` to put that
# directory on PATH. The installer owns file placement: the binary never copies
# or rewrites itself.
set -eu

repo="Samseys/anthropic-account-switcher"

os="$(uname -s)"
case "$os" in
  Darwin) os="darwin" ;;
  Linux)  os="linux" ;;
  *) echo "unsupported OS: $os" >&2; exit 1 ;;
esac

arch="$(uname -m)"
case "$arch" in
  x86_64|amd64)  arch="amd64" ;;
  arm64|aarch64) arch="arm64" ;;
  *) echo "unsupported architecture: $arch" >&2; exit 1 ;;
esac

asset="claude-acc_${os}_${arch}"

# Nightly is an opt-in rolling pre-release; everyone else tracks latest stable.
channel="latest"
if [ "${CLAUDE_ACC_NIGHTLY:-}" = "1" ] || [ "${1:-}" = "--nightly" ] || [ "${1:-}" = "-n" ]; then
  channel="nightly"
  base="https://github.com/${repo}/releases/download/nightly"
else
  base="https://github.com/${repo}/releases/latest/download"
fi

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

echo "Downloading ${asset} (${channel}) ..."
curl -fsSL "${base}/${asset}"     -o "${tmp}/${asset}"
curl -fsSL "${base}/SHA256SUMS"   -o "${tmp}/SHA256SUMS"

want="$(awk -v f="$asset" '{ n=$2; sub(/^\*/,"",n); if (n==f) print $1 }' "${tmp}/SHA256SUMS")"
if [ -z "$want" ]; then
  echo "no checksum for ${asset} in SHA256SUMS" >&2
  exit 1
fi
if command -v sha256sum >/dev/null 2>&1; then
  got="$(sha256sum "${tmp}/${asset}" | awk '{print $1}')"
else
  got="$(shasum -a 256 "${tmp}/${asset}" | awk '{print $1}')"
fi
if [ "$got" != "$want" ]; then
  echo "checksum mismatch for ${asset}: expected ${want}, got ${got}" >&2
  exit 1
fi
echo "Checksum verified."

# Install location must match the Go installDir(): ~/.local/bin/claude-acc.
dir="${HOME}/.local/bin"
dest="${dir}/claude-acc"
mkdir -p "$dir"
cp "${tmp}/${asset}" "$dest"
chmod 0755 "$dest"
echo "Installed to ${dest}"

"$dest" register
