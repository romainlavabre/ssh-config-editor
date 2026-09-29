package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/romainlavabre/ssh-config-editor/internal/sshconfig"
)

// ImportGroup is a batch of Hosts from ~/.ssh/config sharing a prefix.
type ImportGroup struct {
	Key   string
	Hosts []*Host
}

// GroupKey groups aliases by their first two segments, digits removed:
// fairfair-dev4-node → fairfair-dev, my-pilot-live → my-pilot.
func GroupKey(name string) string {
	parts := strings.Split(name, "-")
	if len(parts) < 3 {
		return name
	}
	second := strings.TrimRightFunc(parts[1], unicode.IsDigit)
	if second == "" {
		second = parts[1]
	}
	return parts[0] + "-" + second
}

// ImportCandidates groups the concrete Hosts left in ~/.ssh/config.
// Wildcard blocks (Host *) and Match blocks are not included.
func (s *Store) ImportCandidates() []ImportGroup {
	var groups []ImportGroup
	idx := map[string]int{}
	for _, h := range s.Hosts {
		if h.Source.Kind != KindMain || !h.Block.IsConcrete() {
			continue
		}
		k := GroupKey(h.Name)
		i, ok := idx[k]
		if !ok {
			i = len(groups)
			idx[k] = i
			groups = append(groups, ImportGroup{Key: k})
		}
		groups[i].Hosts = append(groups[i].Hosts, h)
	}
	return groups
}

// Import moves each group to the chosen source (missing or ~/.ssh/config: the
// group stays where it is). In a repository, a group becomes <key>.conf.
// ~/.ssh/config is backed up before being rewritten. Returns the backup path
// and the repositories to sync.
func (s *Store) Import(assign map[string]*Source) (string, []*Source, error) {
	mainF, err := s.fresh(s.Paths.SSHConfig)
	if err != nil {
		return "", nil, err
	}
	changed := map[string]*sshconfig.File{s.Paths.SSHConfig: mainF}
	moved := 0
	for _, g := range s.ImportCandidates() {
		src := assign[g.Key]
		if src == nil || src.Kind == KindMain {
			continue
		}
		path, err := s.TargetPath(Target{Source: src, File: g.Key + ".conf"})
		if err != nil {
			return "", nil, err
		}
		f, ok := changed[path]
		if !ok {
			if f, err = s.fresh(path); err != nil {
				return "", nil, err
			}
			changed[path] = f
		}
		for _, h := range g.Hosts {
			b := mainF.Find(h.Name)
			if b == nil {
				continue
			}
			if f.Find(h.Name) != nil {
				return "", nil, fmt.Errorf("%s existe déjà dans %s", h.Name, filepath.Base(path))
			}
			f.Append(b.Clone())
			mainF.Remove(b)
			moved++
		}
	}
	if moved == 0 {
		return "", nil, errors.New("aucun Host à déplacer : choisissez une destination pour au moins un groupe")
	}
	backup := s.Paths.SSHConfig + ".ssh-config-editor-bak-" + time.Now().Format("20060102-150405")
	if err := os.WriteFile(backup, []byte(s.mainRaw), 0o600); err != nil {
		return "", nil, fmt.Errorf("sauvegarde de ~/.ssh/config impossible : %w", err)
	}
	touched, err := s.writeAll(changed)
	return backup, touched, err
}
