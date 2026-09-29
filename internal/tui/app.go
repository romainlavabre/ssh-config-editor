// Package tui is the terminal interface of ssh-config-editor.
package tui

import (
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/sahilm/fuzzy"

	"github.com/romainlavabre/ssh-config-editor/internal/gitsync"
	"github.com/romainlavabre/ssh-config-editor/internal/store"
)

// Start selects the opening screen.
type Start int

const (
	StartMain Start = iota
	StartImport
)

type screen int

const (
	scrMain screen = iota
	scrForm
	scrRepos
	scrImport
	scrConflict
	scrHelp
)

// Run starts the interface.
func Run(st *store.Store, start Start) error {
	m := newModel(st)
	if start == StartImport {
		m.openImport()
	}
	_, err := tea.NewProgram(m, tea.WithAltScreen()).Run()
	return err
}

type repoState struct {
	syncing   bool
	again     bool
	message   string
	status    gitsync.Status
	err       string
	conflicts []gitsync.FileConflict
}

type effective struct {
	values  map[string]string
	err     string
	loading bool
}

type row struct {
	src  *store.Source
	host *store.Host // nil: group header
}

func (r row) key() string {
	if r.host == nil {
		return "s:" + r.src.Name
	}
	return "h:" + r.host.Path + "|" + r.host.Name
}

type toastKind int

const (
	toastInfo toastKind = iota
	toastOK
	toastWarn
	toastErr
)

// Model is the application state.
type Model struct {
	st     *store.Store
	w, h   int
	screen screen

	rows      []row
	cursor    int
	top       int
	filter    textinput.Model
	filtering bool
	collapsed map[string]bool
	eff       map[string]*effective

	repos map[string]*repoState

	toast     string
	toastKind toastKind
	toastID   int

	dialog   *dialog
	form     *hostForm
	reposV   *reposView
	imp      *importView
	conflict *conflictView

	quitting bool
}

// Asynchronous messages.
type (
	syncDoneMsg struct {
		repo   string
		res    gitsync.Result
		err    error
		status gitsync.Status
	}
	effMsg struct {
		name   string
		values map[string]string
		err    error
	}
	execDoneMsg struct {
		host string
		err  error
	}
	cloneDoneMsg struct {
		repo store.Repo
		err  error
	}
	toastExpireMsg struct{ id int }
)

func newModel(st *store.Store) *Model {
	f := textinput.New()
	f.Prompt = "/ "
	f.Placeholder = "filter (name, IP, user)"
	m := &Model{
		st:        st,
		filter:    f,
		collapsed: map[string]bool{},
		eff:       map[string]*effective{},
		repos:     map[string]*repoState{},
	}
	m.rebuildRows()
	return m
}

// Init syncs every repository at startup.
func (m *Model) Init() tea.Cmd {
	return tea.Batch(m.syncAll(), m.loadEffective())
}

func (m *Model) repo(name string) *repoState {
	rs, ok := m.repos[name]
	if !ok {
		rs = &repoState{}
		m.repos[name] = rs
	}
	return rs
}

// ------------------------------------------------------------------- update

// Update dispatches messages to the active screen.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.resize()
		return m, nil
	case syncDoneMsg:
		return m, m.onSyncDone(msg)
	case effMsg:
		e := &effective{values: msg.values}
		if msg.err != nil {
			e.err = msg.err.Error()
		}
		m.eff[msg.name] = e
		return m, nil
	case execDoneMsg:
		var ee *exec.ExitError
		if errors.As(msg.err, &ee) && ee.ExitCode() == 255 {
			return m, m.notify(toastErr, "cannot connect to "+msg.host+" (exit code 255)")
		} else if msg.err != nil && !errors.As(msg.err, &ee) {
			return m, m.notify(toastErr, msg.err.Error())
		}
		return m, nil
	case cloneDoneMsg:
		return m, m.onCloneDone(msg)
	case toastExpireMsg:
		if msg.id == m.toastID {
			m.toast = ""
		}
		return m, nil
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
		if m.dialog != nil {
			return m, m.updateDialog(msg)
		}
		switch m.screen {
		case scrForm:
			return m, m.updateForm(msg)
		case scrRepos:
			return m, m.updateRepos(msg)
		case scrImport:
			return m, m.updateImport(msg)
		case scrConflict:
			return m, m.updateConflict(msg)
		case scrHelp:
			m.screen = scrMain
			return m, nil
		}
		return m, m.updateMain(msg)
	}
	// Cursor blink and other messages for the active inputs.
	return m, m.forwardToInputs(msg)
}

func (m *Model) forwardToInputs(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	switch {
	case m.screen == scrForm && m.form != nil:
		cmd = m.form.forward(msg)
	case m.screen == scrRepos && m.reposV != nil && m.reposV.adding:
		cmd = m.reposV.forward(msg)
	case m.screen == scrConflict && m.conflict != nil && m.conflict.editing:
		m.conflict.editor, cmd = m.conflict.editor.Update(msg)
	case m.filtering:
		m.filter, cmd = m.filter.Update(msg)
	}
	return cmd
}

func (m *Model) resize() {
	if m.form != nil {
		m.form.resize(m.w, m.h)
	}
	if m.conflict != nil {
		m.conflict.resize(m.w, m.h)
	}
	if m.reposV != nil {
		m.reposV.resize(m.w)
	}
}

// toastDelay is how long a notification stays visible (doubled for an error).
var toastDelay = 4 * time.Second

func (m *Model) notify(kind toastKind, text string) tea.Cmd {
	m.toastID++
	m.toast, m.toastKind = text, kind
	id := m.toastID
	d := toastDelay
	if kind == toastErr {
		d *= 2
	}
	return tea.Tick(d, func(time.Time) tea.Msg { return toastExpireMsg{id: id} })
}

// --------------------------------------------------------------------- sync

func (m *Model) requestSync(src *store.Source, message string) tea.Cmd {
	if src == nil || src.Kind != store.KindRepo {
		return nil
	}
	rs := m.repo(src.Name)
	if len(rs.conflicts) > 0 {
		// Committed along with the conflict resolution.
		return nil
	}
	if rs.syncing {
		rs.again = true
		if message != "" {
			rs.message = message
		}
		return nil
	}
	rs.syncing = true
	g, name := src.Git(), src.Name
	return func() tea.Msg {
		res, err := g.Sync(message)
		status, _ := g.Status()
		return syncDoneMsg{repo: name, res: res, err: err, status: status}
	}
}

func (m *Model) syncAll() tea.Cmd {
	var cmds []tea.Cmd
	for _, src := range m.st.Repos() {
		cmds = append(cmds, m.requestSync(src, ""))
	}
	return tea.Batch(cmds...)
}

func (m *Model) syncTouched(touched []*store.Source, message string) tea.Cmd {
	var cmds []tea.Cmd
	for _, src := range touched {
		cmds = append(cmds, m.requestSync(src, message))
	}
	return tea.Batch(cmds...)
}

func (m *Model) anySyncing() bool {
	for _, rs := range m.repos {
		if rs.syncing {
			return true
		}
	}
	return false
}

func (m *Model) onSyncDone(msg syncDoneMsg) tea.Cmd {
	rs := m.repo(msg.repo)
	rs.syncing = false
	rs.status = msg.status
	rs.conflicts = msg.res.Conflicts
	switch {
	case msg.err != nil:
		rs.err = msg.err.Error()
	case msg.res.Offline:
		rs.err = "offline: " + firstLine(msg.res.Warning)
	default:
		rs.err = ""
	}
	m.reload()

	var cmds []tea.Cmd
	if len(rs.conflicts) > 0 {
		rs.again = false
		if m.screen == scrMain && m.dialog == nil {
			m.openConflict(msg.repo)
		} else if m.screen != scrConflict {
			cmds = append(cmds, m.notify(toastWarn, "conflict on "+msg.repo+": press C to resolve it"))
		}
	} else if rs.again {
		rs.again = false
		message := rs.message
		rs.message = ""
		cmds = append(cmds, m.requestSync(m.st.Source(msg.repo), message))
	}
	if m.quitting && !m.anySyncing() {
		return tea.Quit
	}
	return tea.Batch(cmds...)
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "hint:") {
			return l
		}
	}
	return s
}

func (m *Model) reload() {
	if err := m.st.Reload(); err != nil {
		m.notify(toastErr, err.Error())
	}
	m.eff = map[string]*effective{}
	m.rebuildRows()
}

// ---------------------------------------------------------------- host list

func (m *Model) rebuildRows() {
	selected := ""
	if m.cursor >= 0 && m.cursor < len(m.rows) {
		selected = m.rows[m.cursor].key()
	}
	q := strings.TrimSpace(m.filter.Value())
	var keep map[*store.Host]bool
	if q != "" {
		keep = map[*store.Host]bool{}
		targets := make([]string, len(m.st.Hosts))
		for i, h := range m.st.Hosts {
			targets[i] = h.Name + " " + h.Block.Get("HostName") + " " + h.Block.Get("User")
		}
		for _, match := range fuzzy.Find(q, targets) {
			keep[m.st.Hosts[match.Index]] = true
		}
	}
	m.rows = nil
	for _, src := range m.st.Sources {
		hosts := m.st.HostsOf(src)
		if keep != nil {
			var filtered []*store.Host
			for _, h := range hosts {
				if keep[h] {
					filtered = append(filtered, h)
				}
			}
			if len(filtered) == 0 {
				continue
			}
			hosts = filtered
		} else if src.Kind == store.KindMain && len(hosts) == 0 {
			continue
		}
		m.rows = append(m.rows, row{src: src})
		if keep == nil && m.collapsed[src.Name] {
			continue
		}
		for _, h := range hosts {
			m.rows = append(m.rows, row{src: src, host: h})
		}
	}
	m.cursor = 0
	for i, r := range m.rows {
		if r.key() == selected {
			m.cursor = i
			return
		}
	}
	if q != "" {
		for i, r := range m.rows {
			if r.host != nil {
				m.cursor = i
				return
			}
		}
	}
}

func (m *Model) current() row {
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		return row{}
	}
	return m.rows[m.cursor]
}

func (m *Model) selectHost(path, name string) {
	for i, r := range m.rows {
		if r.host != nil && r.host.Path == path && r.host.Name == name {
			m.cursor = i
			return
		}
	}
	// Collapsed group: expand it to show the Host.
	if src := m.st.SourceOf(path); src != nil && m.collapsed[src.Name] {
		delete(m.collapsed, src.Name)
		m.rebuildRows()
		m.selectHost(path, name)
	}
}

func (m *Model) move(delta int) tea.Cmd {
	if len(m.rows) == 0 {
		return nil
	}
	m.cursor = max(0, min(len(m.rows)-1, m.cursor+delta))
	return m.loadEffective()
}

func (m *Model) loadEffective() tea.Cmd {
	h := m.current().host
	if h == nil || !h.Block.IsConcrete() {
		return nil
	}
	if _, ok := m.eff[h.Name]; ok {
		return nil
	}
	m.eff[h.Name] = &effective{loading: true}
	st, name := m.st, h.Name
	return func() tea.Msg {
		values, err := st.Effective(name)
		return effMsg{name: name, values: values, err: err}
	}
}

func (m *Model) listHeight() int {
	h := m.h - 5 // title, borders, status bar, help
	if m.filtering || m.filter.Value() != "" {
		h--
	}
	return max(3, h)
}

func (m *Model) updateMain(k tea.KeyMsg) tea.Cmd {
	if m.filtering {
		switch k.String() {
		case "esc":
			m.filtering = false
			m.filter.SetValue("")
			m.filter.Blur()
			m.rebuildRows()
			return m.loadEffective()
		case "enter":
			m.filtering = false
			m.filter.Blur()
			return nil
		case "up", "ctrl+p":
			return m.move(-1)
		case "down", "ctrl+n":
			return m.move(1)
		}
		var cmd tea.Cmd
		m.filter, cmd = m.filter.Update(k)
		m.rebuildRows()
		return tea.Batch(cmd, m.loadEffective())
	}

	cur := m.current()
	switch k.String() {
	case "q":
		if m.anySyncing() {
			m.quitting = true
			return m.notify(toastInfo, "finishing sync before quitting…")
		}
		return tea.Quit
	case "up", "k":
		return m.move(-1)
	case "down", "j":
		return m.move(1)
	case "pgup", "ctrl+u":
		return m.move(-m.listHeight() / 2)
	case "pgdown", "ctrl+d":
		return m.move(m.listHeight() / 2)
	case "home", "g":
		return m.move(-len(m.rows))
	case "end", "G":
		return m.move(len(m.rows))
	case "/":
		m.filtering = true
		return m.filter.Focus()
	case "esc":
		if m.filter.Value() != "" {
			m.filter.SetValue("")
			m.rebuildRows()
		}
	case "left", "h":
		if cur.src != nil {
			m.collapsed[cur.src.Name] = true
			m.rebuildRows()
			for i, r := range m.rows {
				if r.host == nil && r.src == cur.src {
					m.cursor = i
				}
			}
		}
	case "right", "l":
		if cur.src != nil && m.collapsed[cur.src.Name] {
			delete(m.collapsed, cur.src.Name)
			m.rebuildRows()
		}
	case " ":
		if cur.host == nil && cur.src != nil {
			m.toggle(cur.src)
		}
	case "enter":
		if cur.host != nil {
			return m.connect(cur.host)
		}
		if cur.src != nil {
			m.toggle(cur.src)
		}
	case "n":
		m.openForm(formNew, nil, cur.src)
		return m.form.focusCmd()
	case "e":
		if cur.host != nil {
			m.openForm(formEdit, cur.host, cur.src)
			return m.form.focusCmd()
		}
	case "c":
		if cur.host != nil {
			m.openForm(formDuplicate, cur.host, cur.src)
			return m.form.focusCmd()
		}
	case "m":
		if cur.host != nil {
			m.openMoveDialog(cur.host)
		}
	case "x", "delete":
		if cur.host != nil {
			m.openDeleteDialog(cur.host)
		}
	case "r":
		if len(m.st.Repos()) == 0 {
			return m.notify(toastInfo, "no repository: press R to add one")
		}
		return tea.Batch(m.syncAll(), m.notify(toastInfo, "syncing all repositories…"))
	case "R":
		m.openRepos()
	case "I":
		m.openImport()
	case "C":
		for _, src := range m.st.Repos() {
			if len(m.repo(src.Name).conflicts) > 0 {
				m.openConflict(src.Name)
				return nil
			}
		}
		return m.notify(toastInfo, "no pending conflict")
	case "?":
		m.screen = scrHelp
	}
	return nil
}

func (m *Model) toggle(src *store.Source) {
	if m.collapsed[src.Name] {
		delete(m.collapsed, src.Name)
	} else {
		m.collapsed[src.Name] = true
	}
	m.rebuildRows()
}

func (m *Model) connect(h *store.Host) tea.Cmd {
	if !h.Block.IsConcrete() {
		return m.notify(toastInfo, h.Name+" is a pattern, not a server")
	}
	c := exec.Command("ssh", "-F", m.st.Paths.SSHConfig, h.Name)
	name := h.Name
	return tea.ExecProcess(c, func(err error) tea.Msg { return execDoneMsg{host: name, err: err} })
}

func commitMessage(verb, host string) string {
	return fmt.Sprintf("ssh-config-editor: %s %s (%s)", verb, host, gitsync.Author())
}

func (m *Model) openMoveDialog(h *store.Host) {
	d := &dialog{title: "Move " + h.Name, body: "To which source? The block is moved as is, comments included."}
	for _, src := range m.st.Sources {
		if src == h.Source {
			continue
		}
		src := src
		label := src.Label()
		if src.Kind == store.KindRepo {
			label += sMuted.Render("  → " + moveFile(h) + ".conf")
		}
		d.choices = append(d.choices, choice{label: label, run: func(m *Model) tea.Cmd {
			touched, err := m.st.MoveHost(h, store.Target{Source: src, File: moveFile(h)})
			if err != nil {
				return m.notify(toastErr, err.Error())
			}
			// The previous repository needs syncing too.
			if h.Source.Kind == store.KindRepo {
				touched = appendSource(touched, h.Source)
			}
			m.rebuildRows()
			newPath, _ := m.st.TargetPath(store.Target{Source: src, File: moveFile(h)})
			m.selectHost(newPath, h.Name)
			return tea.Batch(
				m.notify(toastOK, fmt.Sprintf("%s moved to %s", h.Name, src.Label())),
				m.syncTouched(touched, commitMessage("move", h.Name)),
			)
		}})
	}
	d.choices = append(d.choices, choice{label: sMuted.Render("cancel")})
	m.dialog = d
}

// moveFile keeps the original file name, or derives the group from the name.
func moveFile(h *store.Host) string {
	if h.Source.Kind == store.KindRepo {
		return strings.TrimSuffix(h.File(), ".conf")
	}
	return store.GroupKey(h.Name)
}

func appendSource(list []*store.Source, src *store.Source) []*store.Source {
	for _, s := range list {
		if s == src {
			return list
		}
	}
	return append(list, src)
}

func (m *Model) openDeleteDialog(h *store.Host) {
	where := h.Source.Label()
	if h.Source.Kind == store.KindRepo {
		where += " (" + h.File() + ", shared: deleted for everyone else too)"
	}
	m.dialog = &dialog{
		title:  "Delete " + h.Name + "?",
		body:   "Defined in " + where + ".",
		danger: true,
		choices: []choice{
			{label: "no, keep it", key: "n"},
			{label: "yes, delete", key: "y", run: func(m *Model) tea.Cmd {
				touched, err := m.st.DeleteHost(h)
				if err != nil {
					return m.notify(toastErr, err.Error())
				}
				m.rebuildRows()
				return tea.Batch(
					m.notify(toastOK, h.Name+" deleted"),
					m.syncTouched(touched, commitMessage("delete", h.Name)),
				)
			}},
		},
	}
}

// ------------------------------------------------------------------ rendering

// View renders the active screen.
func (m *Model) View() string {
	if m.w == 0 {
		return ""
	}
	if m.dialog != nil {
		return m.dialog.view(m.w, m.h)
	}
	switch m.screen {
	case scrForm:
		return m.form.view(m)
	case scrRepos:
		return m.viewRepos()
	case scrImport:
		return m.viewImport()
	case scrConflict:
		return m.viewConflict()
	case scrHelp:
		return m.viewHelp()
	}
	return m.viewMain()
}

func (m *Model) titleBar(right string) string {
	left := sTitle.Render("ssh-config-editor")
	gap := m.w - lipgloss.Width(left) - lipgloss.Width(right) - 2
	if gap < 1 {
		return " " + left
	}
	return " " + left + strings.Repeat(" ", gap) + right + " "
}

// footer shows the notification if there is one, otherwise the keys.
func (m *Model) footer(help string) string {
	if m.toast != "" {
		return m.toastLine()
	}
	return " " + truncate(help, m.w-2)
}

func (m *Model) toastLine() string {
	st := sMuted
	icon := "•"
	switch m.toastKind {
	case toastOK:
		st, icon = sOK, "✓"
	case toastWarn:
		st, icon = sWarn, "!"
	case toastErr:
		st, icon = sErr, "✗"
	}
	return " " + truncate(st.Render(icon+" "+m.toast), m.w-2)
}

func (m *Model) viewMain() string {
	nRepos := len(m.st.Repos())
	right := sMuted.Render(fmt.Sprintf("%d %s · %d %s", len(m.st.Hosts), plural(len(m.st.Hosts), "host", "hosts"), nRepos, plural(nRepos, "repository", "repositories")))
	var parts []string
	parts = append(parts, m.titleBar(right))
	if m.filtering || m.filter.Value() != "" {
		parts = append(parts, " "+m.filter.View())
	}

	innerH := m.listHeight()
	leftW := min(max(30, m.w*2/5), 52)
	rightW := max(10, m.w-leftW-8)
	if m.cursor < m.top {
		m.top = m.cursor
	}
	if m.cursor >= m.top+innerH {
		m.top = m.cursor - innerH + 1
	}
	var lines []string
	for i := m.top; i < len(m.rows) && i < m.top+innerH; i++ {
		lines = append(lines, m.renderRow(m.rows[i], i == m.cursor, leftW))
	}
	if len(m.rows) == 0 {
		lines = append(lines, sMuted.Render("no matching Host"))
	}
	left := pane("Hosts", lines, leftW, innerH, true)
	detail := pane(m.detailTitle(), m.detailLines(rightW), rightW, innerH, false)
	parts = append(parts, lipgloss.JoinHorizontal(lipgloss.Top, left, detail))
	// Notifications take the status line so the keys always stay visible.
	if m.toast != "" {
		parts = append(parts, m.toastLine())
	} else {
		parts = append(parts, " "+truncate(m.statusBar(), m.w-2))
	}
	parts = append(parts, " "+truncate(m.mainHelp(), m.w-2))
	return strings.Join(parts, "\n")
}

// mainHelp lists the keys that apply to the selected row, most useful first:
// the line is cut on narrow terminals.
func (m *Model) mainHelp() string {
	if m.current().host != nil {
		return helpLine("enter", "connect", "e", "edit", "c", "duplicate", "m", "move", "x", "delete",
			"n", "new", "/", "filter", "r", "sync", "R", "repos", "?", "help", "q", "quit")
	}
	return helpLine("enter", "fold", "n", "new", "r", "sync", "R", "repos", "I", "import",
		"/", "filter", "?", "help", "q", "quit")
}

func (m *Model) renderRow(r row, selected bool, w int) string {
	var s string
	if r.host == nil {
		arrow := "▾"
		if m.collapsed[r.src.Name] && m.filter.Value() == "" {
			arrow = "▸"
		}
		count := len(m.st.HostsOf(r.src))
		s = sAccent.Render(arrow) + " " + sBold.Render(r.src.Label()) + " " + sMuted.Render(fmt.Sprint(count))
		if r.src.Kind == store.KindRepo {
			s += " " + m.repoBadge(r.src.Name)
		}
	} else {
		name := r.host.Name
		switch {
		case r.host.ShadowedBy != nil:
			s = "  " + sMuted.Render(name) + " " + sWarn.Render("⚠ shadowed")
		case !r.host.Block.IsConcrete():
			s = "  " + sAccent.Render(name)
		default:
			s = "  " + name
			if hn := r.host.Block.Get("HostName"); hn != "" && lipgloss.Width(name)+lipgloss.Width(hn)+6 < w {
				pad := w - lipgloss.Width(name) - lipgloss.Width(hn) - 3
				s += strings.Repeat(" ", pad) + sMuted.Render(hn)
			}
		}
	}
	s = truncate(s, w)
	if selected {
		plain := s
		if pad := w - lipgloss.Width(plain); pad > 0 {
			plain += strings.Repeat(" ", pad)
		}
		return sSel.Render(plain)
	}
	return s
}

func (m *Model) repoBadge(name string) string {
	rs := m.repo(name)
	switch {
	case len(rs.conflicts) > 0:
		return sErr.Render("✗ conflict")
	case rs.syncing:
		return sWarn.Render("⟳")
	case rs.err != "":
		return sErr.Render("✗")
	case rs.status.Ahead > 0:
		return sWarn.Render(fmt.Sprintf("↑%d", rs.status.Ahead))
	case rs.status.Dirty:
		return sWarn.Render("●")
	}
	return sOK.Render("✓")
}

func (m *Model) statusBar() string {
	repos := m.st.Repos()
	if len(repos) == 0 {
		return sMuted.Render("no repository configured: R to add one, I to import ~/.ssh/config")
	}
	var parts []string
	for _, src := range repos {
		parts = append(parts, src.Name+" "+m.repoBadge(src.Name))
	}
	return strings.Join(parts, sMuted.Render(" · "))
}

func (m *Model) detailTitle() string {
	r := m.current()
	if r.host != nil {
		return r.host.Name
	}
	if r.src != nil {
		return r.src.Label()
	}
	return ""
}

var effectiveKeys = []string{"hostname", "user", "port", "identityfile", "proxyjump", "forwardagent"}

func (m *Model) detailLines(w int) []string {
	r := m.current()
	if r.src == nil {
		return m.onboarding()
	}
	if r.host == nil {
		if len(m.st.Repos()) == 0 {
			return m.onboarding()
		}
		return m.sourceDetail(r.src)
	}
	h := r.host
	var lines []string
	where := h.Source.Label()
	if h.Source.Kind == store.KindRepo {
		where += " · " + h.File()
	}
	lines = append(lines, sMuted.Render(where))
	if h.ShadowedBy != nil {
		lines = append(lines, sWarn.Render("⚠ shadowed by the definition in "+h.ShadowedBy.Label()+" (the first one wins)"))
	} else if h.Duplicated {
		lines = append(lines, sWarn.Render("⚠ also defined elsewhere: this definition wins"))
	}
	lines = append(lines, "", sBold.Render("Definition"))
	for _, l := range strings.Split(h.Block.Text(), "\n") {
		lines = append(lines, "  "+highlight(l))
	}
	if !h.Block.IsConcrete() {
		return lines
	}
	lines = append(lines, "", sBold.Render("Effective")+sMuted.Render("  ssh -G, all files combined"))
	e := m.eff[h.Name]
	switch {
	case e == nil || e.loading:
		lines = append(lines, sMuted.Render("  computing…"))
	case e.err != "":
		lines = append(lines, sErr.Render("  "+firstLine(e.err)))
	default:
		for _, k := range effectiveKeys {
			if v := e.values[k]; v != "" {
				lines = append(lines, "  "+sMuted.Render(fmt.Sprintf("%-13s", k))+v)
			}
		}
	}
	return lines
}

func highlight(l string) string {
	t := strings.TrimSpace(l)
	switch {
	case t == "":
		return ""
	case strings.HasPrefix(t, "#"):
		return sMuted.Render(l)
	}
	lead := l[:len(l)-len(strings.TrimLeft(l, " \t"))]
	k, v, _ := strings.Cut(t, " ")
	if strings.EqualFold(k, "host") || strings.EqualFold(k, "match") {
		return lead + sTitle.Render(k) + " " + sBold.Render(v)
	}
	return lead + sAccent.Render(k) + " " + v
}

func (m *Model) sourceDetail(src *store.Source) []string {
	n := len(m.st.HostsOf(src))
	var lines []string
	switch src.Kind {
	case store.KindLocal:
		lines = append(lines,
			sMuted.Render(m.st.Paths.LocalConf()),
			"",
			"Personal overrides, never shared.",
			"Included first: they win over the repositories.",
			"",
			sMuted.Render("E.g. a Host fairfair-* with your own User or IdentityFile."),
		)
	case store.KindMain:
		lines = append(lines,
			sMuted.Render(m.st.Paths.SSHConfig),
			"",
			"What is left outside the repositories, read last.",
			sMuted.Render("I to move these Hosts into a repository or into local."),
		)
	case store.KindRepo:
		rs := m.repo(src.Name)
		lines = append(lines, sMuted.Render(src.Repo.URL+" ("+src.Repo.Branch+")"), "")
		switch {
		case len(rs.conflicts) > 0:
			lines = append(lines, sErr.Render("✗ pending conflict: press C"))
		case rs.syncing:
			lines = append(lines, sWarn.Render("⟳ syncing…"))
		case rs.err != "":
			lines = append(lines, sErr.Render("✗ "+rs.err))
		case rs.status.Ahead > 0:
			lines = append(lines, sWarn.Render(fmt.Sprintf("↑ %d commit(s) to push", rs.status.Ahead)))
		default:
			lines = append(lines, sOK.Render("✓ up to date"))
		}
		files := m.st.ConfFiles(src)
		sort.Strings(files)
		lines = append(lines, "", sBold.Render("Files"))
		for _, f := range files {
			lines = append(lines, "  "+f)
		}
	}
	lines = append(lines, "", sMuted.Render(fmt.Sprintf("%d %s · n to add one here", n, plural(n, "Host", "Hosts"))))
	return lines
}

func (m *Model) onboarding() []string {
	return []string{
		sBold.Render("Welcome"),
		"",
		"1. " + sKey.Render("R") + " adds a shared git repository",
		"2. " + sKey.Render("I") + " moves your current Hosts into the repositories",
		"3. " + sKey.Render("n") + " creates a Host: it is pushed automatically",
	}
}

func (m *Model) viewHelp() string {
	sections := []struct {
		title string
		keys  []string
	}{
		{"Navigation", []string{"↑/↓ j/k", "move", "←/→ h/l", "collapse / expand a group", "/", "filter (name, IP, user)", "esc", "clear the filter"}},
		{"Host", []string{"enter", "connect", "n", "new Host", "e", "edit", "c", "duplicate", "m", "move to another source", "x", "delete"}},
		{"Sync", []string{"r", "sync all repositories", "R", "manage repositories", "I", "import ~/.ssh/config", "C", "resolve a conflict"}},
		{"Form", []string{"tab / ↑↓", "next / previous field", "→", "accept the suggestion", "←/→ ↑↓", "choose (inline choices, lists)", "h / alt+h", "explain the field", "ctrl+s", "save", "esc", "cancel"}},
	}
	var lines []string
	lines = append(lines, m.titleBar(sMuted.Render("help")), "")
	for _, s := range sections {
		lines = append(lines, "  "+sBold.Render(s.title))
		for i := 0; i+1 < len(s.keys); i += 2 {
			lines = append(lines, fmt.Sprintf("    %s %s", sKey.Render(fmt.Sprintf("%-10s", s.keys[i])), s.keys[i+1]))
		}
		lines = append(lines, "")
	}
	lines = append(lines,
		"  "+sBold.Render("How it works"),
		"    ~/.ssh/config starts with an Include block managed by the tool: local.conf,",
		"    then each repository in priority order. In ssh, the first value wins.",
		"    Every change is committed, merged Host by Host with the remote, then pushed.",
		"    The same Host changed on both sides opens the conflict screen.",
		"",
		"  "+sMuted.Render("press any key to go back"),
	)
	return box(lines, m.w, m.h)
}
