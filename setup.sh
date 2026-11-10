#!/usr/bin/env bash
# AIPlex — One-command setup
#
# Installs the aiplex CLI and all dev tools, then runs `aiplex init`.
# Everything after this is managed by the CLI itself.
#
# Usage:
#   ./setup.sh --local                            # local only: Go, then `aiplex dev`
#   ./setup.sh                                    # interactive cloud setup
#   ./setup.sh --project vital-octagon-19612      # non-interactive cloud setup
#
# --local needs only Go 1.25+ (from your PATH or mise) and installs no cloud
# tools; it builds aiplex and aiplex-api and tells you to run `aiplex dev`.
#
# What it does:
#   1. Installs mise (tool version manager) if missing
#   2. Installs tools from .mise.toml (go, terraform, helm, kubectl, node)
#   3. Builds and installs the `aiplex` CLI
#   4. Runs `aiplex init` (interactive GCP setup wizard)
#
# After setup, the CLI is self-sufficient:
#   aiplex platform apply    # deploy everything
#   aiplex status            # check health
#   aiplex doctor            # diagnose issues

set -euo pipefail

RED='\033[0;31m'; GREEN='\033[0;32m'; BLUE='\033[0;34m'; NC='\033[0m'
info()  { echo -e "${BLUE}[INFO]${NC}  $*"; }
ok()    { echo -e "${GREEN}[OK]${NC}    $*"; }
err()   { echo -e "${RED}[ERROR]${NC} $*" >&2; }

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# ─── Step 1: mise ───────────────────────────────────────────
install_mise() {
    if command -v mise &>/dev/null; then
        ok "mise $(mise --version)"
        return
    fi

    if [[ -x "$HOME/.local/bin/mise" ]]; then
        export PATH="$HOME/.local/bin:$PATH"
        ok "mise $(mise --version)"
        return
    fi

    info "Installing mise (tool version manager)..."
    curl -fsSL https://mise.run | sh
    export PATH="$HOME/.local/bin:$PATH"
    ok "mise $(mise --version) installed"

    # Add to shell rc if not present
    local shell_rc="$HOME/.bashrc"
    [[ "$SHELL" == */zsh ]] && shell_rc="$HOME/.zshrc"
    local shell_name=bash
    [[ "$SHELL" == */zsh ]] && shell_name=zsh
    if ! grep -q 'mise activate' "$shell_rc" 2>/dev/null; then
        echo "eval \"\$(~/.local/bin/mise activate ${shell_name})\"" >> "$shell_rc"
    fi
}

# ─── Step 2: Tools from .mise.toml ─────────────────────────
install_tools() {
    if [[ ! -f "$SCRIPT_DIR/.mise.toml" ]]; then
        err "No .mise.toml found in $SCRIPT_DIR"
        exit 1
    fi

    mise trust "$SCRIPT_DIR/.mise.toml" 2>/dev/null || true
    eval "$(mise activate bash)" 2>/dev/null || true

    info "Installing tools from .mise.toml..."
    (cd "$SCRIPT_DIR" && mise install --yes)
    ok "Tools installed"
    (cd "$SCRIPT_DIR" && mise ls --current 2>/dev/null) | while read -r line; do
        echo "       $line"
    done
}

# ─── Step 3: Build + install aiplex CLI ────────────────────
install_cli() {
    if ! command -v go &>/dev/null && command -v mise &>/dev/null; then
        # Use mise-managed go
        eval "$(mise activate bash)" 2>/dev/null || true
    fi
    if ! command -v go &>/dev/null; then
        err "Go is not installed. Install Go $(sed -n 's/^go //p' "$SCRIPT_DIR/go.mod")+ from https://go.dev/dl (or run ./setup.sh without --local to install it with mise)"
        exit 1
    fi
    local need have
    need="$(sed -n 's/^go //p' "$SCRIPT_DIR/go.mod")"
    have="$(go env GOVERSION | sed 's/^go//')"
    if [[ "$(printf '%s\n%s\n' "$need" "$have" | sort -V | head -1)" != "$need" ]]; then
        err "Go $have is older than $need (required by go.mod). Install a newer Go from https://go.dev/dl"
        exit 1
    fi

    info "Building aiplex and aiplex-api..."
    (cd "$SCRIPT_DIR" && go build -trimpath -ldflags "-s -w" -o bin/aiplex ./cmd/aiplex-cli && go build -trimpath -ldflags "-s -w" -o bin/aiplex-api ./cmd/aiplex-api)

    # Install to a location on PATH; aiplex-api sits beside aiplex for `aiplex dev`.
    local install_dir="$HOME/.local/bin"
    mkdir -p "$install_dir"
    cp "$SCRIPT_DIR/bin/aiplex" "$install_dir/aiplex"
    cp "$SCRIPT_DIR/bin/aiplex-api" "$install_dir/aiplex-api"
    chmod +x "$install_dir/aiplex" "$install_dir/aiplex-api"
    local on_path=1
    [[ ":$PATH:" == *":$install_dir:"* ]] || on_path=0
    export PATH="$install_dir:$PATH"
    ok "aiplex CLI installed → $install_dir/aiplex (with aiplex-api)"
    if [[ $on_path == 0 ]]; then
        info "Add it to your PATH: export PATH=\"$install_dir:\$PATH\""
    fi
}

# ─── Step 4: Run aiplex init ──────────────────────────────
run_init() {
    echo ""
    info "Launching aiplex init..."
    echo ""
    (cd "$SCRIPT_DIR" && aiplex init "$@")
}

# ─── Main ───────────────────────────────────────────────────
main() {
    echo ""
    echo "  ╔══════════════════════════════════════╗"
    echo "  ║         AIPlex Setup                  ║"
    echo "  ╚══════════════════════════════════════╝"
    echo ""

    if [[ "${1:-}" == "--local" ]]; then
        install_cli
        echo ""
        ok "Ready. Run AIPlex locally (mock model, tools, usage, Console when built):"
        echo "    aiplex dev"
        return
    fi

    install_mise
    install_tools
    install_cli

    echo ""
    ok "Environment ready"
    echo ""

    # Pass all args through to aiplex init
    run_init "$@"
}

main "$@"
