#!/usr/bin/env bash
# Emulates the release archive for this machine and checks the first run of a
# user who installs only the archive (no clone, no Go, no Node on PATH):
#
#   1. builds the Console (when Node is available), then aiplex and aiplex-api
#      with release ldflags, CGO disabled, like .goreleaser.yaml;
#   2. packs them as goreleaser does: aiplex_<version>_<os>_<arch>.tar.gz with
#      the binaries at the archive root, plus checksums.txt;
#   3. installs that archive with install.sh (AIPLEX_DOWNLOAD_BASE=file://...)
#      into an empty directory, with an empty HOME and a PATH without Go;
#   4. runs aiplex version, aiplex dev --detach, a chat request, aiplex usage
#      records, aiplex dev status and aiplex dev stop.
#
# What it does not cover: goreleaser itself, other OS/arch archives, Homebrew,
# and downloading from GitHub. Exits non-zero on the first failure.
#
#   bash tools/release-smoke.sh            # or: make release-smoke
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORK="$(mktemp -d)"
DEV_HOME="$WORK/home"
trap 'HOME="$DEV_HOME" "$WORK/install/aiplex" dev stop >/dev/null 2>&1 || true; rm -rf "$WORK"' EXIT

step() { printf '\n==> %s\n' "$*"; }
fail() { printf '\nRELEASE SMOKE FAILED: %s\n' "$*" >&2; exit 1; }

OS="$(go env GOOS)"; ARCH="$(go env GOARCH)"
VERSION="0.0.0-smoke"
COMMIT="$(git -C "$ROOT" rev-parse --short HEAD 2>/dev/null || echo unknown)"
DIST="$WORK/dist"; STAGE="$WORK/stage"
mkdir -p "$DIST" "$STAGE" "$DEV_HOME" "$WORK/install"

step "Console"
CONSOLE_RESTORE=""
if command -v npm >/dev/null; then
    cp "$ROOT/console/dist/index.html" "$WORK/placeholder-index.html"
    CONSOLE_RESTORE=1
    (cd "$ROOT/console" && npm ci --no-audit --no-fund >/dev/null && npm run build >/dev/null) || fail "console build"
else
    echo "npm not found: the archive will carry the placeholder Console (aiplex version says so)"
fi

step "Build aiplex and aiplex-api ($OS/$ARCH, release ldflags)"
(cd "$ROOT" && CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=$VERSION -X main.commit=$COMMIT" -o "$STAGE/aiplex" ./cmd/aiplex-cli) || fail "build aiplex"
(cd "$ROOT" && CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o "$STAGE/aiplex-api" ./cmd/aiplex-api) || fail "build aiplex-api"
if [[ -n "$CONSOLE_RESTORE" ]]; then
    # Leave the source tree as it was: the committed placeholder page.
    rm -rf "$ROOT/console/dist/assets" "$ROOT/console/dist/favicon.svg"
    cp "$WORK/placeholder-index.html" "$ROOT/console/dist/index.html"
fi
cp "$ROOT/LICENSE" "$ROOT/README.md" "$ROOT/CHANGELOG.md" "$STAGE/"

step "Pack aiplex_${VERSION}_${OS}_${ARCH}.tar.gz"
ARCHIVE="aiplex_${VERSION}_${OS}_${ARCH}.tar.gz"
tar -czf "$DIST/$ARCHIVE" -C "$STAGE" aiplex aiplex-api LICENSE README.md CHANGELOG.md
(cd "$DIST" && if command -v sha256sum >/dev/null; then sha256sum "$ARCHIVE"; else shasum -a 256 "$ARCHIVE"; fi) > "$DIST/checksums.txt"
tar -tzf "$DIST/$ARCHIVE"

step "install.sh from the local archive"
mkdir -p "$WORK/download/v$VERSION" && cp "$DIST"/* "$WORK/download/v$VERSION/"
AIPLEX_DOWNLOAD_BASE="file://$WORK/download" AIPLEX_INSTALL_DIR="$WORK/install" HOME="$DEV_HOME" bash "$ROOT/install.sh" --version "v$VERSION" || fail "install.sh"

CLEAN_PATH="$WORK/install:/usr/bin:/bin"
run() { env -i HOME="$DEV_HOME" PATH="$CLEAN_PATH" TERM=dumb "$@"; }

step "aiplex version"
run aiplex version | tee "$WORK/version.txt"
grep -q "aiplex version $VERSION" "$WORK/version.txt" || fail "version"
grep -q "api:      $WORK/install/aiplex-api" "$WORK/version.txt" || fail "aiplex-api not found next to aiplex"

step "aiplex dev --detach (no Go on PATH, no source tree)"
PORT=$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1])')
CPORT=$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1])')
(cd "$WORK" && run aiplex dev --detach --port "$PORT" --console-port "$CPORT") || fail "aiplex dev --detach"

step "chat request through the local access point"
eval "$(run aiplex dev env)"
curl --noproxy '*' -fsS "$AIPLEX_URL/ap/local/v1/chat/completions" -H "Authorization: Bearer $AIPLEX_TOKEN" \
    -H "Content-Type: application/json" -d '{"model":"mock-gpt","messages":[{"role":"user","content":"hi"}]}' | grep -q "mock model" || fail "chat"

step "aiplex usage records / dev status / dev stop"
run aiplex usage records | grep -q mock-gpt || fail "usage records"
run aiplex dev status || fail "dev status"
run aiplex dev stop || fail "dev stop"
if run aiplex dev status >/dev/null 2>&1; then fail "dev status after stop"; fi

printf '\nRelease smoke passed (%s/%s).\n' "$OS" "$ARCH"
