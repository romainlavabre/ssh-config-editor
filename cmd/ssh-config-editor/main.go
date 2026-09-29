// ssh-config-editor: a terminal editor for ~/.ssh/config, synced across
// several git repositories.
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

const usage = `ssh-config-editor: edits ~/.ssh/config and shares it through git repositories.

Usage:
  ssh-config-editor                     interactive UI
  ssh-config-editor import              moves the Hosts of ~/.ssh/config into the repositories
  ssh-config-editor sync                syncs every repository (for a timer)
  ssh-config-editor ls                  lists the Hosts and their source
  ssh-config-editor repo ls             lists the repositories
  ssh-config-editor repo add NAME URL [BRANCH]
  ssh-config-editor repo rm NAME [--force]
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
		// Recover a ~/.ssh/config rewritten by hand or by another tool.
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
	return fmt.Errorf("unknown command: %s", cmd)
}

func cmdSync(st *store.Store) error {
	conflicts := 0
	for _, src := range st.Repos() {
		res, err := src.Git().Sync("")
		switch {
		case err != nil:
			fmt.Printf("✗ %s: %v\n", src.Name, err)
		case len(res.Conflicts) > 0:
			conflicts++
			fmt.Printf("✗ %s: conflict, run ssh-config-editor to resolve it\n", src.Name)
		case res.Offline:
			fmt.Printf("! %s: offline, commits kept locally (%s)\n", src.Name, strings.TrimSpace(res.Warning))
		default:
			fmt.Printf("✓ %s\n", src.Name)
		}
	}
	if conflicts > 0 {
		return errors.New("pending conflict(s)")
	}
	return nil
}

func cmdLs(st *store.Store) error {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "HOST\tHOSTNAME\tUSER\tSOURCE\tFILE")
	for _, h := range st.Hosts {
		source := h.Source.Label()
		if h.ShadowedBy != nil {
			source += " (shadowed by " + h.ShadowedBy.Label() + ")"
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
			return errors.New("usage: repo add NAME URL [BRANCH]")
		}
		r := store.Repo{Name: args[1], URL: args[2]}
		if len(args) > 3 {
			r.Branch = args[3]
		}
		if err := st.AddRepo(r); err != nil {
			return err
		}
		fmt.Printf("✓ %s cloned and added to %s\n", r.Name, st.Paths.SSHConfig)
		return nil
	case "rm":
		if len(args) < 2 {
			return errors.New("usage: repo rm NAME [--force]")
		}
		force := len(args) > 2 && args[2] == "--force"
		if err := st.RemoveRepo(args[1], force); err != nil {
			if errors.Is(err, store.ErrUnpushed) {
				return fmt.Errorf("%w (rerun with --force to discard them)", err)
			}
			return err
		}
		fmt.Printf("✓ %s removed\n", args[1])
		return nil
	}
	return fmt.Errorf("unknown subcommand: repo %s", sub)
}
