#!/usr/bin/env bash
# Runs the current checkout against a copy of your ~/.ssh, inside a throwaway
# container: the real ~/.ssh is mounted read-only and never modified.
#
#   ./run.sh              interactive UI
#   ./run.sh ls           any ssh-config-editor command
#   ./run.sh --keep       keep the sandbox home in .sandbox/ across runs
#   ./run.sh --reset      delete .sandbox/ and exit
#
# Inside the container, HOME has the same path as on the host, so ~/.ssh/...
# in IdentityFile resolves the same way. Hosts reachable from the host (VPN
# included) are reachable too: the container shares the host network.
# Pushes to real git repositories are real: use test repositories.
set -euo pipefail

cd "$(dirname "$0")"

GO_IMAGE="${GO_IMAGE:-golang:1.25}"
SANDBOX="$PWD/.sandbox/home"

keep=false
args=()
for arg in "$@"; do
    case "$arg" in
        --keep) keep=true ;;
        --reset) rm -rf .sandbox; echo "✓ .sandbox removed"; exit 0 ;;
        *) args+=("$arg") ;;
    esac
done

[[ -d "$HOME/.ssh" ]] || { echo "✗ $HOME/.ssh not found" >&2; exit 1; }

make --no-print-directory build >&2

uid=$(id -u)
gid=$(id -g)
docker_args=(
    run --rm -i
    --network host
    --user "$uid:$gid"
    -v /etc/passwd:/etc/passwd:ro
    -v /etc/group:/etc/group:ro
    -v "$HOME/.ssh:/host/ssh:ro"
    -v "$PWD/dist/ssh-config-editor:/usr/local/bin/ssh-config-editor:ro"
    -e HOME="$HOME"
    -e TERM="${TERM:-xterm-256color}"
    -e COLORTERM="${COLORTERM:-}"
    -e LANG=C.UTF-8
    -w "$HOME"
)
[[ -t 0 && -t 1 ]] && docker_args+=(-t)

if $keep; then
    mkdir -p "$SANDBOX"
    docker_args+=(-v "$SANDBOX:$HOME")
else
    docker_args+=(--tmpfs "$HOME:uid=$uid,gid=$gid,mode=0700,exec")
fi

# Reuse the host ssh-agent for keys with a passphrase.
if [[ -n "${SSH_AUTH_SOCK:-}" && -S "$SSH_AUTH_SOCK" ]]; then
    docker_args+=(-v "$SSH_AUTH_SOCK:/host/ssh-agent" -e SSH_AUTH_SOCK=/host/ssh-agent)
fi
# Same git identity as on the host.
[[ -f "$HOME/.gitconfig" ]] && docker_args+=(-v "$HOME/.gitconfig:/host/gitconfig:ro")

# First run of the sandbox: copy ~/.ssh (keys, known_hosts, config) and the git identity.
setup='
if [ ! -e "$HOME/.ssh" ]; then
    cp -a /host/ssh "$HOME/.ssh" && chmod 700 "$HOME/.ssh"
fi
if [ -f /host/gitconfig ] && [ ! -e "$HOME/.gitconfig" ]; then
    cp /host/gitconfig "$HOME/.gitconfig"
fi
exec ssh-config-editor "$@"
'

exec docker "${docker_args[@]}" "$GO_IMAGE" sh -c "$setup" ssh-config-editor ${args[@]+"${args[@]}"}
