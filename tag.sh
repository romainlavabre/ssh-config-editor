#!/usr/bin/env bash
# Publishes a version: git tag, push, then a GitHub release with the binaries
# (what install.sh downloads).
#
#   ./tag.sh              next patch version (1.2.3 → 1.2.4)
#   ./tag.sh minor        1.2.3 → 1.3.0
#   ./tag.sh major        1.2.3 → 2.0.0
#   ./tag.sh 2.0.0        explicit version (digits and dots only)
#   ./tag.sh --dry-run    shows what would be done, without changing anything
set -euo pipefail

cd "$(dirname "$0")"

BRANCH=master
REMOTE=origin

if [[ -t 1 ]]; then
    BOLD=$'\033[1m'; GREEN=$'\033[32m'; YELLOW=$'\033[33m'; RED=$'\033[31m'; RESET=$'\033[0m'
else
    BOLD=""; GREEN=""; YELLOW=""; RED=""; RESET=""
fi
info() { printf '%s•%s %s\n' "$BOLD" "$RESET" "$*"; }
ok()   { printf '%s✓%s %s\n' "$GREEN" "$RESET" "$*"; }
warn() { printf '%s!%s %s\n' "$YELLOW" "$RESET" "$*" >&2; }
die()  { printf '%s✗%s %s\n' "$RED" "$RESET" "$*" >&2; exit 1; }

bump=patch
dry_run=false
for arg in "$@"; do
    case "$arg" in
        --dry-run) dry_run=true ;;
        patch | minor | major | [0-9]*) bump="$arg" ;;
        -h | --help) sed -n '2,10p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
        *) die "argument inconnu : $arg (patch, minor, major, X.Y.Z ou --dry-run)" ;;
    esac
done

# Runs the command, or prints it in --dry-run mode.
run() {
    if $dry_run; then
        printf '  %s[dry-run]%s %s\n' "$YELLOW" "$RESET" "$*"
    else
        "$@"
    fi
}

# ------------------------------------------------------------------- checks

for cmd in git gh docker make; do
    command -v "$cmd" >/dev/null || die "$cmd est introuvable"
done
gh auth status >/dev/null 2>&1 || die "gh n'est pas connecté : lancez « gh auth login »"

current=$(git branch --show-current)
[[ "$current" == "$BRANCH" ]] || die "vous êtes sur « $current » : une version se publie depuis $BRANCH"

if [[ -n "$(git status --porcelain)" ]]; then
    if $dry_run; then
        warn "des modifications ne sont pas commitées (bloquant hors --dry-run)"
    else
        git status --short >&2
        die "des modifications ne sont pas commitées"
    fi
fi

info "Récupération de $REMOTE…"
git fetch --quiet --tags "$REMOTE"
if git rev-parse --verify --quiet "$REMOTE/$BRANCH" >/dev/null; then
    behind=$(git rev-list --count "HEAD..$REMOTE/$BRANCH")
    ahead=$(git rev-list --count "$REMOTE/$BRANCH..HEAD")
else
    behind=0
    ahead=$(git rev-list --count HEAD)
fi
(( behind == 0 )) || die "$BRANCH est en retard de $behind commit(s) sur $REMOTE : faites un git pull d'abord"

# ------------------------------------------------------------------- version

SEMVER='^([0-9]+)\.([0-9]+)\.([0-9]+)$'

# Latest X.Y.Z tag (digits and dots only), in version order.
last=$(git tag --list '[0-9]*' --sort=-v:refname | grep -E "$SEMVER" | head -n 1 || true)
last=${last:-0.0.0}
if [[ "$bump" =~ ^[0-9] ]]; then
    next="$bump"
    [[ "$next" =~ $SEMVER ]] || die "version invalide : $next (attendu X.Y.Z, chiffres et points uniquement)"
else
    [[ "$last" =~ $SEMVER ]] || die "dernier tag illisible : $last"
    major=${BASH_REMATCH[1]} minor=${BASH_REMATCH[2]} patch=${BASH_REMATCH[3]}
    case "$bump" in
        major) next="$((major + 1)).0.0" ;;
        minor) next="$major.$((minor + 1)).0" ;;
        patch) next="$major.$minor.$((patch + 1))" ;;
    esac
    # First version: 1.0.0 rather than 0.0.1.
    [[ "$last" == 0.0.0 ]] && next=1.0.0
fi
if git rev-parse --verify --quiet "refs/tags/$next" >/dev/null; then
    die "le tag $next existe déjà"
fi

# ------------------------------------------------------------------ summary

echo
printf '  Version     %s%s%s  (précédente : %s)\n' "$BOLD" "$next" "$RESET" "$last"
printf '  Commit      %s\n' "$(git log -1 --format='%h %s')"
(( ahead > 0 )) && printf '  À pousser   %d commit(s) sur %s/%s\n' "$ahead" "$REMOTE" "$BRANCH"
if [[ "$last" != 0.0.0 ]]; then
    printf '  Changements depuis %s :\n' "$last"
    git log --format='    - %s' "$last..HEAD" | head -n 20
fi
echo

if ! $dry_run; then
    read -r -p "Tester, taguer, pousser et publier $next ? [o/N] " answer
    [[ "$answer" =~ ^[oOyY]$ ]] || die "annulé"
fi

# ---------------------------------------------------------------- publish

info "Tests…"
run make test

info "Tag $next…"
run git tag -a "$next" -m "ssh-config-editor $next"

info "Push de $BRANCH et $next…"
if ! run git push --atomic "$REMOTE" "$BRANCH" "refs/tags/$next"; then
    run git tag -d "$next"
    die "push refusé : le tag local a été supprimé, rien n'est publié"
fi

info "Release GitHub…"
run make publish VERSION="$next" || die "le tag est poussé mais la release a échoué : relancez « make publish VERSION=$next »"

echo
if $dry_run; then
    ok "dry-run terminé : rien n'a été modifié"
else
    ok "$next publiée : $(gh release view "$next" --json url --jq .url)"
    echo
    echo "  Installation / mise à jour :"
    echo "  curl -fsSL https://raw.githubusercontent.com/$(gh repo view --json nameWithOwner --jq .nameWithOwner)/$BRANCH/install.sh | sh"
fi
