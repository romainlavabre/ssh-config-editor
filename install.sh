#!/bin/sh
# Installs ssh-config-editor into /usr/local/bin, without cloning the repository.
#
#   curl -fsSL https://raw.githubusercontent.com/romainlavabre/ssh-config-editor/master/install.sh | sh
#
# Options (environment variables):
#   SSH_CONFIG_EDITOR_VERSION   version to install (default: latest), e.g. 1.0.0
#   SSH_CONFIG_EDITOR_BIN_DIR   install directory (default: /usr/local/bin)
#   SSH_CONFIG_EDITOR_REPO      GitHub repository (default: romainlavabre/ssh-config-editor)
#   SSH_CONFIG_EDITOR_BASE_URL  URL to fetch the binaries from, instead of GitHub
#
# Uninstall: ... | sh -s -- --uninstall
set -eu

REPO="${SSH_CONFIG_EDITOR_REPO:-romainlavabre/ssh-config-editor}"
VERSION="${SSH_CONFIG_EDITOR_VERSION:-latest}"
BIN_DIR="${SSH_CONFIG_EDITOR_BIN_DIR:-/usr/local/bin}"
BASE_URL="${SSH_CONFIG_EDITOR_BASE_URL:-}"
NAME="ssh-config-editor"

if [ -t 1 ]; then
    BOLD="$(printf '\033[1m')"; GREEN="$(printf '\033[32m')"; YELLOW="$(printf '\033[33m')"
    RED="$(printf '\033[31m')"; RESET="$(printf '\033[0m')"
else
    BOLD=""; GREEN=""; YELLOW=""; RED=""; RESET=""
fi

info() { printf '%s•%s %s\n' "$BOLD" "$RESET" "$*"; }
ok()   { printf '%s✓%s %s\n' "$GREEN" "$RESET" "$*"; }
warn() { printf '%s!%s %s\n' "$YELLOW" "$RESET" "$*" >&2; }
die()  { printf '%s✗%s %s\n' "$RED" "$RESET" "$*" >&2; exit 1; }

have() { command -v "$1" >/dev/null 2>&1; }

# Runs a command with sudo only when the directory is not writable.
as_root() {
    if [ -w "$BIN_DIR" ] || { [ ! -e "$BIN_DIR" ] && [ -w "$(dirname "$BIN_DIR")" ]; }; then
        "$@"
    elif [ "$(id -u)" -eq 0 ]; then
        "$@"
    elif have sudo; then
        sudo "$@"
    else
        die "$BIN_DIR is not writable and sudo is missing. Rerun with SSH_CONFIG_EDITOR_BIN_DIR=\$HOME/.local/bin"
    fi
}

uninstall() {
    target="$BIN_DIR/$NAME"
    [ -e "$target" ] || die "$target does not exist"
    as_root rm -f "$target"
    ok "$target removed"
    info "Your data is kept: ~/.config/$NAME, ~/.local/share/$NAME and the Include block in ~/.ssh/config."
    exit 0
}

detect_platform() {
    case "$(uname -s)" in
        Linux)  os=linux ;;
        Darwin) os=darwin ;;
        *) die "unsupported system: $(uname -s) (Linux and macOS only)" ;;
    esac
    case "$(uname -m)" in
        x86_64 | amd64)  arch=amd64 ;;
        aarch64 | arm64) arch=arm64 ;;
        *) die "unsupported architecture: $(uname -m)" ;;
    esac
    ASSET="$NAME-$os-$arch"
}

# download URL FILE
download() {
    if have curl; then
        curl -fsSL --retry 3 -o "$2" "$1"
    elif have wget; then
        wget -q -O "$2" "$1"
    else
        die "curl or wget is required"
    fi
}

sha256() {
    if have sha256sum; then
        sha256sum "$1" | cut -d' ' -f1
    elif have shasum; then
        shasum -a 256 "$1" | cut -d' ' -f1
    else
        die "sha256sum or shasum is required to verify the binary"
    fi
}

fetch() {
    if [ -n "$BASE_URL" ]; then
        download "$BASE_URL/$ASSET" "$TMP/$ASSET"
        download "$BASE_URL/checksums.txt" "$TMP/checksums.txt"
    elif have gh && gh auth status >/dev/null 2>&1; then
        # gh also works when the repository is private.
        tag=""
        [ "$VERSION" != "latest" ] && tag="$VERSION"
        # shellcheck disable=SC2086
        gh release download $tag --repo "$REPO" --dir "$TMP" --pattern "$ASSET" --pattern checksums.txt \
            || die "cannot download from $REPO (release $VERSION)"
    else
        if [ "$VERSION" = "latest" ]; then
            url="https://github.com/$REPO/releases/latest/download"
        else
            url="https://github.com/$REPO/releases/download/$VERSION"
        fi
        download "$url/$ASSET" "$TMP/$ASSET" \
            || die "cannot download: $url/$ASSET
  If the repository is private, install gh and run \"gh auth login\" before running this script again."
        download "$url/checksums.txt" "$TMP/checksums.txt" || die "checksums.txt not found in the release"
    fi
}

verify() {
    expected="$(grep " $ASSET\$" "$TMP/checksums.txt" | cut -d' ' -f1 || true)"
    [ -n "$expected" ] || die "$ASSET missing from checksums.txt"
    actual="$(sha256 "$TMP/$ASSET")"
    [ "$expected" = "$actual" ] || die "invalid checksum for $ASSET (expected $expected, got $actual)"
}

main() {
    [ "${1:-}" = "--uninstall" ] && uninstall

    detect_platform
    info "Installing $BOLD$NAME$RESET ($VERSION, $os/$arch) into $BIN_DIR"

    TMP="$(mktemp -d)"
    trap 'rm -rf "$TMP"' EXIT INT TERM

    fetch
    verify
    chmod 0755 "$TMP/$ASSET"
    "$TMP/$ASSET" version >/dev/null 2>&1 || die "the downloaded binary does not run on this system"

    as_root mkdir -p "$BIN_DIR"
    as_root install -m 0755 "$TMP/$ASSET" "$BIN_DIR/$NAME"
    ok "$NAME $("$BIN_DIR/$NAME" version) installed in $BIN_DIR/$NAME"

    case ":$PATH:" in
        *":$BIN_DIR:"*) ;;
        *) warn "$BIN_DIR is not in your PATH: add  export PATH=\"$BIN_DIR:\$PATH\"  to your ~/.bashrc or ~/.zshrc" ;;
    esac
    have git || warn "git not found: it is required to sync the repositories"
    have ssh || warn "ssh not found"

    printf '\n  Run %s%s%s, then %sR%s to add a repository and %sI%s to import your ~/.ssh/config.\n\n' \
        "$BOLD" "$NAME" "$RESET" "$BOLD" "$RESET" "$BOLD" "$RESET"
}

main "$@"
