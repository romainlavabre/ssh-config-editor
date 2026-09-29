// Package store rassemble les Host de toutes les sources (surcharges locales,
// dépôts, ~/.ssh/config) et applique les modifications sur disque.
package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"github.com/BurntSushi/toml"
)

// Paths regroupe les emplacements utilisés par l'outil.
type Paths struct {
	SSHConfig string // ~/.ssh/config
	ConfigDir string // ~/.config/ssh-config-editor
	DataDir   string // ~/.local/share/ssh-config-editor
}

// DefaultPaths respecte $HOME, $XDG_CONFIG_HOME et $XDG_DATA_HOME.
func DefaultPaths() (Paths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Paths{}, err
	}
	cfg := os.Getenv("XDG_CONFIG_HOME")
	if cfg == "" {
		cfg = filepath.Join(home, ".config")
	}
	data := os.Getenv("XDG_DATA_HOME")
	if data == "" {
		data = filepath.Join(home, ".local", "share")
	}
	return Paths{
		SSHConfig: filepath.Join(home, ".ssh", "config"),
		ConfigDir: filepath.Join(cfg, "ssh-config-editor"),
		DataDir:   filepath.Join(data, "ssh-config-editor"),
	}, nil
}

// ConfigFile est la configuration de l'outil (liste des dépôts).
func (p Paths) ConfigFile() string { return filepath.Join(p.ConfigDir, "config.toml") }

// LocalConf contient les surcharges personnelles, jamais partagées.
func (p Paths) LocalConf() string { return filepath.Join(p.ConfigDir, "local.conf") }

// RepoDir est le clone local d'un dépôt.
func (p Paths) RepoDir(name string) string { return filepath.Join(p.DataDir, "repos", name) }

// SSHDir est le dossier des clés.
func (p Paths) SSHDir() string { return filepath.Dir(p.SSHConfig) }

// Repo est un dépôt de configurations partagé.
type Repo struct {
	Name   string `toml:"name"`
	URL    string `toml:"url"`
	Branch string `toml:"branch"`
}

// Config est le contenu de config.toml. L'ordre des dépôts est leur priorité.
type Config struct {
	Repos []Repo `toml:"repo"`
}

var repoName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// ValidateRepoName refuse les noms qui ne feraient pas un nom de dossier sûr.
func ValidateRepoName(name string) error {
	if !repoName.MatchString(name) {
		return fmt.Errorf("nom de dépôt invalide %q : lettres, chiffres, . _ - uniquement", name)
	}
	if name == "local" {
		return errors.New(`"local" est réservé aux surcharges personnelles`)
	}
	return nil
}

// LoadConfig lit config.toml ; un fichier absent donne une configuration vide.
func LoadConfig(p Paths) (*Config, error) {
	c := &Config{}
	if _, err := toml.DecodeFile(p.ConfigFile(), c); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("lecture de %s : %w", p.ConfigFile(), err)
	}
	for i := range c.Repos {
		if c.Repos[i].Branch == "" {
			c.Repos[i].Branch = "main"
		}
	}
	return c, nil
}

// SaveConfig écrit config.toml.
func SaveConfig(p Paths, c *Config) error {
	if err := os.MkdirAll(p.ConfigDir, 0o755); err != nil {
		return err
	}
	tmp := p.ConfigFile() + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := f.WriteString("# Dépôts de configurations ssh, par ordre de priorité (le premier gagne).\n"); err != nil {
		f.Close()
		return err
	}
	if err := toml.NewEncoder(f).Encode(c); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, p.ConfigFile())
}
