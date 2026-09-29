package store

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/romainlavabre/ssh-config-editor/internal/gitsync"
	"github.com/romainlavabre/ssh-config-editor/internal/sshconfig"
)

// Kind distingue les trois sortes de sources.
type Kind int

const (
	KindLocal Kind = iota // ~/.config/ssh-config-editor/local.conf
	KindRepo              // un dépôt git partagé
	KindMain              // ce qui reste dans ~/.ssh/config
)

// Source est un endroit où vivent des Host.
type Source struct {
	Kind Kind
	Name string
	Repo *Repo
	Dir  string
}

// Label est le nom affiché.
func (s *Source) Label() string {
	switch s.Kind {
	case KindLocal:
		return "local"
	case KindMain:
		return "~/.ssh/config"
	}
	return s.Name
}

// Git renvoie le clone d'un dépôt.
func (s *Source) Git() gitsync.Repo {
	return gitsync.Repo{Dir: s.Dir, URL: s.Repo.URL, Branch: s.Repo.Branch}
}

// Host est un bloc Host et l'endroit où il est défini.
type Host struct {
	Name       string
	Source     *Source
	Path       string
	Block      *sshconfig.Block
	ShadowedBy *Source // non nil : un Host du même nom est défini avant, c'est lui qui s'applique
	Duplicated bool    // défini aussi ailleurs
}

// File renvoie le nom du fichier qui définit le Host.
func (h *Host) File() string { return filepath.Base(h.Path) }

// Store est la vue consolidée de toutes les sources.
type Store struct {
	Paths   Paths
	Config  *Config
	Sources []*Source
	Hosts   []*Host
	files   map[string]*sshconfig.File
	mainRaw string
}

// Open charge la configuration de l'outil et toutes les sources.
func Open(p Paths) (*Store, error) {
	c, err := LoadConfig(p)
	if err != nil {
		return nil, err
	}
	s := &Store{Paths: p, Config: c}
	return s, s.Reload()
}

// Reload relit tous les fichiers.
func (s *Store) Reload() error {
	s.files = map[string]*sshconfig.File{}
	s.Hosts = nil
	// Les Source restent les mêmes objets d'un rechargement à l'autre : les
	// appelants peuvent garder un pointeur.
	prev := map[string]*Source{}
	for _, src := range s.Sources {
		prev[fmt.Sprint(src.Kind, "/", src.Name)] = src
	}
	reuse := func(src Source) *Source {
		if old, ok := prev[fmt.Sprint(src.Kind, "/", src.Name)]; ok {
			*old = src
			return old
		}
		return &src
	}
	s.Sources = []*Source{reuse(Source{Kind: KindLocal, Name: "local"})}
	for i := range s.Config.Repos {
		r := &s.Config.Repos[i]
		s.Sources = append(s.Sources, reuse(Source{Kind: KindRepo, Name: r.Name, Repo: r, Dir: s.Paths.RepoDir(r.Name)}))
	}
	s.Sources = append(s.Sources, reuse(Source{Kind: KindMain, Name: "ssh-config"}))

	raw, err := os.ReadFile(s.Paths.SSHConfig)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	s.mainRaw = string(raw)

	for _, src := range s.Sources {
		paths, err := s.sourceFiles(src)
		if err != nil {
			return err
		}
		for _, path := range paths {
			f, err := s.load(path)
			if err != nil {
				return err
			}
			s.files[path] = f
			for _, b := range f.Hosts() {
				s.Hosts = append(s.Hosts, &Host{Name: b.Name(), Source: src, Path: path, Block: b})
			}
		}
	}

	first := map[string]*Host{}
	for _, h := range s.Hosts {
		if prev, ok := first[h.Name]; ok {
			h.ShadowedBy = prev.Source
			h.Duplicated = true
			prev.Duplicated = true
			continue
		}
		first[h.Name] = h
	}
	return nil
}

func (s *Store) sourceFiles(src *Source) ([]string, error) {
	switch src.Kind {
	case KindLocal:
		return []string{s.Paths.LocalConf()}, nil
	case KindMain:
		return []string{s.Paths.SSHConfig}, nil
	}
	paths, err := filepath.Glob(filepath.Join(src.Dir, "*.conf"))
	sort.Strings(paths)
	return paths, err
}

// load lit un fichier ; ~/.ssh/config est lu sans son bloc géré.
func (s *Store) load(path string) (*sshconfig.File, error) {
	if path == s.Paths.SSHConfig {
		rest, _ := stripManaged(s.mainRaw)
		return sshconfig.Parse(rest), nil
	}
	b, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return sshconfig.Parse(string(b)), nil
}

// fresh renvoie une copie modifiable d'un fichier.
func (s *Store) fresh(path string) (*sshconfig.File, error) {
	if f, ok := s.files[path]; ok {
		return sshconfig.Parse(f.String()), nil
	}
	return s.load(path)
}

// SourceOf renvoie la source qui possède un fichier.
func (s *Store) SourceOf(path string) *Source {
	for _, src := range s.Sources {
		switch src.Kind {
		case KindLocal:
			if path == s.Paths.LocalConf() {
				return src
			}
		case KindMain:
			if path == s.Paths.SSHConfig {
				return src
			}
		case KindRepo:
			if filepath.Dir(path) == src.Dir {
				return src
			}
		}
	}
	return nil
}

// Source renvoie une source par nom ("local", "ssh-config" ou nom de dépôt).
func (s *Store) Source(name string) *Source {
	for _, src := range s.Sources {
		if src.Name == name {
			return src
		}
	}
	return nil
}

// Repos renvoie les sources de type dépôt.
func (s *Store) Repos() []*Source {
	var out []*Source
	for _, src := range s.Sources {
		if src.Kind == KindRepo {
			out = append(out, src)
		}
	}
	return out
}

// HostsOf renvoie les Host d'une source.
func (s *Store) HostsOf(src *Source) []*Host {
	var out []*Host
	for _, h := range s.Hosts {
		if h.Source == src {
			out = append(out, h)
		}
	}
	return out
}

// ConfFiles liste les fichiers .conf d'un dépôt.
func (s *Store) ConfFiles(src *Source) []string {
	if src.Kind != KindRepo {
		return nil
	}
	paths, _ := s.sourceFiles(src)
	out := make([]string, len(paths))
	for i, p := range paths {
		out[i] = filepath.Base(p)
	}
	return out
}

// Target désigne le fichier où écrire un Host.
type Target struct {
	Source *Source
	File   string // nom du .conf, pour un dépôt
}

// TargetPath résout le chemin d'une cible.
func (s *Store) TargetPath(t Target) (string, error) {
	switch t.Source.Kind {
	case KindLocal:
		return s.Paths.LocalConf(), nil
	case KindMain:
		return s.Paths.SSHConfig, nil
	}
	name := strings.TrimSpace(t.File)
	if name == "" {
		return "", errors.New("choisissez un fichier .conf dans le dépôt")
	}
	if !strings.HasSuffix(name, ".conf") {
		name += ".conf"
	}
	if strings.ContainsAny(name, `/\`) || strings.HasPrefix(name, ".") {
		return "", fmt.Errorf("nom de fichier invalide : %s", name)
	}
	return filepath.Join(t.Source.Dir, name), nil
}

// SaveHost crée (old == nil) ou met à jour un Host, en le déplaçant si la
// cible a changé. Renvoie les dépôts à synchroniser.
func (s *Store) SaveHost(old *Host, t Target, name string, opts []sshconfig.Option) ([]*Source, error) {
	name = strings.Join(strings.Fields(name), " ")
	if name == "" {
		return nil, errors.New("l'alias (Host) est obligatoire")
	}
	dest, err := s.TargetPath(t)
	if err != nil {
		return nil, err
	}
	destF, err := s.fresh(dest)
	if err != nil {
		return nil, err
	}
	changed := map[string]*sshconfig.File{dest: destF}

	var block *sshconfig.Block
	if old != nil && old.Path == dest {
		if block = destF.Find(old.Name); block == nil {
			return nil, fmt.Errorf("%s a disparu de %s (synchronisé entre-temps ?)", old.Name, old.File())
		}
	} else {
		if old != nil {
			oldF, err := s.fresh(old.Path)
			if err != nil {
				return nil, err
			}
			ob := oldF.Find(old.Name)
			if ob == nil {
				return nil, fmt.Errorf("%s a disparu de %s (synchronisé entre-temps ?)", old.Name, old.File())
			}
			block = ob.Clone()
			oldF.Remove(ob)
			changed[old.Path] = oldF
		} else {
			block = sshconfig.NewHostBlock(name, nil)
		}
		destF.Append(block)
	}
	if other := destF.Find(name); other != nil && other != block {
		return nil, fmt.Errorf("%s est déjà défini dans %s", name, filepath.Base(dest))
	}
	block.SetName(name)
	block.SetOptions(opts)
	return s.writeAll(changed)
}

// DeleteHost supprime un Host de son fichier.
func (s *Store) DeleteHost(h *Host) ([]*Source, error) {
	f, err := s.fresh(h.Path)
	if err != nil {
		return nil, err
	}
	b := f.Find(h.Name)
	if b == nil {
		return nil, fmt.Errorf("%s a disparu de %s", h.Name, h.File())
	}
	f.Remove(b)
	return s.writeAll(map[string]*sshconfig.File{h.Path: f})
}

// MoveHost déplace un Host tel quel, commentaires compris.
func (s *Store) MoveHost(h *Host, t Target) ([]*Source, error) {
	return s.SaveHost(h, t, h.Name, h.Block.Options())
}

// writeAll valide puis écrit les fichiers modifiés, ~/.ssh/config en dernier,
// et renvoie les dépôts touchés.
func (s *Store) writeAll(changed map[string]*sshconfig.File) ([]*Source, error) {
	contents := map[string]string{}
	for path, f := range changed {
		c := f.String()
		if path == s.Paths.SSHConfig {
			c = composeMain(managedBlock(s.Paths, s.Config.Repos), c)
		}
		if err := Validate(c); err != nil {
			return nil, fmt.Errorf("%s : %w", filepath.Base(path), err)
		}
		contents[path] = c
	}
	var touched []*Source
	seen := map[*Source]bool{}
	paths := make([]string, 0, len(contents))
	for p := range contents {
		paths = append(paths, p)
	}
	sort.Slice(paths, func(i, j int) bool { return paths[j] == s.Paths.SSHConfig })
	for _, path := range paths {
		src := s.SourceOf(path)
		c := contents[path]
		if src != nil && src.Kind == KindRepo && strings.TrimSpace(c) == "" {
			// Un fichier vidé disparaît du dépôt.
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return touched, err
			}
		} else {
			mode := os.FileMode(0o644)
			if src != nil && src.Kind != KindRepo {
				mode = 0o600
			}
			if err := writeFileAtomic(path, c, mode); err != nil {
				return touched, err
			}
		}
		if src != nil && src.Kind == KindRepo && !seen[src] {
			seen[src] = true
			touched = append(touched, src)
		}
	}
	return touched, s.Reload()
}

// ManagedUpToDate indique si ~/.ssh/config contient déjà le bon bloc d'Include.
func (s *Store) ManagedUpToDate() bool {
	rest, _ := stripManaged(s.mainRaw)
	return composeMain(managedBlock(s.Paths, s.Config.Repos), rest) == s.mainRaw
}

// EnsureManaged (ré)écrit le bloc d'Include en tête de ~/.ssh/config.
func (s *Store) EnsureManaged() error {
	if s.ManagedUpToDate() {
		return nil
	}
	f, err := s.fresh(s.Paths.SSHConfig)
	if err != nil {
		return err
	}
	_, err = s.writeAll(map[string]*sshconfig.File{s.Paths.SSHConfig: f})
	return err
}

// AddRepo clone un dépôt et l'ajoute en dernière priorité.
func (s *Store) AddRepo(r Repo) error {
	r, err := s.CheckRepo(r)
	if err != nil {
		return err
	}
	if err := CloneRepo(s.Paths, r); err != nil {
		return err
	}
	return s.RegisterRepo(r)
}

// CheckRepo normalise et valide un dépôt avant clonage.
func (s *Store) CheckRepo(r Repo) (Repo, error) {
	r.Name = strings.TrimSpace(r.Name)
	r.URL = strings.TrimSpace(r.URL)
	r.Branch = strings.TrimSpace(r.Branch)
	if r.Branch == "" {
		r.Branch = "main"
	}
	if err := ValidateRepoName(r.Name); err != nil {
		return r, err
	}
	if r.URL == "" {
		return r, errors.New("l'URL du dépôt est obligatoire")
	}
	for _, e := range s.Config.Repos {
		if e.Name == r.Name {
			return r, fmt.Errorf("un dépôt s'appelle déjà %s", r.Name)
		}
	}
	if _, err := os.Stat(s.Paths.RepoDir(r.Name)); err == nil {
		return r, fmt.Errorf("%s existe déjà : supprimez-le ou choisissez un autre nom", s.Paths.RepoDir(r.Name))
	}
	return r, nil
}

// CloneRepo clone sans toucher au Store : peut tourner en arrière-plan.
func CloneRepo(p Paths, r Repo) error {
	dir := p.RepoDir(r.Name)
	if _, err := gitsync.Clone(r.URL, dir, r.Branch); err != nil {
		os.RemoveAll(dir)
		return err
	}
	return nil
}

// RegisterRepo ajoute un dépôt déjà cloné à la configuration.
func (s *Store) RegisterRepo(r Repo) error {
	s.Config.Repos = append(s.Config.Repos, r)
	return s.saveConfig()
}

// ErrUnpushed signale un dépôt dont des modifications ne sont pas poussées.
var ErrUnpushed = errors.New("des modifications de ce dépôt ne sont pas poussées")

// RemoveRepo retire un dépôt et son clone. Sans force, refuse de perdre des
// modifications non poussées.
func (s *Store) RemoveRepo(name string, force bool) error {
	src := s.Source(name)
	if src == nil || src.Kind != KindRepo {
		return fmt.Errorf("dépôt inconnu : %s", name)
	}
	if !force {
		if st, err := src.Git().Status(); err == nil && (st.Ahead > 0 || st.Dirty || st.Merging) {
			return ErrUnpushed
		}
	}
	var repos []Repo
	for _, r := range s.Config.Repos {
		if r.Name != name {
			repos = append(repos, r)
		}
	}
	s.Config.Repos = repos
	if err := s.saveConfig(); err != nil {
		return err
	}
	return os.RemoveAll(src.Dir)
}

// MoveRepo change la priorité d'un dépôt (delta -1 : plus prioritaire).
func (s *Store) MoveRepo(name string, delta int) error {
	for i, r := range s.Config.Repos {
		if r.Name != name {
			continue
		}
		j := i + delta
		if j < 0 || j >= len(s.Config.Repos) {
			return nil
		}
		s.Config.Repos[i], s.Config.Repos[j] = s.Config.Repos[j], s.Config.Repos[i]
		return s.saveConfig()
	}
	return fmt.Errorf("dépôt inconnu : %s", name)
}

func (s *Store) saveConfig() error {
	if err := SaveConfig(s.Paths, s.Config); err != nil {
		return err
	}
	if err := s.Reload(); err != nil {
		return err
	}
	return s.EnsureManaged()
}

// Keys liste les clés privées de ~/.ssh, sous la forme ~/.ssh/nom.
func (s *Store) Keys() []string {
	entries, err := os.ReadDir(s.Paths.SSHDir())
	if err != nil {
		return nil
	}
	home, _ := os.UserHomeDir()
	var out []string
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || strings.HasPrefix(n, ".") || strings.HasSuffix(n, ".pub") ||
			strings.HasPrefix(n, "config") || strings.HasPrefix(n, "known_hosts") ||
			strings.HasPrefix(n, "authorized_keys") || strings.HasPrefix(n, "environment") {
			continue
		}
		switch filepath.Ext(n) {
		case ".bck", ".bak", ".old", ".tmp", ".swp":
			continue
		}
		p := filepath.Join(s.Paths.SSHDir(), n)
		if home != "" && strings.HasPrefix(p, home+string(filepath.Separator)) {
			p = "~" + strings.TrimPrefix(p, home)
		}
		out = append(out, p)
	}
	return out
}

// Effective renvoie la configuration résolue par ssh pour un alias (ssh -G).
func (s *Store) Effective(alias string) (map[string]string, error) {
	out, err := exec.Command("ssh", "-F", s.Paths.SSHConfig, "-G", alias).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("%s", strings.TrimSpace(string(out)))
	}
	res := map[string]string{}
	for _, l := range strings.Split(string(out), "\n") {
		k, v, ok := strings.Cut(l, " ")
		if !ok {
			continue
		}
		if prev, dup := res[k]; dup {
			res[k] = prev + ", " + v
		} else {
			res[k] = v
		}
	}
	return res, nil
}
