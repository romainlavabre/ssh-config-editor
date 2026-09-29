package gitsync

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// team crée un dépôt distant vide et deux clones qui jouent deux collègues.
func team(t *testing.T) (Repo, Repo) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git absent")
	}
	root := t.TempDir()
	remote := filepath.Join(root, "remote.git")
	if _, err := run(root, "init", "--quiet", "--bare", remote); err != nil {
		t.Fatal(err)
	}
	a, err := Clone(remote, filepath.Join(root, "alice"), "main")
	if err != nil {
		t.Fatal(err)
	}
	b, err := Clone(remote, filepath.Join(root, "bob"), "main")
	if err != nil {
		t.Fatal(err)
	}
	return a, b
}

func write(t *testing.T, r Repo, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(r.Dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, r Repo, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(r.Dir, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func mustSync(t *testing.T, r Repo, msg string) Result {
	t.Helper()
	res, err := r.Sync(msg)
	if err != nil {
		t.Fatal(err)
	}
	if res.Offline {
		t.Fatalf("hors ligne : %s", res.Warning)
	}
	return res
}

const hosts = "Host a\n  HostName 10.0.0.1\n\nHost b\n  HostName 10.0.0.2\n"

func TestNeighbourHostsMergeAutomatically(t *testing.T) {
	alice, bob := team(t)
	write(t, alice, "team.conf", hosts)
	mustSync(t, alice, "init")
	mustSync(t, bob, "")
	if got := read(t, bob, "team.conf"); got != hosts {
		t.Fatalf("bob n'a pas reçu le fichier : %q", got)
	}

	// Deux Host voisins : conflit texte pour git, aucun conflit par Host.
	write(t, alice, "team.conf", strings.Replace(hosts, "10.0.0.1", "10.0.0.11", 1))
	write(t, bob, "team.conf", strings.Replace(hosts, "10.0.0.2", "10.0.0.22", 1))
	mustSync(t, alice, "alice")
	if res := mustSync(t, bob, "bob"); len(res.Conflicts) != 0 {
		t.Fatalf("conflits inattendus : %+v", res.Conflicts)
	}
	mustSync(t, alice, "")

	for _, r := range []Repo{alice, bob} {
		got := read(t, r, "team.conf")
		if !strings.Contains(got, "10.0.0.11") || !strings.Contains(got, "10.0.0.22") {
			t.Errorf("%s : fusion incomplète :\n%s", filepath.Base(r.Dir), got)
		}
		st, _ := r.Status()
		if st.Ahead != 0 || st.Behind != 0 || st.Dirty || st.Merging {
			t.Errorf("%s : pas à jour : %+v", filepath.Base(r.Dir), st)
		}
	}
}

func TestSameHostConflictBlocksPushUntilResolved(t *testing.T) {
	alice, bob := team(t)
	write(t, alice, "team.conf", hosts)
	mustSync(t, alice, "init")
	mustSync(t, bob, "")

	write(t, alice, "team.conf", strings.Replace(hosts, "10.0.0.2", "10.0.0.20", 1))
	write(t, bob, "team.conf", strings.Replace(hosts, "10.0.0.2", "10.0.0.21", 1))
	mustSync(t, alice, "alice")
	res := mustSync(t, bob, "bob")
	if len(res.Conflicts) != 1 || len(res.Conflicts[0].Merge.Conflicts) != 1 {
		t.Fatalf("attendu un conflit sur b, obtenu %+v", res.Conflicts)
	}

	// Un Sync pendant le conflit ne pousse rien.
	if res := mustSync(t, bob, ""); len(res.Conflicts) != 1 {
		t.Fatal("le conflit doit rester en attente")
	}
	pending, err := bob.PendingConflicts()
	if err != nil || len(pending) != 1 {
		t.Fatalf("PendingConflicts : %v %+v", err, pending)
	}
	c := pending[0].Merge.Conflicts[0]
	c.Resolve(c.Ours)
	if err := bob.Resolve(pending); err != nil {
		t.Fatal(err)
	}
	mustSync(t, bob, "")
	mustSync(t, alice, "")
	if got := read(t, alice, "team.conf"); !strings.Contains(got, "10.0.0.21") {
		t.Errorf("alice doit recevoir la version tranchée par bob :\n%s", got)
	}
}

func TestFirstPushesFromTwoClonesOfEmptyRemote(t *testing.T) {
	alice, bob := team(t)
	write(t, alice, "alice.conf", "Host a\n  User x\n")
	write(t, bob, "bob.conf", "Host b\n  User y\n")
	mustSync(t, alice, "alice")
	mustSync(t, bob, "bob")
	mustSync(t, alice, "")
	for _, f := range []string{"alice.conf", "bob.conf"} {
		read(t, alice, f)
		read(t, bob, f)
	}
}

func TestOfflineKeepsLocalCommit(t *testing.T) {
	alice, _ := team(t)
	write(t, alice, "team.conf", hosts)
	if _, err := alice.git("remote", "set-url", "origin", filepath.Join(t.TempDir(), "absent.git")); err != nil {
		t.Fatal(err)
	}
	res, err := alice.Sync("hors ligne")
	if err != nil || !res.Offline {
		t.Fatalf("attendu Offline, obtenu %+v %v", res, err)
	}
	st, _ := alice.Status()
	if st.Ahead != 1 || st.Dirty {
		t.Errorf("le commit doit rester en local : %+v", st)
	}
}
