# ssh-config-editor

Éditeur terminal de `~/.ssh/config`, partagé à travers plusieurs dépôts git.
Chaque modification est commitée, fusionnée avec les modifications des collègues, puis poussée, sans rien avoir à faire.

## Installation

Un seul binaire statique, sans dépendance (Go n'est pas nécessaire) :

```sh
cp ssh-config-editor-linux-amd64 ~/.local/bin/ssh-config-editor
```

Le poste a seulement besoin de `git` et `ssh`, et d'un accès aux dépôts, par clé ssh ou par identifiants git déjà configurés.

## Démarrage

```sh
ssh-config-editor            # interface
```

1. `R` puis `a` : ajouter un dépôt, par exemple `git@github.com:equipe/ssh-config.git`. Un dépôt vide convient.
2. `I` : ranger les Host existants de `~/.ssh/config` dans les dépôts. Ils sont regroupés par préfixe (`fairfair-live-*`, `my-pilot-*`…), et `~/.ssh/config` est sauvegardé avant d'être réécrit.
3. `n` / `e` : créer ou éditer un Host. `ctrl+s` enregistre et pousse.

## Fonctionnement

`~/.ssh/config` commence par un bloc géré par l'outil :

```
# >>> ssh-config-editor (généré, ne pas éditer) >>>
Include ~/.config/ssh-config-editor/local.conf
Include ~/.local/share/ssh-config-editor/repos/equipe/*.conf
Include ~/.local/share/ssh-config-editor/repos/perso/*.conf
# <<< ssh-config-editor <<<
```

- **C'est ssh qui fusionne**, via `Include`. Si l'outil disparaît, la config continue de marcher.
- **En ssh, la première valeur gagne.** `local.conf` passe en premier : c'est là que vont les surcharges personnelles (votre `User`, votre `IdentityFile`), jamais partagées. Viennent ensuite les dépôts, dans l'ordre de priorité réglé avec `K`/`J` dans l'écran des dépôts.
- Tout ce qui est en dehors du bloc reste à vous, et l'outil ne le réécrit que si vous éditez un Host qui s'y trouve.
- Un même Host défini à deux endroits est signalé par `⚠ masqué` sur la définition qui ne s'applique pas.
- Avant chaque écriture, `ssh -G` relit la config : une option inconnue est refusée avant d'être poussée.

### Synchronisation

À chaque enregistrement, dans le dépôt concerné : `commit` → `fetch` → fusion → `push`. Les autres dépôts sont tirés au lancement et avec `r`.

La fusion se fait **Host par Host**, pas ligne par ligne. Deux collègues qui modifient deux Host voisins n'ont aucun conflit, là où git en verrait un. Seul un **même Host modifié des deux côtés** ouvre l'écran de conflit : `m` garde votre version, `t` la leur, `e` permet de rédiger la version finale. Rien n'est poussé tant que ce n'est pas tranché.

Si le réseau est coupé, le commit reste en local (`↑1` dans la barre d'état) et part à la prochaine synchronisation.

## Touches

| Touche | Action |
|---|---|
| `enter` | se connecter au Host |
| `/` | filtrer (nom, IP, user) |
| `n` `e` `c` `x` | nouveau, éditer, dupliquer, supprimer |
| `m` | déplacer vers un autre dépôt, `local` ou `~/.ssh/config` |
| `r` | synchroniser tous les dépôts |
| `R` | gérer les dépôts (ajout, retrait, priorité) |
| `I` | importer `~/.ssh/config` |
| `C` | reprendre un conflit en attente |
| `?` | aide |

Dans le formulaire : `tab`/`↑↓` pour changer de champ, `→` pour accepter la suggestion (clés de `~/.ssh`, Host connus pour ProxyJump), `←/→` sur Destination pour changer de dépôt.

## Ligne de commande

```sh
ssh-config-editor sync                     # tous les dépôts, code 1 si conflit
ssh-config-editor ls                       # Host, HostName, User, source
ssh-config-editor repo add NOM URL [BRANCHE]
ssh-config-editor repo rm NOM [--force]
ssh-config-editor import
```

Pour une synchronisation en tâche de fond, un timer systemd utilisateur :

```ini
# ~/.config/systemd/user/ssh-config-editor.service
[Service]
Type=oneshot
ExecStart=%h/.local/bin/ssh-config-editor sync

# ~/.config/systemd/user/ssh-config-editor.timer
[Timer]
OnCalendar=*:0/15
[Install]
WantedBy=timers.target
```

## Règles de partage

- **Jamais de clé privée dans un dépôt.** Seulement `HostName`, `Port`, `ProxyJump` et les options communes.
- Un `IdentityFile` partagé suppose que tout le monde nomme sa clé pareil. Sinon, chacun met le sien dans `local.conf` :
  ```
  Host fairfair-*
    IdentityFile ~/.ssh/ma-cle
  ```

## Emplacements

| Quoi | Où |
|---|---|
| Liste des dépôts | `~/.config/ssh-config-editor/config.toml` |
| Surcharges perso | `~/.config/ssh-config-editor/local.conf` |
| Clones | `~/.local/share/ssh-config-editor/repos/<nom>/` |
| Sauvegardes d'import | `~/.ssh/config.ssh-config-editor-bak-<date>` |

## Développement

Go n'est pas requis sur le poste, tout passe par l'image Docker `golang:1.25` :

```sh
make test    # tests unitaires, git (deux clones d'un même distant) et TUI de bout en bout
make build   # dist/ssh-config-editor
make dist    # linux amd64/arm64, macOS arm64
```
