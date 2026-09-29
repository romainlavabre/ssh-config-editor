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
  LogLevel ERROR
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
		d.t.Fatal("command stuck")
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
	if r, ok := strings.CutPrefix(s, "alt+"); ok {
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(r), Alt: true}
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
		d.t.Fatalf("adding the repository: %s", d.m.reposV.err)
	}
	d.keys("esc")
}

func (d *driver) host(name string) *store.Host {
	for _, h := range d.m.st.Hosts {
		if h.Name == name && h.Source.Kind == store.KindRepo {
			return h
		}
	}
	d.t.Fatalf("%s missing from the repositories", name)
	return nil
}

func (d *driver) setHostName(name, ip string) {
	h := d.host(name)
	d.m.selectHost(h.Path, h.Name)
	d.keys("e", "tab", "ctrl+e", "ctrl+u", ip, "ctrl+s")
	if d.m.form != nil {
		d.t.Fatalf("form still open: %s", d.m.form.err)
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
	d.t.Fatalf("group %s missing from the import", group)
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
	t.Logf("Alice's welcome screen:\n%s", alice.m.View())
	alice.addRepo(remote)
	if alice.m.st.Source("team") == nil {
		t.Fatal("repository team not added")
	}
	alice.keys("I")
	t.Logf("import:\n%s", alice.m.View())
	alice.chooseImport("fairfair-live", "team")
	alice.chooseImport("my-pilot", "local")
	alice.keys("enter", "y")
	if alice.m.screen != scrMain {
		t.Fatalf("the import should go back to the main screen (toast: %s)", alice.m.toast)
	}
	mainCfg := alice.file(alice.m.st.Paths.SSHConfig)
	if !strings.HasPrefix(mainCfg, "# >>> ssh-config-editor") || strings.Contains(mainCfg, "fairfair-live-worker") ||
		!strings.Contains(mainCfg, "Host minecraft-server") || !strings.Contains(mainCfg, "Host *") {
		t.Fatalf("Alice's ~/.ssh/config after import:\n%s", mainCfg)
	}
	h := alice.host("fairfair-live-worker-3")
	alice.m.selectHost(h.Path, h.Name)
	alice.run(alice.m.loadEffective())
	view := alice.m.View()
	t.Logf("Alice's main screen:\n%s", view)
	if !strings.Contains(view, "x delete") || !strings.Contains(view, "c duplicate") {
		t.Error("the footer should list the Host actions, delete included")
	}
	if !strings.Contains(view, "10.100.0.3") || !strings.Contains(view, "#  HostName 10.200.0.3") {
		t.Error("the detail pane should show the definition and the effective config")
	}

	// Bob adds the same repository: he receives Alice's Hosts.
	bob := newDriver(t, filepath.Join(root, "bob"), "")
	bob.addRepo(remote)
	if len(bob.m.st.HostsOf(bob.m.st.Source("team"))) != 3 {
		t.Fatalf("Bob should receive 3 Hosts, he has %d", len(bob.m.st.HostsOf(bob.m.st.Source("team"))))
	}

	// Neighbouring Hosts: automatic merge.
	alice.setHostName("fairfair-live-worker-1", "10.9.9.1")
	out, err := exec.Command("git", "-C", alice.m.st.Paths.RepoDir("team"), "log", "-1", "--format=%s").Output()
	if err != nil || !strings.HasPrefix(string(out), "ssh-config-editor: update fairfair-live-worker-1 (") {
		t.Errorf("commit message: %q (%v)", out, err)
	}
	bob.setHostName("fairfair-live-worker-2", "10.8.8.2")
	if bob.m.screen != scrMain || len(bob.m.repo("team").conflicts) != 0 {
		t.Fatal("two neighbouring Hosts must not produce a conflict")
	}
	alice.keys("r")
	conf := filepath.Join(alice.m.st.Paths.RepoDir("team"), "fairfair-live.conf")
	if got := alice.file(conf); !strings.Contains(got, "10.9.9.1") || !strings.Contains(got, "10.8.8.2") {
		t.Fatalf("Alice should have both changes:\n%s", got)
	}
	// Editing through the form keeps what has no field (LogLevel) and the
	// inline choices (IdentitiesOnly).
	if got := alice.file(conf); !strings.Contains(got, "  LogLevel ERROR\n") || !strings.Contains(got, "  IdentitiesOnly yes\n") {
		t.Errorf("directives lost by the form:\n%s", got)
	}

	// Same Host on both sides: conflict screen for Bob.
	alice.setHostName("fairfair-live-worker-3", "10.7.7.1")
	bob.setHostName("fairfair-live-worker-3", "10.7.7.2")
	if bob.m.screen != scrConflict {
		t.Fatalf("Bob should land on the conflict screen (screen %d, toast %q)", bob.m.screen, bob.m.toast)
	}
	view = bob.m.View()
	t.Logf("Bob's conflict screen:\n%s", view)
	if !strings.Contains(view, "fairfair-live-worker-3") || !strings.Contains(view, "10.7.7.1") || !strings.Contains(view, "10.7.7.2") {
		t.Error("the conflict screen should show both versions")
	}
	bob.keys("t", "enter")
	if bob.m.screen != scrMain || len(bob.m.repo("team").conflicts) != 0 || bob.m.repo("team").err != "" {
		t.Fatalf("resolution failed: %+v", bob.m.repo("team"))
	}
	bobConf := filepath.Join(bob.m.st.Paths.RepoDir("team"), "fairfair-live.conf")
	if got := bob.file(bobConf); !strings.Contains(got, "10.7.7.1") || strings.Contains(got, "10.7.7.2") {
		t.Fatalf("Bob should keep Alice's version:\n%s", got)
	}
	if st := bob.m.repo("team").status; st.Ahead != 0 || st.Merging {
		t.Errorf("Bob should have pushed everything: %+v", st)
	}
	alice.keys("r")
	if got, want := alice.file(conf), bob.file(bobConf); got != want {
		t.Errorf("Alice and Bob diverge:\n--- alice\n%s\n--- bob\n%s", got, want)
	}

	// The form, for review.
	alice.m.selectHost(h.Path, h.Name)
	alice.keys("e")
	t.Logf("form:\n%s", alice.m.View())
	alice.keys("esc")

	// New Host: Destination and File are lists with every option visible.
	alice.keys("n", "fairfair-live-worker-9")
	f := alice.m.form
	focus := func(id string) {
		for n := 0; f.cur().id != id && n < len(f.fields); n++ {
			alice.keys("tab")
		}
		if f.cur().id != id {
			t.Fatalf("cannot reach field %s", id)
		}
	}
	dest, file, identity := &f.field(idDest).list, &f.field(idFile).list, &f.field("IdentityFile").list
	focus(idDest)
	team := 0
	for i, s := range f.dests {
		if s.Name == "team" {
			team = i
		}
	}
	for dest.idx > team {
		alice.keys("up")
	}
	for dest.idx < team {
		alice.keys("down")
	}
	if file.value() != "fairfair-live.conf" || file.customSelected() {
		t.Errorf("the File list should preselect fairfair-live.conf, got %q", file.value())
	}
	view = alice.m.View()
	t.Logf("new Host form (Storage):\n%s", view)
	for _, want := range []string{"● team", "○ local", "○ ~/.ssh/config", "● fairfair-live.conf", "○ new file…"} {
		if !strings.Contains(view, want) {
			t.Errorf("every list option should be visible, %q is missing", want)
		}
	}

	// h explains the field under focus; esc closes the help, not the form.
	alice.keys("h")
	if view := alice.m.View(); !strings.Contains(view, "Where the Host is saved") {
		t.Errorf("h should open the Destination help:\n%s", view)
	}
	alice.keys("esc")
	if alice.m.form == nil || f.helpOpen {
		t.Fatal("esc should close the help and keep the form open")
	}

	// An unknown prefix proposes a new file named after it.
	focus(idAlias)
	alice.keys("ctrl+e", "ctrl+u", "db-prod-1")
	if file.value() != "db-prod.conf" || !file.customSelected() {
		t.Errorf("expected new file db-prod.conf, got %q (new: %v)", file.value(), file.customSelected())
	}
	alice.keys("ctrl+e", "ctrl+u", "fairfair-live-worker-9")

	// In the File list: down reaches "new file", typing names it, up goes back,
	// and leaving the top of the list moves to Destination.
	focus(idFile)
	alice.keys("down", "extra")
	if file.value() != "extra" || !file.customSelected() {
		t.Errorf("expected the typed new file, got %q", file.value())
	}
	alice.keys("up")
	if file.value() != "fairfair-live.conf" {
		t.Errorf("up should select fairfair-live.conf again, got %q", file.value())
	}
	alice.keys("up")
	if f.cur().id != idDest {
		t.Errorf("leaving the top of the File list should focus Destination, focus is %s", f.cur().id)
	}

	// Text field: h is typed, alt+h explains.
	focus("HostName")
	alice.keys("h")
	if f.helpOpen || f.field("HostName").value() != "h" {
		t.Errorf("h should be typed in a text field, got %q", f.field("HostName").value())
	}
	alice.keys("alt+h")
	if view := alice.m.View(); !strings.Contains(view, "The real address to connect to") {
		t.Errorf("alt+h should open the HostName help:\n%s", view)
	}
	alice.keys("x", "ctrl+e", "ctrl+u", "10.0.0.9")

	// Advanced options are collapsed on a new Host; enter opens them.
	if view := alice.m.View(); strings.Contains(view, "ForwardAgent") || !strings.Contains(view, "▸ Advanced options") {
		t.Errorf("advanced options should start collapsed:\n%s", view)
	}
	focus(idAdvanced)
	alice.keys("enter")
	if !f.advanced {
		t.Fatal("enter on Advanced options should expand them")
	}

	// Inline choice: ←/→ picks a value, every value stays visible.
	focus("ForwardAgent")
	alice.keys("right")
	if f.field("ForwardAgent").value() != "yes" {
		t.Errorf("right should pick yes, got %q", f.field("ForwardAgent").value())
	}
	if view := alice.m.View(); !strings.Contains(view, "○ unset   ● yes   ○ no") {
		t.Errorf("the ForwardAgent choices should all be visible:\n%s", view)
	}

	// IdentityFile list: "none" by default, typing switches to a custom path.
	focus("IdentityFile")
	if identity.value() != "" || identity.idx != 0 {
		t.Errorf("a new Host should have no IdentityFile, got %q", identity.value())
	}
	alice.keys("~/.ssh/team-key")
	if !identity.customSelected() || identity.value() != "~/.ssh/team-key" {
		t.Errorf("typing should fill the custom path, got %q", identity.value())
	}
	view = alice.m.View()
	if !strings.Contains(view, "○ none") || !strings.Contains(view, "custom path: ~/.ssh/team-key") {
		t.Errorf("the IdentityFile list should show every option:\n%s", view)
	}
	alice.keys("ctrl+s")
	saved := alice.host("fairfair-live-worker-9")
	want := "Host fairfair-live-worker-9\n  HostName 10.0.0.9\n  IdentityFile ~/.ssh/team-key\n  ForwardAgent yes"
	if saved.File() != "fairfair-live.conf" || saved.Block.Text() != want {
		t.Errorf("saved in %s:\n%s\nwant:\n%s", saved.File(), saved.Block.Text(), want)
	}

	// Editing: a key that is not in ~/.ssh shows up as a custom path; none
	// removes the directive.
	alice.m.selectHost(saved.Path, saved.Name)
	alice.keys("e")
	f = alice.m.form
	if f.advanced || !strings.Contains(alice.m.View(), "1 set") {
		t.Errorf("editing should also start collapsed, showing how many advanced options are set:\n%s", alice.m.View())
	}
	identity = &f.field("IdentityFile").list
	if !identity.customSelected() || identity.value() != "~/.ssh/team-key" {
		t.Errorf("expected custom path ~/.ssh/team-key, got %q", identity.value())
	}
	focus("IdentityFile")
	for identity.idx > 0 {
		alice.keys("up")
	}
	alice.keys("ctrl+s")
	if got := alice.host("fairfair-live-worker-9").Block.Get("IdentityFile"); got != "" {
		t.Errorf("none should remove IdentityFile, got %q", got)
	}

	// Directives without a field are listed and kept.
	h2 := alice.host("fairfair-live-worker-2")
	alice.m.selectHost(h2.Path, h2.Name)
	alice.keys("e")
	if len(alice.m.form.kept) != 1 || alice.m.form.kept[0].Key != "LogLevel" {
		t.Errorf("LogLevel should be kept as is, got %+v", alice.m.form.kept)
	}
	alice.keys("esc")
}
