// ssh-config-editor : éditeur terminal de ~/.ssh/config, synchronisé sur
// plusieurs dépôts git.
package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/romainlavabre/ssh-config-editor/internal/store"
	"github.com/romainlavabre/ssh-config-editor/internal/tui"
)

var version = "dev"

const usage = `ssh-config-editor : édite ~/.ssh/config et le partage via des dépôts git.

Usage :
  ssh-config-editor                     interface interactive
  ssh-config-editor import              range les Host de ~/.ssh/config dans les dépôts
  ssh-config-editor sync                synchronise tous les dépôts (pour un timer)
  ssh-config-editor ls                  liste les Host et leur source
  ssh-config-editor repo ls             liste les dépôts
  ssh-config-editor repo add NOM URL [BRANCHE]
  ssh-config-editor repo rm NOM [--force]
  ssh-config-editor version
`

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "✗", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	cmd := ""
	if len(args) > 0 {
		cmd = args[0]
	}
	switch cmd {
	case "help", "-h", "--help":
		fmt.Print(usage)
		return nil
	case "version", "-v", "--version":
		fmt.Println(version)
		return nil
	}

	p, err := store.DefaultPaths()
	if err != nil {
		return err
	}
	st, err := store.Open(p)
	if err != nil {
		return err
	}
	if len(st.Config.Repos) > 0 {
		// Rattrape un ~/.ssh/config réécrit à la main ou par un autre outil.
		if err := st.EnsureManaged(); err != nil {
			return err
		}
	}

	switch cmd {
	case "", "tui":
		return tui.Run(st, tui.StartMain)
	case "import":
		return tui.Run(st, tui.StartImport)
	case "sync":
		return cmdSync(st)
	case "ls":
		return cmdLs(st)
	case "repo":
		return cmdRepo(st, args[1:])
	}
	fmt.Fprint(os.Stderr, usage)
	return fmt.Errorf("commande inconnue : %s", cmd)
}

func cmdSync(st *store.Store) error {
	conflicts := 0
	for _, src := range st.Repos() {
		res, err := src.Git().Sync("")
		switch {
		case err != nil:
			fmt.Printf("✗ %s : %v\n", src.Name, err)
		case len(res.Conflicts) > 0:
			conflicts++
			fmt.Printf("✗ %s : conflit, lancez ssh-config-editor pour le résoudre\n", src.Name)
		case res.Offline:
			fmt.Printf("! %s : hors ligne, commits gardés en local (%s)\n", src.Name, strings.TrimSpace(res.Warning))
		default:
			fmt.Printf("✓ %s\n", src.Name)
		}
	}
	if conflicts > 0 {
		return errors.New("conflit(s) en attente")
	}
	return nil
}

func cmdLs(st *store.Store) error {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "HOST\tHOSTNAME\tUSER\tSOURCE\tFICHIER")
	for _, h := range st.Hosts {
		source := h.Source.Label()
		if h.ShadowedBy != nil {
			source += " (masqué par " + h.ShadowedBy.Label() + ")"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", h.Name, h.Block.Get("HostName"), h.Block.Get("User"), source, h.File())
	}
	return w.Flush()
}

func cmdRepo(st *store.Store, args []string) error {
	sub := ""
	if len(args) > 0 {
		sub = args[0]
	}
	switch sub {
	case "", "ls":
		for i, src := range st.Repos() {
			fmt.Printf("%d. %s\t%s (%s)\n", i+1, src.Name, src.Repo.URL, src.Repo.Branch)
		}
		return nil
	case "add":
		if len(args) < 3 {
			return errors.New("usage : repo add NOM URL [BRANCHE]")
		}
		r := store.Repo{Name: args[1], URL: args[2]}
		if len(args) > 3 {
			r.Branch = args[3]
		}
		if err := st.AddRepo(r); err != nil {
			return err
		}
		fmt.Printf("✓ %s cloné et ajouté à %s\n", r.Name, st.Paths.SSHConfig)
		return nil
	case "rm":
		if len(args) < 2 {
			return errors.New("usage : repo rm NOM [--force]")
		}
		force := len(args) > 2 && args[2] == "--force"
		if err := st.RemoveRepo(args[1], force); err != nil {
			if errors.Is(err, store.ErrUnpushed) {
				return fmt.Errorf("%w (relancez avec --force pour les perdre)", err)
			}
			return err
		}
		fmt.Printf("✓ %s retiré\n", args[1])
		return nil
	}
	return fmt.Errorf("sous-commande inconnue : repo %s", sub)
}
