package store

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/romainlavabre/ssh-config-editor/internal/sshconfig"
)

const mainConfig = `Host *
  StrictHostKeyChecking no

Host fairfair-live-worker-1
  IdentityFile ~/.ssh/fairfair-live
  User ubuntu
  HostName 10.100.0.206

Host fairfair-live-worker-2
  User ubuntu
#  HostName 57.130.39.121
  HostName 10.100.0.158

Host github.com
  IdentityFile ~/.ssh/github
  User git
`

func sandbox(t *testing.T) *Store {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git absent")
	}
	root := t.TempDir()
	p := Paths{
		SSHConfig: filepath.Join(root, "home", ".ssh", "config"),
		ConfigDir: filepath.Join(root, "config"),
		DataDir:   filepath.Join(root, "data"),
	}
	if err := os.MkdirAll(filepath.Dir(p.SSHConfig), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.SSHConfig, []byte(mainConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	remote := filepath.Join(root, "team.git")
	if out, err := exec.Command("git", "init", "--quiet", "--bare", remote).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	s, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddRepo(Repo{Name: "team", URL: remote}); err != nil {
		t.Fatal(err)
	}
	return s
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestAddRepoWritesManagedBlockOnTop(t *testing.T) {
	s := sandbox(t)
	got := readFile(t, s.Paths.SSHConfig)
	if !strings.HasPrefix(got, beginMarker+"\nInclude "+s.Paths.LocalConf()+"\nInclude "+filepath.Join(s.Paths.RepoDir("team"), "*.conf")) {
		t.Fatalf("bloc géré absent ou mal placé :\n%s", got)
	}
	if !strings.HasSuffix(got, "\n\n"+mainConfig) {
		t.Errorf("le contenu d'origine doit suivre le bloc, intact :\n%s", got)
	}
	// Idempotent: no rewrite when nothing changes.
	if !s.ManagedUpToDate() {
		t.Error("le bloc devrait être à jour")
	}
	if len(s.Hosts) != 4 {
		t.Errorf("attendu 4 Host lus, obtenu %d", len(s.Hosts))
	}
}

func TestImportMovesGroupsAndKeepsBackup(t *testing.T) {
	s := sandbox(t)
	groups := s.ImportCandidates()
	var keys []string
	for _, g := range groups {
		keys = append(keys, g.Key)
	}
	if strings.Join(keys, ",") != "fairfair-live,github.com" {
		t.Fatalf("groupes inattendus : %v", keys)
	}

	backup, touched, err := s.Import(map[string]*Source{
		"fairfair-live": s.Source("team"),
		"github.com":    s.Source("local"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(touched) != 1 || touched[0].Name != "team" {
		t.Errorf("dépôts à synchroniser : %+v", touched)
	}
	if readFile(t, backup) == "" {
		t.Error("sauvegarde vide")
	}

	main := readFile(t, s.Paths.SSHConfig)
	if strings.Contains(main, "fairfair-live-worker") || strings.Contains(main, "github.com") || !strings.Contains(main, "Host *") {
		t.Errorf("~/.ssh/config après import :\n%s", main)
	}
	team := readFile(t, filepath.Join(s.Paths.RepoDir("team"), "fairfair-live.conf"))
	if !strings.Contains(team, "#  HostName 57.130.39.121") || strings.Count(team, "Host ") != 2 {
		t.Errorf("fairfair-live.conf :\n%s", team)
	}
	if !strings.Contains(readFile(t, s.Paths.LocalConf()), "Host github.com") {
		t.Error("github.com doit être dans local.conf")
	}

	if _, err := exec.LookPath("ssh"); err == nil {
		eff, err := s.Effective("fairfair-live-worker-2")
		if err != nil {
			t.Fatal(err)
		}
		if eff["hostname"] != "10.100.0.158" || eff["stricthostkeychecking"] != "false" {
			t.Errorf("ssh -G ne voit pas la config importée : %v / %v", eff["hostname"], eff["stricthostkeychecking"])
		}
	}
}

func TestSaveHostCreateMoveAndDuplicates(t *testing.T) {
	s := sandbox(t)
	team := s.Source("team")
	touched, err := s.SaveHost(nil, Target{Source: team, File: "infra"}, "bastion", []sshconfig.Option{{Key: "HostName", Value: "1.2.3.4"}, {Key: "User", Value: "admin"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(touched) != 1 {
		t.Errorf("le dépôt doit être à synchroniser")
	}
	if got := readFile(t, filepath.Join(team.Dir, "infra.conf")); got != "Host bastion\n  HostName 1.2.3.4\n  User admin\n" {
		t.Errorf("infra.conf : %q", got)
	}

	if _, err := s.SaveHost(nil, Target{Source: team, File: "infra.conf"}, "bastion", nil); err == nil {
		t.Error("un doublon dans le même fichier doit être refusé")
	}

	// Same name in local: allowed, but flagged as shadowing the repository.
	if _, err := s.SaveHost(nil, Target{Source: s.Source("local")}, "bastion", []sshconfig.Option{{Key: "User", Value: "moi"}}); err != nil {
		t.Fatal(err)
	}
	var shadowed int
	for _, h := range s.Hosts {
		if h.Name == "bastion" && h.ShadowedBy != nil {
			shadowed++
			if h.Source.Name != "team" || h.ShadowedBy.Name != "local" {
				t.Errorf("mauvais masquage : %s masqué par %s", h.Source.Name, h.ShadowedBy.Name)
			}
		}
	}
	if shadowed != 1 {
		t.Errorf("attendu 1 Host masqué, obtenu %d", shadowed)
	}

	// Move to ~/.ssh/config: the emptied repository file disappears.
	var h *Host
	for _, x := range s.Hosts {
		if x.Name == "bastion" && x.Source == team {
			h = x
		}
	}
	if _, err := s.MoveHost(h, Target{Source: s.Source("ssh-config")}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(team.Dir, "infra.conf")); !os.IsNotExist(err) {
		t.Error("infra.conf vide doit être supprimé")
	}
	if !strings.Contains(readFile(t, s.Paths.SSHConfig), "Host bastion\n  HostName 1.2.3.4") {
		t.Error("bastion doit être dans ~/.ssh/config")
	}
}

func TestInvalidOptionRejected(t *testing.T) {
	if _, err := exec.LookPath("ssh"); err != nil {
		t.Skip("ssh absent")
	}
	s := sandbox(t)
	_, err := s.SaveHost(nil, Target{Source: s.Source("team"), File: "x"}, "x", []sshconfig.Option{{Key: "HostNme", Value: "1.2.3.4"}})
	if err == nil {
		t.Fatal("une option inconnue doit être refusée")
	}
	if _, statErr := os.Stat(filepath.Join(s.Paths.RepoDir("team"), "x.conf")); !os.IsNotExist(statErr) {
		t.Error("rien ne doit être écrit quand ssh refuse")
	}
}

func TestGroupKey(t *testing.T) {
	cases := map[string]string{
		"fairfair-dev4-node":        "fairfair-dev",
		"fairfair-live-worker-10":   "fairfair-live",
		"my-pilot-live":             "my-pilot",
		"minecraft-server":          "minecraft-server",
		"github.com":                "github.com",
		"fairfair-tooling-database": "fairfair-tooling",
	}
	for in, want := range cases {
		if got := GroupKey(in); got != want {
			t.Errorf("GroupKey(%q) = %q, attendu %q", in, got, want)
		}
	}
}
