#!/bin/sh
set -e

REPO="drgould/multi-dev-proxy"
BINARY="mdp"

die() { echo "Error: $1" >&2; exit 1; }

OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)

case "$OS" in
  linux|darwin) ;;
  *) die "Unsupported OS: $OS. Use 'npm install -g mdp' on Windows." ;;
esac

case "$ARCH" in
  x86_64)  ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
  *) die "Unsupported architecture: $ARCH" ;;
esac

if [ -z "$VERSION" ]; then
  VERSION=$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" \
    | grep '"tag_name"' | sed 's/.*"tag_name": *"\([^"]*\)".*/\1/')
fi
[ -z "$VERSION" ] && die "Could not determine latest version"

ARCHIVE="${BINARY}_${OS}_${ARCH}.tar.gz"
URL="https://github.com/${REPO}/releases/download/${VERSION}/${ARCHIVE}"

TMPDIR=$(mktemp -d)
trap 'rm -rf "$TMPDIR"' EXIT

echo "Downloading mdp ${VERSION} for ${OS}/${ARCH}..."
curl -fsSL "$URL" -o "$TMPDIR/$ARCHIVE"

if command -v sha256sum >/dev/null 2>&1; then
  CHECKSUMS_URL="https://github.com/${REPO}/releases/download/${VERSION}/checksums.txt"
  curl -fsSL "$CHECKSUMS_URL" -o "$TMPDIR/checksums.txt"
  (cd "$TMPDIR" && grep "$ARCHIVE" checksums.txt | sha256sum --check --status) \
    || die "Checksum verification failed"
fi

tar xzf "$TMPDIR/$ARCHIVE" -C "$TMPDIR"

INSTALL_DIR="/usr/local/bin"
if [ ! -w "$INSTALL_DIR" ]; then
  INSTALL_DIR="$HOME/.local/bin"
  mkdir -p "$INSTALL_DIR"
fi

install -m755 "$TMPDIR/$BINARY" "$INSTALL_DIR/$BINARY"

# macOS: strip quarantine attribute so Gatekeeper doesn't block the binary
if [ "$OS" = "darwin" ]; then
  xattr -d com.apple.quarantine "$INSTALL_DIR/$BINARY" 2>/dev/null || true
fi

echo "mdp ${VERSION} installed to ${INSTALL_DIR}/${BINARY}"

if ! echo "$PATH" | grep -q "$INSTALL_DIR"; then
  echo "Note: Add ${INSTALL_DIR} to your PATH if not already present."
fi
