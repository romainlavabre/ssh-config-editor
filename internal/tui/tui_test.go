package tui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/romainlavabre/ssh-config-editor/internal/store"
)

const aliceConfig = `Host *
  StrictHostKeyChecking no

Host fairfair-live-worker-1
  IdentityFile ~/.ssh/fairfair-live
  IdentitiesOnly yes
  User ubuntu
  HostName 10.100.0.1

Host fairfair-live-worker-2
  IdentityFile ~/.ssh/fairfair-live
  User ubuntu
  HostName 10.100.0.2

Host fairfair-live-worker-3
  IdentityFile ~/.ssh/fairfair-live
  User ubuntu
#  HostName 10.200.0.3
  HostName 10.100.0.3

Host my-pilot-dev
  IdentityFile ~/.ssh/my-pilot
  User ubuntu
  HostName 10.50.0.1

Host minecraft-server
  User ubuntu
  HostName 10.60.0.1
`

// driver drives the model the way bubbletea would: key → Update →
// run the commands → feed the resulting messages back to the model.
type driver struct {
	t *testing.T
	m *Model
}

func newDriver(t *testing.T, home, mainConfig string) *driver {
	t.Helper()
	p := store.Paths{
		SSHConfig: filepath.Join(home, ".ssh", "config"),
		ConfigDir: filepath.Join(home, ".config", "ssh-config-editor"),
		DataDir:   filepath.Join(home, ".local", "share", "ssh-config-editor"),
	}
	if err := os.MkdirAll(filepath.Dir(p.SSHConfig), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.SSHConfig, []byte(mainConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	d := &driver{t: t, m: newModel(st)}
	d.send(tea.WindowSizeMsg{Width: 130, Height: 36})
	d.run(d.m.Init())
	return d
}

func (d *driver) send(msg tea.Msg) {
	_, cmd := d.m.Update(msg)
	d.run(cmd)
}

// run executes a command and feeds the application messages back to the model.
// Cursor blinks and notification expiries are ignored.
func (d *driver) run(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	ch := make(chan tea.Msg, 1)
	go func() { ch <- cmd() }()
	var msg tea.Msg
	select {
	case msg = <-ch:
	case <-time.After(30 * time.Second):
		d.t.Fatal("commande bloquée")
	}
	switch msg := msg.(type) {
	case tea.BatchMsg:
		for _, c := range msg {
			d.run(c)
		}
	case syncDoneMsg, effMsg, cloneDoneMsg:
		d.send(msg)
	}
}

func key(s string) tea.KeyMsg {
	named := map[string]tea.KeyType{
		"enter": tea.KeyEnter, "esc": tea.KeyEsc, "tab": tea.KeyTab, "shift+tab": tea.KeyShiftTab,
		"up": tea.KeyUp, "down": tea.KeyDown, "left": tea.KeyLeft, "right": tea.KeyRight,
		"ctrl+s": tea.KeyCtrlS, "ctrl+u": tea.KeyCtrlU, "ctrl+e": tea.KeyCtrlE, " ": tea.KeySpace,
	}
	if k, ok := named[s]; ok {
		return tea.KeyMsg{Type: k}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func (d *driver) keys(keys ...string) {
	for _, k := range keys {
		d.send(key(k))
	}
}

func (d *driver) addRepo(url string) {
	d.keys("R", "a", url, "enter", "enter", "enter")
	if d.m.reposV.err != "" {
		d.t.Fatalf("ajout du dépôt : %s", d.m.reposV.err)
	}
	d.keys("esc")
}

func (d *driver) host(name string) *store.Host {
	for _, h := range d.m.st.Hosts {
		if h.Name == name && h.Source.Kind == store.KindRepo {
			return h
		}
	}
	d.t.Fatalf("%s absent des dépôts", name)
	return nil
}

func (d *driver) setHostName(name, ip string) {
	h := d.host(name)
	d.m.selectHost(h.Path, h.Name)
	d.keys("e", "tab", "ctrl+e", "ctrl+u", ip, "ctrl+s")
	if d.m.form != nil {
		d.t.Fatalf("formulaire resté ouvert : %s", d.m.form.err)
	}
}

func (d *driver) chooseImport(group, dest string) {
	v := d.m.imp
	for i, g := range v.groups {
		if g.Key != group {
			continue
		}
		for v.cursor > i {
			d.keys("up")
		}
		for v.cursor < i {
			d.keys("down")
		}
		for n := 0; n < len(v.dests) && v.destLabel(v.choice[i]) != dest; n++ {
			d.keys("right")
		}
		return
	}
	d.t.Fatalf("groupe %s absent de l'import", group)
}

func (d *driver) file(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		d.t.Fatal(err)
	}
	return string(b)
}

func TestTwoColleaguesEndToEnd(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git absent")
	}
	toastDelay = time.Millisecond
	root := t.TempDir()
	remote := filepath.Join(root, "team.git")
	if out, err := exec.Command("git", "init", "--quiet", "--bare", remote).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}

	// Alice adds the repository and imports her existing config into it.
	alice := newDriver(t, filepath.Join(root, "alice"), aliceConfig)
	t.Logf("accueil d'Alice :\n%s", alice.m.View())
	alice.addRepo(remote)
	if alice.m.st.Source("team") == nil {
		t.Fatal("dépôt team non ajouté")
	}
	alice.keys("I")
	t.Logf("import :\n%s", alice.m.View())
	alice.chooseImport("fairfair-live", "team")
	alice.chooseImport("my-pilot", "local")
	alice.keys("enter", "y")
	if alice.m.screen != scrMain {
		t.Fatalf("l'import doit revenir à l'écran principal (toast : %s)", alice.m.toast)
	}
	mainCfg := alice.file(alice.m.st.Paths.SSHConfig)
	if !strings.HasPrefix(mainCfg, "# >>> ssh-config-editor") || strings.Contains(mainCfg, "fairfair-live-worker") ||
		!strings.Contains(mainCfg, "Host minecraft-server") || !strings.Contains(mainCfg, "Host *") {
		t.Fatalf("~/.ssh/config d'Alice après import :\n%s", mainCfg)
	}
	h := alice.host("fairfair-live-worker-3")
	alice.m.selectHost(h.Path, h.Name)
	alice.run(alice.m.loadEffective())
	view := alice.m.View()
	t.Logf("écran principal d'Alice :\n%s", view)
	if !strings.Contains(view, "10.100.0.3") || !strings.Contains(view, "#  HostName 10.200.0.3") {
		t.Error("le détail doit montrer la définition et la config effective")
	}

	// Bob adds the same repository: he receives Alice's Hosts.
	bob := newDriver(t, filepath.Join(root, "bob"), "")
	bob.addRepo(remote)
	if len(bob.m.st.HostsOf(bob.m.st.Source("team"))) != 3 {
		t.Fatalf("Bob doit recevoir 3 Host, il en a %d", len(bob.m.st.HostsOf(bob.m.st.Source("team"))))
	}

	// Neighbouring Hosts: automatic merge.
	alice.setHostName("fairfair-live-worker-1", "10.9.9.1")
	bob.setHostName("fairfair-live-worker-2", "10.8.8.2")
	if bob.m.screen != scrMain || len(bob.m.repo("team").conflicts) != 0 {
		t.Fatal("deux Host voisins ne doivent pas produire de conflit")
	}
	alice.keys("r")
	conf := filepath.Join(alice.m.st.Paths.RepoDir("team"), "fairfair-live.conf")
	if got := alice.file(conf); !strings.Contains(got, "10.9.9.1") || !strings.Contains(got, "10.8.8.2") {
		t.Fatalf("Alice doit avoir les deux modifications :\n%s", got)
	}

	// Same Host on both sides: conflict screen for Bob.
	alice.setHostName("fairfair-live-worker-3", "10.7.7.1")
	bob.setHostName("fairfair-live-worker-3", "10.7.7.2")
	if bob.m.screen != scrConflict {
		t.Fatalf("Bob doit arriver sur l'écran de conflit (écran %d, toast %q)", bob.m.screen, bob.m.toast)
	}
	view = bob.m.View()
	t.Logf("écran de conflit de Bob :\n%s", view)
	if !strings.Contains(view, "fairfair-live-worker-3") || !strings.Contains(view, "10.7.7.1") || !strings.Contains(view, "10.7.7.2") {
		t.Error("l'écran de conflit doit montrer les deux versions")
	}
	bob.keys("t", "enter")
	if bob.m.screen != scrMain || len(bob.m.repo("team").conflicts) != 0 || bob.m.repo("team").err != "" {
		t.Fatalf("résolution ratée : %+v", bob.m.repo("team"))
	}
	bobConf := filepath.Join(bob.m.st.Paths.RepoDir("team"), "fairfair-live.conf")
	if got := bob.file(bobConf); !strings.Contains(got, "10.7.7.1") || strings.Contains(got, "10.7.7.2") {
		t.Fatalf("Bob doit garder la version d'Alice :\n%s", got)
	}
	if st := bob.m.repo("team").status; st.Ahead != 0 || st.Merging {
		t.Errorf("Bob doit avoir tout poussé : %+v", st)
	}
	alice.keys("r")
	if got, want := alice.file(conf), bob.file(bobConf); got != want {
		t.Errorf("Alice et Bob divergent :\n--- alice\n%s\n--- bob\n%s", got, want)
	}

	// The form, for review.
	alice.m.selectHost(h.Path, h.Name)
	alice.keys("e")
	t.Logf("formulaire :\n%s", alice.m.View())
	alice.keys("esc")
}
