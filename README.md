# ssh-config-editor

A terminal editor for `~/.ssh/config`, shared across several git repositories.
Every change is committed, merged with your teammates' changes and pushed, with nothing for you to do.

## Installation

```sh
curl -fsSL https://raw.githubusercontent.com/romainlavabre/ssh-config-editor/master/install.sh | sh
```

The script installs `ssh-config-editor` into `/usr/local/bin`, asking for sudo only when needed. It downloads the binary from the latest release for Linux or macOS (amd64/arm64) and verifies its SHA-256 checksum. Go is not required.

- Specific version: `curl … | SSH_CONFIG_EDITOR_VERSION=1.0.0 sh`
- Without sudo: `curl … | SSH_CONFIG_EDITOR_BIN_DIR=$HOME/.local/bin sh`
- Uninstall: `curl … | sh -s -- --uninstall` (your data and `~/.ssh/config` are kept)
- Private repository: the script goes through `gh` when it is authenticated (`gh auth login`)

To update, run the same command again.

The machine only needs `git`, `ssh`, and access to the repositories, through an ssh key or git credentials that are already set up.

## Getting started

```sh
ssh-config-editor            # interactive UI
```

1. `R` then `a`: add a repository, for example `git@github.com:team/ssh-config.git`. An empty repository is fine.
2. `I`: move the Hosts already in `~/.ssh/config` into the repositories. They are grouped by prefix (`fairfair-live-*`, `my-pilot-*`…), and `~/.ssh/config` is backed up before being rewritten.
3. `n` / `e`: create or edit a Host. `ctrl+s` saves and pushes.

## How it works

`~/.ssh/config` starts with a block managed by the tool:

```
# >>> ssh-config-editor (generated, do not edit) >>>
Include ~/.config/ssh-config-editor/local.conf
Include ~/.local/share/ssh-config-editor/repos/team/*.conf
Include ~/.local/share/ssh-config-editor/repos/personal/*.conf
# <<< ssh-config-editor <<<
```

- **ssh itself does the merging**, through `Include`. If the tool goes away, your config keeps working.
- **In ssh, the first value wins.** `local.conf` comes first: that is where personal overrides go (your `User`, your `IdentityFile`), never shared. The repositories follow, in the priority order set with `K`/`J` on the repositories screen.
- Everything outside the block stays yours; the tool only rewrites it when you edit a Host that lives there.
- A Host defined in two places is flagged `⚠ shadowed` on the definition that does not apply.
- Before every write, `ssh -G` re-reads the config: an unknown option is rejected before it gets pushed.

### Synchronization

On every save, in the repository concerned: `commit` → `fetch` → merge → `push`. The other repositories are pulled at startup and with `r`.

Merging happens **Host by Host**, not line by line. Two teammates editing two neighbouring Hosts get no conflict, where git would report one. Only **the same Host changed on both sides** opens the conflict screen: `m` keeps your version, `t` keeps theirs, `e` lets you write the final version. Nothing is pushed until it is resolved.

If the network is down, the commit stays local (`↑1` in the status bar) and goes out on the next sync.

## Keys

| Key | Action |
|---|---|
| `enter` | connect to the Host |
| `/` | filter (name, IP, user) |
| `n` `e` `c` `x` | new, edit, duplicate, delete |
| `m` | move to another repository, `local` or `~/.ssh/config` |
| `r` | sync all repositories |
| `R` | manage repositories (add, remove, priority) |
| `I` | import `~/.ssh/config` |
| `C` | resume a pending conflict |
| `?` | help |

The form shows the common ssh directives as fields, grouped by section (Connection, Authentication, Forwarding, Session, Multiplexing, Storage). An empty field writes nothing; directives without a field are listed under "Kept as is" and written back untouched.

- `tab`/`↑↓` switch fields; `→` accepts the ProxyJump suggestion.
- yes/no-like options are inline choices (`● unset ○ yes ○ no`): `←/→` picks one.
- IdentityFile, Destination and File are lists where every option stays visible: `↑↓` inside the list, leaving it at the top or bottom moves to the neighbouring field. IdentityFile lists the keys in `~/.ssh`, plus "none" and "custom path"; File lists the repository's `.conf` files plus "new file". Typing on these lists switches to the typed entry.
- `h` (`alt+h` in a text field, where `h` is a letter) explains the field under the cursor.

## Command line

```sh
ssh-config-editor sync                     # all repositories, exit code 1 on conflict
ssh-config-editor ls                       # Host, HostName, User, source
ssh-config-editor repo add NAME URL [BRANCH]
ssh-config-editor repo rm NAME [--force]
ssh-config-editor import
```

For background sync, a systemd user timer:

```ini
# ~/.config/systemd/user/ssh-config-editor.service
[Service]
Type=oneshot
ExecStart=/usr/local/bin/ssh-config-editor sync

# ~/.config/systemd/user/ssh-config-editor.timer
[Timer]
OnCalendar=*:0/15
[Install]
WantedBy=timers.target
```

## Sharing rules

- **Never put a private key in a repository.** Only `HostName`, `Port`, `ProxyJump` and shared options.
- A shared `IdentityFile` assumes everyone names their key the same way. Otherwise, each person puts their own in `local.conf`:
  ```
  Host fairfair-*
    IdentityFile ~/.ssh/my-key
  ```

## Locations

| What | Where |
|---|---|
| Repository list | `~/.config/ssh-config-editor/config.toml` |
| Personal overrides | `~/.config/ssh-config-editor/local.conf` |
| Clones | `~/.local/share/ssh-config-editor/repos/<name>/` |
| Import backups | `~/.ssh/config.ssh-config-editor-bak-<date>` |

## Development

Go is not required on the machine, everything goes through the `golang:1.25` Docker image:

```sh
make test    # unit tests, git (two clones of the same remote) and end-to-end TUI
make build   # dist/ssh-config-editor
make dist    # linux and macOS, amd64/arm64
make release # dist + checksums.txt
```

Trying the current checkout on your real config, without touching it:

```sh
./run.sh              # or: make run
./run.sh ls           # any command
./run.sh --keep       # keep the sandbox in .sandbox/ across runs (./run.sh --reset to wipe it)
```

It builds the binary and runs it in a throwaway container where `HOME` has the same path as yours, seeded with a copy of `~/.ssh` (keys, `known_hosts`, config) and `~/.gitconfig`. Your real `~/.ssh` is mounted read-only. The container shares the host network and your ssh-agent, so connecting to a Host works (VPN included). Git pushes are real, though: add test repositories, not the team's.

Publishing a version (this is what `install.sh` downloads):

```sh
./tag.sh              # next patch version (1.2.3 → 1.2.4), 1.0.0 for the first one
./tag.sh minor        # or major, or an explicit 2.0.0
./tag.sh --dry-run    # show what would happen, change nothing
```

Tags are digits and dots only (`1.0.0`, no `v` prefix). From a clean, up-to-date `master`, it runs the tests, creates an annotated tag, pushes `master` and the tag atomically, then creates the GitHub release with the binaries, `checksums.txt` and `install.sh`. It asks for confirmation before doing anything.
