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

// Kind tells the three kinds of sources apart.
type Kind int

const (
	KindLocal Kind = iota // ~/.config/ssh-config-editor/local.conf
	KindRepo              // a shared git repository
	KindMain              // what is left in ~/.ssh/config
)

// Source is a place where Hosts live.
type Source struct {
	Kind Kind
	Name string
	Repo *Repo
	Dir  string
}

// Label is the displayed name.
func (s *Source) Label() string {
	switch s.Kind {
	case KindLocal:
		return "local"
	case KindMain:
		return "~/.ssh/config"
	}
	return s.Name
}

// Git returns a repository's clone.
func (s *Source) Git() gitsync.Repo {
	return gitsync.Repo{Dir: s.Dir, URL: s.Repo.URL, Branch: s.Repo.Branch}
}

// Host is a Host block and the place where it is defined.
type Host struct {
	Name       string
	Source     *Source
	Path       string
	Block      *sshconfig.Block
	ShadowedBy *Source // non-nil: a Host with the same name is defined earlier and wins
	Duplicated bool    // also defined elsewhere
}

// File returns the name of the file defining the Host.
func (h *Host) File() string { return filepath.Base(h.Path) }

// Store is the consolidated view of every source.
type Store struct {
	Paths   Paths
	Config  *Config
	Sources []*Source
	Hosts   []*Host
	files   map[string]*sshconfig.File
	mainRaw string
}

// Open loads the tool's configuration and every source.
func Open(p Paths) (*Store, error) {
	c, err := LoadConfig(p)
	if err != nil {
		return nil, err
	}
	s := &Store{Paths: p, Config: c}
	return s, s.Reload()
}

// Reload re-reads every file.
func (s *Store) Reload() error {
	s.files = map[string]*sshconfig.File{}
	s.Hosts = nil
	// Sources stay the same objects across reloads: callers can keep a
	// pointer.
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

// load reads a file; ~/.ssh/config is read without its managed block.
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

// fresh returns a modifiable copy of a file.
func (s *Store) fresh(path string) (*sshconfig.File, error) {
	if f, ok := s.files[path]; ok {
		return sshconfig.Parse(f.String()), nil
	}
	return s.load(path)
}

// SourceOf returns the source owning a file.
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

// Source returns a source by name ("local", "ssh-config" or a repository name).
func (s *Store) Source(name string) *Source {
	for _, src := range s.Sources {
		if src.Name == name {
			return src
		}
	}
	return nil
}

// Repos returns the repository sources.
func (s *Store) Repos() []*Source {
	var out []*Source
	for _, src := range s.Sources {
		if src.Kind == KindRepo {
			out = append(out, src)
		}
	}
	return out
}

// HostsOf returns the Hosts of a source.
func (s *Store) HostsOf(src *Source) []*Host {
	var out []*Host
	for _, h := range s.Hosts {
		if h.Source == src {
			out = append(out, h)
		}
	}
	return out
}

// ConfFiles lists a repository's .conf files.
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

// Target designates the file a Host is written to.
type Target struct {
	Source *Source
	File   string // .conf file name, for a repository
}

// TargetPath resolves a target's path.
func (s *Store) TargetPath(t Target) (string, error) {
	switch t.Source.Kind {
	case KindLocal:
		return s.Paths.LocalConf(), nil
	case KindMain:
		return s.Paths.SSHConfig, nil
	}
	name := strings.TrimSpace(t.File)
	if name == "" {
		return "", errors.New("choose a .conf file in the repository")
	}
	if !strings.HasSuffix(name, ".conf") {
		name += ".conf"
	}
	if strings.ContainsAny(name, `/\`) || strings.HasPrefix(name, ".") {
		return "", fmt.Errorf("invalid file name: %s", name)
	}
	return filepath.Join(t.Source.Dir, name), nil
}

// SaveHost creates (old == nil) or updates a Host, moving it if the target
// changed. Returns the repositories to sync.
func (s *Store) SaveHost(old *Host, t Target, name string, opts []sshconfig.Option) ([]*Source, error) {
	name = strings.Join(strings.Fields(name), " ")
	if name == "" {
		return nil, errors.New("the alias (Host) is required")
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
			return nil, fmt.Errorf("%s is no longer in %s (synced in the meantime?)", old.Name, old.File())
		}
	} else {
		if old != nil {
			oldF, err := s.fresh(old.Path)
			if err != nil {
				return nil, err
			}
			ob := oldF.Find(old.Name)
			if ob == nil {
				return nil, fmt.Errorf("%s is no longer in %s (synced in the meantime?)", old.Name, old.File())
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
		return nil, fmt.Errorf("%s is already defined in %s", name, filepath.Base(dest))
	}
	block.SetName(name)
	block.SetOptions(opts)
	return s.writeAll(changed)
}

// DeleteHost removes a Host from its file.
func (s *Store) DeleteHost(h *Host) ([]*Source, error) {
	f, err := s.fresh(h.Path)
	if err != nil {
		return nil, err
	}
	b := f.Find(h.Name)
	if b == nil {
		return nil, fmt.Errorf("%s is no longer in %s", h.Name, h.File())
	}
	f.Remove(b)
	return s.writeAll(map[string]*sshconfig.File{h.Path: f})
}

// MoveHost moves a Host as is, comments included.
func (s *Store) MoveHost(h *Host, t Target) ([]*Source, error) {
	return s.SaveHost(h, t, h.Name, h.Block.Options())
}

// writeAll validates then writes the modified files, ~/.ssh/config last,
// and returns the repositories touched.
func (s *Store) writeAll(changed map[string]*sshconfig.File) ([]*Source, error) {
	contents := map[string]string{}
	for path, f := range changed {
		c := f.String()
		if path == s.Paths.SSHConfig {
			c = composeMain(managedBlock(s.Paths, s.Config.Repos), c)
		}
		if err := Validate(c); err != nil {
			return nil, fmt.Errorf("%s: %w", filepath.Base(path), err)
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
			// An emptied file is removed from the repository.
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

// ManagedUpToDate reports whether ~/.ssh/config already holds the right Include block.
func (s *Store) ManagedUpToDate() bool {
	rest, _ := stripManaged(s.mainRaw)
	return composeMain(managedBlock(s.Paths, s.Config.Repos), rest) == s.mainRaw
}

// EnsureManaged (re)writes the Include block at the top of ~/.ssh/config.
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

// AddRepo clones a repository and adds it with the lowest priority.
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

// CheckRepo normalizes and validates a repository before cloning.
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
		return r, errors.New("the repository URL is required")
	}
	for _, e := range s.Config.Repos {
		if e.Name == r.Name {
			return r, fmt.Errorf("a repository named %s already exists", r.Name)
		}
	}
	if _, err := os.Stat(s.Paths.RepoDir(r.Name)); err == nil {
		return r, fmt.Errorf("%s already exists: remove it or choose another name", s.Paths.RepoDir(r.Name))
	}
	return r, nil
}

// CloneRepo clones without touching the Store: safe to run in the background.
func CloneRepo(p Paths, r Repo) error {
	dir := p.RepoDir(r.Name)
	if _, err := gitsync.Clone(r.URL, dir, r.Branch); err != nil {
		os.RemoveAll(dir)
		return err
	}
	return nil
}

// RegisterRepo adds an already cloned repository to the configuration.
func (s *Store) RegisterRepo(r Repo) error {
	s.Config.Repos = append(s.Config.Repos, r)
	return s.saveConfig()
}

// ErrUnpushed reports a repository with changes that are not pushed.
var ErrUnpushed = errors.New("this repository has changes that are not pushed")

// RemoveRepo removes a repository and its clone. Without force, it refuses to
// lose unpushed changes.
func (s *Store) RemoveRepo(name string, force bool) error {
	src := s.Source(name)
	if src == nil || src.Kind != KindRepo {
		return fmt.Errorf("unknown repository: %s", name)
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

// MoveRepo changes a repository's priority (delta -1: higher priority).
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
	return fmt.Errorf("unknown repository: %s", name)
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

// Keys lists the private keys in ~/.ssh, as ~/.ssh/name.
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

// Effective returns the configuration resolved by ssh for an alias (ssh -G).
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
