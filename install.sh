#!/usr/bin/env bash
# AIPlex CLI — Binary Installer
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/vamsiramakrishnan/aiplex/main/install.sh | bash
#   curl -fsSL ... | bash -s -- --version v0.2.0   # specific version
#
# Installs the pre-built aiplex CLI (and aiplex-api, used by `aiplex dev`,
# when the release archive carries it) to ~/.local/bin (AIPLEX_INSTALL_DIR).
# AIPLEX_DOWNLOAD_BASE overrides the release download URL (for mirrors and tests).
# The archive is checked against the release's checksums.txt before anything is installed;
# a missing checksums.txt is an error. Nothing runs with sudo: the default directory is in $HOME.

set -euo pipefail

REPO="vamsiramakrishnan/aiplex"
INSTALL_DIR="${AIPLEX_INSTALL_DIR:-$HOME/.local/bin}"
DOWNLOAD_BASE="${AIPLEX_DOWNLOAD_BASE:-https://github.com/${REPO}/releases/download}"
VERSION="latest"
while [[ $# -gt 0 ]]; do
    case "$1" in
        --version) [[ $# -ge 2 ]] || { echo "--version needs a value, e.g. --version v0.2.0" >&2; exit 1; }; VERSION="$2"; shift 2 ;;
        --version=*) VERSION="${1#--version=}"; shift ;;
        -h|--help) sed -n '2,12p' "$0" 2>/dev/null || true; exit 0 ;;
        v[0-9]*) VERSION="$1"; shift ;;  # bare version, as before
        *) echo "unknown argument: $1 (use --version vX.Y.Z)" >&2; exit 1 ;;
    esac
done

RED='\033[0;31m'; GREEN='\033[0;32m'; BLUE='\033[0;34m'; NC='\033[0m'
info()  { echo -e "${BLUE}[INFO]${NC}  $*"; }
ok()    { echo -e "${GREEN}[OK]${NC}    $*"; }
err()   { echo -e "${RED}[ERROR]${NC} $*" >&2; exit 1; }

# The version becomes part of a URL and a file name, so accept only a tag-shaped value.
if [[ "$VERSION" != "latest" ]]; then
    [[ "$VERSION" =~ ^v?[0-9]+\.[0-9]+\.[0-9]+([-+][0-9A-Za-z.+-]+)?$ ]] \
        || err "invalid version '${VERSION}' (expected latest or vX.Y.Z)"
    [[ "$VERSION" == v* ]] || VERSION="v${VERSION}"
fi

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
    command -v curl >/dev/null || err "curl is required"
    command -v tar >/dev/null || err "tar is required"
    if [[ "$VERSION" == "latest" ]]; then
        local body
        body="$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" 2>/dev/null)" \
            || err "Could not reach the GitHub API for the latest release. Specify with: --version v0.1.0"
        # With pipefail, a grep with no match would exit the script silently; keep it out of the status.
        VERSION="$(printf '%s\n' "$body" | { grep '"tag_name"' || true; } | cut -d'"' -f4)"
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
    local url="${DOWNLOAD_BASE}/${VERSION}/${filename}"

    info "Downloading aiplex ${VERSION} for ${OS}/${ARCH}..."

    tmpdir="$(mktemp -d)"
    trap 'rm -rf "${tmpdir:-}"' EXIT

    # A missing asset (no build for this OS/arch in this release) is the common failure; say so.
    if ! curl -fsSL "$url" -o "${tmpdir}/${filename}" 2>/dev/null; then
        err "No ${filename} in release ${VERSION}: this release has no build for ${OS}/${ARCH}, or the download failed (${url}). Releases: https://github.com/${REPO}/releases"
    fi

    # Verify the archive against the release checksums. Fail closed: a release always
    # publishes checksums.txt, so a missing or unreadable one is an error, not a skip.
    curl -fsSL "${DOWNLOAD_BASE}/${VERSION}/checksums.txt" -o "${tmpdir}/checksums.txt" 2>/dev/null \
        || err "Could not download checksums.txt for ${VERSION}; refusing to install an unverified archive"
    local want got
    want="$(awk -v f="$filename" '$2 == f || $2 == "*"f {print $1}' "${tmpdir}/checksums.txt")"
    [[ -n "$want" ]] || err "checksums.txt does not list ${filename}"
    if command -v sha256sum >/dev/null; then
        got="$(sha256sum "${tmpdir}/${filename}" | awk '{print $1}')"
    else
        got="$(shasum -a 256 "${tmpdir}/${filename}" | awk '{print $1}')"
    fi
    [[ "$want" == "$got" ]] || err "checksum mismatch for ${filename} (expected ${want}, got ${got})"
    ok "Checksum verified"

    info "Installing to ${INSTALL_DIR}..."
    mkdir -p "$INSTALL_DIR"

    if [[ "$ext" == "zip" ]]; then
        command -v unzip >/dev/null || err "unzip is required to unpack ${filename}"
        unzip -o "${tmpdir}/${filename}" -d "$tmpdir" >/dev/null
    else
        tar -xzf "${tmpdir}/${filename}" -C "$tmpdir"
    fi

    local suffix=""
    [[ "$OS" == "windows" ]] && suffix=".exe"
    [[ -f "${tmpdir}/aiplex${suffix}" ]] || err "archive ${filename} does not contain the aiplex binary"
    mv "${tmpdir}/aiplex${suffix}" "${INSTALL_DIR}/aiplex${suffix}"
    chmod +x "${INSTALL_DIR}/aiplex${suffix}"
    if [[ -f "${tmpdir}/aiplex-api${suffix}" ]]; then
        mv "${tmpdir}/aiplex-api${suffix}" "${INSTALL_DIR}/aiplex-api${suffix}"
        chmod +x "${INSTALL_DIR}/aiplex-api${suffix}"
        HAVE_API=1
    fi
}

# ─── Verify PATH ─────────────────────────────────────────
check_path() {
    if [[ ":$PATH:" != *":${INSTALL_DIR}:"* ]]; then
        echo ""
        info "Add to your PATH:"
        echo "  export PATH=\"${INSTALL_DIR}:\$PATH\""
        echo ""
        local shell_rc="$HOME/.bashrc"
        [[ "${SHELL:-}" == */zsh ]] && shell_rc="$HOME/.zshrc"
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
    "${INSTALL_DIR}/aiplex" version 2>/dev/null | sed 's/^/  /' || true
    check_path

    echo ""
    info "Get started:"
    if [[ "${HAVE_API:-0}" == "1" ]]; then
        echo "  aiplex dev               # run locally: mock model, tools, usage (no cloud account)"
    else
        echo "  (this release has no aiplex-api binary; for local mode build from source: make build && bin/aiplex dev)"
    fi
    echo "  aiplex --help            # every command, grouped by task"
    echo ""
    info "Shell completion (optional):"
    echo "  bash: echo 'source <(aiplex completion bash)' >> ~/.bashrc   (needs the bash-completion package)"
    echo "  zsh:  echo 'source <(aiplex completion zsh)' >> ~/.zshrc"
    echo "  fish: aiplex completion fish > ~/.config/fish/completions/aiplex.fish"
    echo ""
}

main "$@"
