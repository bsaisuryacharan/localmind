#!/usr/bin/env sh
# localmind installer (Linux / macOS)
# Usage: curl -fsSL https://raw.githubusercontent.com/bsaisuryacharan/localmind/main/install.sh | sh

set -eu

REPO="${LOCALMIND_REPO:-bsaisuryacharan/localmind}"
VERSION="${LOCALMIND_VERSION:-latest}"
INSTALL_DIR="${LOCALMIND_INSTALL_DIR:-$HOME/.localmind}"
BIN_DIR="${LOCALMIND_BIN_DIR:-$HOME/.local/bin}"

log()  { printf '\033[1;34m==>\033[0m %s\n' "$*"; }
err()  { printf '\033[1;31merror:\033[0m %s\n' "$*" >&2; exit 1; }

require() {
  command -v "$1" >/dev/null 2>&1 || err "missing dependency: $1"
}

require curl
require tar
command -v docker >/dev/null 2>&1 || log "warning: docker not found on PATH; localmind needs it to run"

OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"
case "$ARCH" in
  x86_64|amd64) ARCH=amd64 ;;
  aarch64|arm64) ARCH=arm64 ;;
  *) err "unsupported architecture: $ARCH" ;;
esac
case "$OS" in
  linux|darwin) ;;
  *) err "unsupported OS: $OS (use install.ps1 on Windows)" ;;
esac

log "installing localmind ($VERSION, $OS/$ARCH) to $INSTALL_DIR"

mkdir -p "$INSTALL_DIR" "$BIN_DIR"

if [ "$VERSION" = "latest" ]; then
  URL="https://github.com/$REPO/releases/latest/download/localmind-${OS}-${ARCH}.tar.gz"
else
  URL="https://github.com/$REPO/releases/download/$VERSION/localmind-${OS}-${ARCH}.tar.gz"
fi

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

log "downloading $URL"
curl -fsSL "$URL" -o "$TMP/localmind.tar.gz" || err "download failed"
tar -xzf "$TMP/localmind.tar.gz" -C "$TMP"

install -m 0755 "$TMP/localmind" "$BIN_DIR/localmind"

case ":$PATH:" in
  *":$BIN_DIR:"*) ;;
  *) log "add $BIN_DIR to your PATH (e.g. in ~/.bashrc or ~/.zshrc)" ;;
esac

log "installed: $BIN_DIR/localmind"
log "next: run \`localmind init\` to configure, then \`localmind up\`"
