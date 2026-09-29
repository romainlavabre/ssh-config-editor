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
		t.Skip("git not installed")
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
		t.Fatalf("managed block missing or misplaced:\n%s", got)
	}
	if !strings.HasSuffix(got, "\n\n"+mainConfig) {
		t.Errorf("the original content should follow the block, untouched:\n%s", got)
	}
	// Idempotent: no rewrite when nothing changes.
	if !s.ManagedUpToDate() {
		t.Error("the block should be up to date")
	}
	if len(s.Hosts) != 4 {
		t.Errorf("expected 4 Hosts read, got %d", len(s.Hosts))
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
		t.Fatalf("unexpected groups: %v", keys)
	}

	backup, touched, err := s.Import(map[string]*Source{
		"fairfair-live": s.Source("team"),
		"github.com":    s.Source("local"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(touched) != 1 || touched[0].Name != "team" {
		t.Errorf("repositories to sync: %+v", touched)
	}
	if readFile(t, backup) == "" {
		t.Error("empty backup")
	}

	main := readFile(t, s.Paths.SSHConfig)
	if strings.Contains(main, "fairfair-live-worker") || strings.Contains(main, "github.com") || !strings.Contains(main, "Host *") {
		t.Errorf("~/.ssh/config after import:\n%s", main)
	}
	team := readFile(t, filepath.Join(s.Paths.RepoDir("team"), "fairfair-live.conf"))
	if !strings.Contains(team, "#  HostName 57.130.39.121") || strings.Count(team, "Host ") != 2 {
		t.Errorf("fairfair-live.conf:\n%s", team)
	}
	if !strings.Contains(readFile(t, s.Paths.LocalConf()), "Host github.com") {
		t.Error("github.com should be in local.conf")
	}

	if _, err := exec.LookPath("ssh"); err == nil {
		eff, err := s.Effective("fairfair-live-worker-2")
		if err != nil {
			t.Fatal(err)
		}
		if eff["hostname"] != "10.100.0.158" || eff["stricthostkeychecking"] != "false" {
			t.Errorf("ssh -G does not see the imported config: %v / %v", eff["hostname"], eff["stricthostkeychecking"])
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
		t.Errorf("the repository should need syncing")
	}
	if got := readFile(t, filepath.Join(team.Dir, "infra.conf")); got != "Host bastion\n  HostName 1.2.3.4\n  User admin\n" {
		t.Errorf("infra.conf: %q", got)
	}

	if _, err := s.SaveHost(nil, Target{Source: team, File: "infra.conf"}, "bastion", nil); err == nil {
		t.Error("a duplicate in the same file should be rejected")
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
				t.Errorf("wrong shadowing: %s shadowed by %s", h.Source.Name, h.ShadowedBy.Name)
			}
		}
	}
	if shadowed != 1 {
		t.Errorf("expected 1 shadowed Host, got %d", shadowed)
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
		t.Error("the empty infra.conf should be deleted")
	}
	if !strings.Contains(readFile(t, s.Paths.SSHConfig), "Host bastion\n  HostName 1.2.3.4") {
		t.Error("bastion should be in ~/.ssh/config")
	}
}

func TestInvalidOptionRejected(t *testing.T) {
	if _, err := exec.LookPath("ssh"); err != nil {
		t.Skip("ssh not installed")
	}
	s := sandbox(t)
	_, err := s.SaveHost(nil, Target{Source: s.Source("team"), File: "x"}, "x", []sshconfig.Option{{Key: "HostNme", Value: "1.2.3.4"}})
	if err == nil {
		t.Fatal("an unknown option should be rejected")
	}
	if _, statErr := os.Stat(filepath.Join(s.Paths.RepoDir("team"), "x.conf")); !os.IsNotExist(statErr) {
		t.Error("nothing should be written when ssh rejects")
	}
}

func TestOldManagedBlockIsReplaced(t *testing.T) {
	s := sandbox(t)
	old := "# >>> ssh-config-editor (généré, ne pas éditer) >>>\nInclude /old/local.conf\n# <<< ssh-config-editor <<<\n\n" + mainConfig
	if err := os.WriteFile(s.Paths.SSHConfig, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.Reload(); err != nil {
		t.Fatal(err)
	}
	if err := s.EnsureManaged(); err != nil {
		t.Fatal(err)
	}
	got := readFile(t, s.Paths.SSHConfig)
	if strings.Count(got, "# >>> ssh-config-editor") != 1 || !strings.HasPrefix(got, beginMarker) ||
		strings.Contains(got, "/old/local.conf") || !strings.HasSuffix(got, "\n\n"+mainConfig) {
		t.Errorf("the old block should be replaced, not duplicated:\n%s", got)
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
			t.Errorf("GroupKey(%q) = %q, want %q", in, got, want)
		}
	}
}
