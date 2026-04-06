#!/usr/bin/env bash
# AIPlex CLI — Binary Installer
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/vamsiramakrishnan/aiplex/main/install.sh | sh
#   curl -fsSL ... | sh -s -- --version v0.2.0   # specific version
#
# Installs pre-built binary to ~/.local/bin/aiplex

set -euo pipefail

REPO="vamsiramakrishnan/aiplex"
INSTALL_DIR="${AIPLEX_INSTALL_DIR:-$HOME/.local/bin}"
VERSION="${1:-latest}"

RED='\033[0;31m'; GREEN='\033[0;32m'; BLUE='\033[0;34m'; NC='\033[0m'
info()  { echo -e "${BLUE}[INFO]${NC}  $*"; }
ok()    { echo -e "${GREEN}[OK]${NC}    $*"; }
err()   { echo -e "${RED}[ERROR]${NC} $*" >&2; exit 1; }

# ─── Detect platform ───────────────────────────────────────
detect_platform() {
    OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
    ARCH="$(uname -m)"
    case "$ARCH" in
        x86_64)  ARCH="amd64" ;;
        aarch64|arm64) ARCH="arm64" ;;
        *) err "Unsupported architecture: $ARCH" ;;
    esac
    case "$OS" in
        linux|darwin) ;;
        mingw*|msys*|cygwin*) OS="windows" ;;
        *) err "Unsupported OS: $OS" ;;
    esac
}

# ─── Resolve version ──────────────────────────────────────
resolve_version() {
    if [[ "$VERSION" == "latest" ]]; then
        VERSION=$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" \
            | grep '"tag_name"' | cut -d'"' -f4)
        if [[ -z "$VERSION" ]]; then
            err "Could not determine latest version. Specify with: --version v0.1.0"
        fi
    fi
    # Strip leading v for filename
    VERSION_NUM="${VERSION#v}"
}

# ─── Download + install ───────────────────────────────────
install() {
    local ext="tar.gz"
    [[ "$OS" == "windows" ]] && ext="zip"

    local filename="aiplex_${VERSION_NUM}_${OS}_${ARCH}.${ext}"
    local url="https://github.com/${REPO}/releases/download/${VERSION}/${filename}"

    info "Downloading aiplex ${VERSION} for ${OS}/${ARCH}..."

    local tmpdir
    tmpdir="$(mktemp -d)"
    trap 'rm -rf "$tmpdir"' EXIT

    curl -fsSL "$url" -o "${tmpdir}/${filename}" || err "Download failed. Check version: $VERSION"

    info "Installing to ${INSTALL_DIR}..."
    mkdir -p "$INSTALL_DIR"

    if [[ "$ext" == "zip" ]]; then
        unzip -o "${tmpdir}/${filename}" -d "$tmpdir" >/dev/null
    else
        tar -xzf "${tmpdir}/${filename}" -C "$tmpdir"
    fi

    mv "${tmpdir}/aiplex" "${INSTALL_DIR}/aiplex"
    chmod +x "${INSTALL_DIR}/aiplex"
}

# ─── Verify PATH ─────────────────────────────────────────
check_path() {
    if [[ ":$PATH:" != *":${INSTALL_DIR}:"* ]]; then
        echo ""
        info "Add to your PATH:"
        echo "  export PATH=\"${INSTALL_DIR}:\$PATH\""
        echo ""
        local shell_rc="$HOME/.bashrc"
        [[ "$SHELL" == */zsh ]] && shell_rc="$HOME/.zshrc"
        info "Or permanently:"
        echo "  echo 'export PATH=\"${INSTALL_DIR}:\$PATH\"' >> ${shell_rc}"
    fi
}

# ─── Main ────────────────────────────────────────────────
main() {
    echo ""
    echo "  AIPlex CLI Installer"
    echo ""

    detect_platform
    resolve_version
    install

    ok "aiplex ${VERSION} installed → ${INSTALL_DIR}/aiplex"
    check_path

    echo ""
    info "Get started:"
    echo "  aiplex init              # configure your GCP project"
    echo "  aiplex quickstart        # zero to running platform"
    echo ""
}

main "$@"
