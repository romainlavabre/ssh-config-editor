#!/bin/sh
# Installe ssh-config-editor dans /usr/local/bin, sans cloner le dépôt.
#
#   curl -fsSL https://raw.githubusercontent.com/romainlavabre/ssh-config-editor/master/install.sh | sh
#
# Options (variables d'environnement) :
#   SSH_CONFIG_EDITOR_VERSION   version à installer (défaut : latest), ex. v1.0.0
#   SSH_CONFIG_EDITOR_BIN_DIR   dossier d'installation (défaut : /usr/local/bin)
#   SSH_CONFIG_EDITOR_REPO      dépôt GitHub (défaut : romainlavabre/ssh-config-editor)
#   SSH_CONFIG_EDITOR_BASE_URL  URL où trouver les binaires, à la place de GitHub
#
# Désinstallation : ... | sh -s -- --uninstall
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

# Lance une commande avec sudo seulement si le dossier n'est pas accessible en écriture.
as_root() {
    if [ -w "$BIN_DIR" ] || { [ ! -e "$BIN_DIR" ] && [ -w "$(dirname "$BIN_DIR")" ]; }; then
        "$@"
    elif [ "$(id -u)" -eq 0 ]; then
        "$@"
    elif have sudo; then
        sudo "$@"
    else
        die "$BIN_DIR n'est pas accessible en écriture et sudo est absent. Relancez avec SSH_CONFIG_EDITOR_BIN_DIR=\$HOME/.local/bin"
    fi
}

uninstall() {
    target="$BIN_DIR/$NAME"
    [ -e "$target" ] || die "$target n'existe pas"
    as_root rm -f "$target"
    ok "$target supprimé"
    info "Vos données sont conservées : ~/.config/$NAME, ~/.local/share/$NAME et le bloc Include de ~/.ssh/config."
    exit 0
}

detect_platform() {
    case "$(uname -s)" in
        Linux)  os=linux ;;
        Darwin) os=darwin ;;
        *) die "système non pris en charge : $(uname -s) (Linux et macOS uniquement)" ;;
    esac
    case "$(uname -m)" in
        x86_64 | amd64)  arch=amd64 ;;
        aarch64 | arm64) arch=arm64 ;;
        *) die "architecture non prise en charge : $(uname -m)" ;;
    esac
    ASSET="$NAME-$os-$arch"
}

# download URL FICHIER
download() {
    if have curl; then
        curl -fsSL --retry 3 -o "$2" "$1"
    elif have wget; then
        wget -q -O "$2" "$1"
    else
        die "curl ou wget est nécessaire"
    fi
}

sha256() {
    if have sha256sum; then
        sha256sum "$1" | cut -d' ' -f1
    elif have shasum; then
        shasum -a 256 "$1" | cut -d' ' -f1
    else
        die "sha256sum ou shasum est nécessaire pour vérifier le binaire"
    fi
}

fetch() {
    if [ -n "$BASE_URL" ]; then
        download "$BASE_URL/$ASSET" "$TMP/$ASSET"
        download "$BASE_URL/checksums.txt" "$TMP/checksums.txt"
    elif have gh && gh auth status >/dev/null 2>&1; then
        # gh fonctionne aussi quand le dépôt est privé.
        tag=""
        [ "$VERSION" != "latest" ] && tag="$VERSION"
        # shellcheck disable=SC2086
        gh release download $tag --repo "$REPO" --dir "$TMP" --pattern "$ASSET" --pattern checksums.txt \
            || die "téléchargement impossible depuis $REPO (release $VERSION)"
    else
        if [ "$VERSION" = "latest" ]; then
            url="https://github.com/$REPO/releases/latest/download"
        else
            url="https://github.com/$REPO/releases/download/$VERSION"
        fi
        download "$url/$ASSET" "$TMP/$ASSET" \
            || die "téléchargement impossible : $url/$ASSET
  Si le dépôt est privé, installez gh et lancez « gh auth login » avant de relancer ce script."
        download "$url/checksums.txt" "$TMP/checksums.txt" || die "checksums.txt introuvable dans la release"
    fi
}

verify() {
    expected="$(grep " $ASSET\$" "$TMP/checksums.txt" | cut -d' ' -f1 || true)"
    [ -n "$expected" ] || die "$ASSET absent de checksums.txt"
    actual="$(sha256 "$TMP/$ASSET")"
    [ "$expected" = "$actual" ] || die "somme de contrôle invalide pour $ASSET (attendu $expected, obtenu $actual)"
}

main() {
    [ "${1:-}" = "--uninstall" ] && uninstall

    detect_platform
    info "Installation de $BOLD$NAME$RESET ($VERSION, $os/$arch) dans $BIN_DIR"

    TMP="$(mktemp -d)"
    trap 'rm -rf "$TMP"' EXIT INT TERM

    fetch
    verify
    chmod 0755 "$TMP/$ASSET"
    "$TMP/$ASSET" version >/dev/null 2>&1 || die "le binaire téléchargé ne s'exécute pas sur ce système"

    as_root mkdir -p "$BIN_DIR"
    as_root install -m 0755 "$TMP/$ASSET" "$BIN_DIR/$NAME"
    ok "$NAME $("$BIN_DIR/$NAME" version) installé dans $BIN_DIR/$NAME"

    case ":$PATH:" in
        *":$BIN_DIR:"*) ;;
        *) warn "$BIN_DIR n'est pas dans votre PATH : ajoutez  export PATH=\"$BIN_DIR:\$PATH\"  à votre ~/.bashrc ou ~/.zshrc" ;;
    esac
    have git || warn "git est introuvable : il est nécessaire pour synchroniser les dépôts"
    have ssh || warn "ssh est introuvable"

    printf '\n  Lancez %s%s%s, puis %sR%s pour ajouter un dépôt et %sI%s pour importer votre ~/.ssh/config.\n\n' \
        "$BOLD" "$NAME" "$RESET" "$BOLD" "$RESET" "$BOLD" "$RESET"
}

main "$@"
