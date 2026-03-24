#!/usr/bin/env bash
set -euo pipefail

# Squadron Installer
# Usage: curl -fsSL https://raw.githubusercontent.com/mlund01/squadron/main/install.sh | bash

REPO="mlund01/squadron"
BINARY="squadron"
INSTALL_DIR="${INSTALL_DIR:-${HOME}/.local/bin}"

BOLD='\033[1m'
DIM='\033[2m'
GREEN='\033[32m'
RED='\033[31m'
YELLOW='\033[33m'
NC='\033[0m'

info()    { echo -e "${DIM}·${NC} $*"; }
success() { echo -e "${GREEN}✓${NC} $*"; }
warn()    { echo -e "${YELLOW}!${NC} $*"; }
error()   { echo -e "${RED}✗${NC} $*" >&2; }

# Detect OS
detect_os() {
  case "$(uname -s)" in
    Darwin) echo "darwin" ;;
    Linux)  echo "linux" ;;
    MINGW*|MSYS*|CYGWIN*) echo "windows" ;;
    *) error "Unsupported OS: $(uname -s)"; exit 1 ;;
  esac
}

# Detect arch
detect_arch() {
  case "$(uname -m)" in
    x86_64|amd64)   echo "amd64" ;;
    arm64|aarch64)   echo "arm64" ;;
    *) error "Unsupported architecture: $(uname -m)"; exit 1 ;;
  esac
}

# Ensure PATH includes install dir in shell rc
ensure_path() {
  local dir="$1"

  # Already on PATH?
  case ":${PATH}:" in
    *":${dir}:"*) return 0 ;;
  esac

  local rc_file=""
  local shell_name="${SHELL:-/bin/bash}"
  case "$shell_name" in
    */zsh)  rc_file="${HOME}/.zshrc" ;;
    */bash)
      if [ -f "${HOME}/.bash_profile" ]; then
        rc_file="${HOME}/.bash_profile"
      else
        rc_file="${HOME}/.bashrc"
      fi
      ;;
    */fish) rc_file="${HOME}/.config/fish/config.fish" ;;
    *)      rc_file="${HOME}/.profile" ;;
  esac

  local line="export PATH=\"${dir}:\$PATH\""
  if [ "$shell_name" = "*/fish" ]; then
    line="set -gx PATH ${dir} \$PATH"
  fi

  if [ -n "$rc_file" ]; then
    # Don't add if already present
    if [ -f "$rc_file" ] && grep -qF "$dir" "$rc_file" 2>/dev/null; then
      return 0
    fi
    echo "" >> "$rc_file"
    echo "# Added by squadron installer" >> "$rc_file"
    echo "$line" >> "$rc_file"
    warn "${dir} added to PATH in ${rc_file}"
    warn "Restart your shell or run: ${line}"
  fi
}

OS="$(detect_os)"
ARCH="$(detect_arch)"

# Get version
VERSION="${1:-}"
if [ -z "$VERSION" ]; then
  VERSION="$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" | grep '"tag_name"' | cut -d'"' -f4)"
fi
if [ -z "$VERSION" ]; then
  error "Failed to determine latest version"
  exit 1
fi

echo -e "${BOLD}Squadron Installer${NC}"
echo ""
info "OS: ${OS}/${ARCH}"
info "Version: ${VERSION}"
info "Install dir: ${INSTALL_DIR}"
echo ""

# Build download URL
EXT="tar.gz"
[ "$OS" = "windows" ] && EXT="zip"
FILENAME="${BINARY}_${OS}_${ARCH}.${EXT}"
URL="https://github.com/${REPO}/releases/download/${VERSION}/${FILENAME}"

# Download
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

info "Downloading ${FILENAME}..."
if ! curl -fsSL "$URL" -o "${TMP}/${FILENAME}"; then
  error "Download failed — check that ${VERSION} exists at ${URL}"
  exit 1
fi

# Verify checksum
CHECKSUMS_URL="https://github.com/${REPO}/releases/download/${VERSION}/checksums.txt"
if curl -fsSL "$CHECKSUMS_URL" -o "${TMP}/checksums.txt" 2>/dev/null; then
  EXPECTED="$(grep "${FILENAME}" "${TMP}/checksums.txt" | awk '{print $1}')"
  if [ -n "$EXPECTED" ]; then
    if command -v sha256sum &>/dev/null; then
      ACTUAL="$(sha256sum "${TMP}/${FILENAME}" | awk '{print $1}')"
    else
      ACTUAL="$(shasum -a 256 "${TMP}/${FILENAME}" | awk '{print $1}')"
    fi
    if [ "$EXPECTED" != "$ACTUAL" ]; then
      error "Checksum mismatch!"
      error "  Expected: ${EXPECTED}"
      error "  Actual:   ${ACTUAL}"
      exit 1
    fi
    success "Checksum verified"
  fi
else
  warn "Could not download checksums — skipping verification"
fi

# Extract
info "Extracting..."
if [ "$EXT" = "zip" ]; then
  unzip -q "${TMP}/${FILENAME}" -d "${TMP}/extracted"
else
  mkdir -p "${TMP}/extracted"
  tar -xzf "${TMP}/${FILENAME}" -C "${TMP}/extracted"
fi

# Install
mkdir -p "$INSTALL_DIR"
mv "${TMP}/extracted/${BINARY}" "${INSTALL_DIR}/${BINARY}"
chmod +x "${INSTALL_DIR}/${BINARY}"

success "Installed to ${INSTALL_DIR}/${BINARY}"

# Ensure on PATH
ensure_path "$INSTALL_DIR"

echo ""
success "${BOLD}squadron ${VERSION} is ready!${NC}"
echo -e "  Run ${BOLD}squadron --help${NC} to get started."
