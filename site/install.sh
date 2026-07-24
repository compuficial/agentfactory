#!/bin/sh
# AgentFactory installer — https://agentfactory.sh
#
#   curl -fsSL agentfactory.sh | sh
#
# Downloads the release binary for your platform, verifies its SHA-256
# checksum, and installs `af` to ~/.local/bin (override with AF_INSTALL_DIR).
# Pin a version with AF_VERSION=v0.1.0. POSIX sh; HTTPS only; no eval of
# anything it downloads.
set -eu

REPO="compuficial/agentfactory"
BINARY="af"
BINDIR="${AF_INSTALL_DIR:-$HOME/.local/bin}"

info() { printf '\033[36m==>\033[0m %s\n' "$1"; }
warn() { printf '\033[33mwarning:\033[0m %s\n' "$1" >&2; }
err()  { printf '\033[31merror:\033[0m %s\n' "$1" >&2; exit 1; }
need() { command -v "$1" >/dev/null 2>&1 || err "required command not found: $1"; }

need curl
need tar
need uname

os=$(uname -s | tr '[:upper:]' '[:lower:]')
arch=$(uname -m)
case "$arch" in
  x86_64 | amd64) arch=amd64 ;;
  arm64 | aarch64) arch=arm64 ;;
  *) err "unsupported architecture: $arch" ;;
esac
case "$os" in
  linux | darwin) ;;
  *) err "unsupported OS: $os (af runs on Linux and macOS)" ;;
esac

version="${AF_VERSION:-}"
if [ -z "$version" ]; then
  info "Finding the latest release"
  version=$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" \
    | grep '"tag_name"' | head -n1 | cut -d'"' -f4)
  [ -n "$version" ] || err "could not determine the latest version"
fi
v="${version#v}"

asset="${BINARY}_${v}_${os}_${arch}.tar.gz"
base="https://github.com/$REPO/releases/download/$version"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

info "Downloading $asset ($version)"
curl -fSL --proto '=https' --tlsv1.2 "$base/$asset" -o "$tmp/$asset" \
  || err "download failed: $base/$asset"
curl -fSL --proto '=https' --tlsv1.2 "$base/checksums.txt" -o "$tmp/checksums.txt" \
  || err "could not fetch checksums.txt"

info "Verifying checksum"
expected=$(grep " ${asset}\$" "$tmp/checksums.txt" | awk '{print $1}')
[ -n "$expected" ] || err "no checksum listed for $asset"
if command -v sha256sum >/dev/null 2>&1; then
  actual=$(sha256sum "$tmp/$asset" | awk '{print $1}')
elif command -v shasum >/dev/null 2>&1; then
  actual=$(shasum -a 256 "$tmp/$asset" | awk '{print $1}')
else
  err "need sha256sum or shasum to verify the download"
fi
[ "$expected" = "$actual" ] || err "checksum mismatch — refusing to install"

tar -xzf "$tmp/$asset" -C "$tmp"
[ -f "$tmp/$BINARY" ] || err "archive did not contain $BINARY"

mkdir -p "$BINDIR"
if ! install -m 0755 "$tmp/$BINARY" "$BINDIR/$BINARY" 2>/dev/null; then
  cp "$tmp/$BINARY" "$BINDIR/$BINARY" && chmod 0755 "$BINDIR/$BINARY"
fi

info "Installed $BINARY $version to $BINDIR/$BINARY"

case ":$PATH:" in
  *":$BINDIR:"*) ;;
  *)
    warn "$BINDIR is not on your PATH. Add it:"
    printf '      export PATH="%s:$PATH"\n' "$BINDIR"
    ;;
esac

command -v tmux >/dev/null 2>&1 \
  || warn "af needs tmux >= 3.2 — install it with your package manager (apt, brew, ...)"

printf '\033[35m*\033[0m af is ready. Next: \033[36maf doctor\033[0m\n'
