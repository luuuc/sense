#!/bin/sh
# install.sh — install the sense binary from GitHub Releases.
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/luuuc/sense/main/install.sh | sh
#
# Installs to /usr/local/bin/sense (or ~/.local/bin/sense if
# /usr/local/bin is not writable). Verifies the SHA256 checksum of the
# downloaded archive.

set -eu

REPO="luuuc/sense"

# --- Detect OS and architecture -------------------------------------------

OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"

case "$OS" in
  darwin|linux) ;;
  *) echo "Error: unsupported OS: $OS" >&2; exit 1 ;;
esac

case "$ARCH" in
  x86_64|amd64)  ARCH="amd64" ;;
  arm64|aarch64) ARCH="arm64" ;;
  *) echo "Error: unsupported architecture: $ARCH" >&2; exit 1 ;;
esac

echo "Detected platform: ${OS}/${ARCH}"

# --- Resolve latest version -----------------------------------------------

echo "Fetching latest sense release..."

if command -v curl >/dev/null 2>&1; then
  fetch() { curl -fsSL "$1"; }
  download() { curl -fsSL -o "$1" "$2"; }
elif command -v wget >/dev/null 2>&1; then
  fetch() { wget -qO- "$1"; }
  download() { wget -qO "$1" "$2"; }
else
  echo "Error: curl or wget is required" >&2
  exit 1
fi

# Extract tag_name from the JSON response without requiring jq.
TAG="$(fetch "https://api.github.com/repos/${REPO}/releases/latest" \
  | grep '"tag_name"' | head -1 | sed 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/')"

if [ -z "$TAG" ]; then
  echo "Error: could not determine latest release" >&2
  exit 1
fi

VERSION="${TAG#v}"
echo "Latest version: ${VERSION}"

# --- Download archive and checksums ---------------------------------------

ARCHIVE="sense_${VERSION}_${OS}_${ARCH}.tar.gz"
CHECKSUMS="sense_${VERSION}_checksums.txt"
BASE_URL="https://github.com/${REPO}/releases/download/${TAG}"

WORK_DIR="$(mktemp -d)"
trap 'rm -rf "$WORK_DIR"' EXIT

echo "Downloading ${ARCHIVE}..."
download "${WORK_DIR}/${ARCHIVE}" "${BASE_URL}/${ARCHIVE}"
download "${WORK_DIR}/${CHECKSUMS}" "${BASE_URL}/${CHECKSUMS}"

# --- Verify checksum ------------------------------------------------------

# Match the exact filename in field 2, not a substring.
EXPECTED="$(awk -v a="${ARCHIVE}" '$2 == a {print $1}' "${WORK_DIR}/${CHECKSUMS}")"

if [ -z "$EXPECTED" ]; then
  echo "Error: checksum not found for ${ARCHIVE}" >&2
  exit 1
fi

if command -v sha256sum >/dev/null 2>&1; then
  ACTUAL="$(sha256sum "${WORK_DIR}/${ARCHIVE}" | awk '{print $1}')"
elif command -v shasum >/dev/null 2>&1; then
  ACTUAL="$(shasum -a 256 "${WORK_DIR}/${ARCHIVE}" | awk '{print $1}')"
else
  echo "Error: sha256sum or shasum is required" >&2
  exit 1
fi

if [ "$EXPECTED" != "$ACTUAL" ]; then
  echo "Error: checksum mismatch" >&2
  echo "  expected: ${EXPECTED}" >&2
  echo "  actual:   ${ACTUAL}" >&2
  exit 1
fi

echo "Checksum verified."

# --- Extract and install --------------------------------------------------

tar -xzf "${WORK_DIR}/${ARCHIVE}" -C "${WORK_DIR}"

if [ -w /usr/local/bin ]; then
  INSTALL_DIR="/usr/local/bin"
else
  INSTALL_DIR="${HOME}/.local/bin"
  mkdir -p "$INSTALL_DIR"
  PATH_HINT=1
fi

# GoReleaser archives place the binary at the archive root (no subdirectory).
mv "${WORK_DIR}/sense" "${INSTALL_DIR}/sense"
chmod +x "${INSTALL_DIR}/sense"

echo ""
echo "sense ${VERSION} installed to ${INSTALL_DIR}/sense"
"${INSTALL_DIR}/sense" version

if [ "${PATH_HINT:-}" = "1" ]; then
  echo ""
  echo "Note: add ${INSTALL_DIR} to your PATH if it's not already there."
fi
