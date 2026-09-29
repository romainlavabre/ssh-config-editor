package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/romainlavabre/ssh-config-editor/internal/sshconfig"
	"github.com/romainlavabre/ssh-config-editor/internal/store"
)

type formMode int

const (
	formNew formMode = iota
	formEdit
	formDuplicate
)

// Form fields, in tab order.
const (
	fAlias = iota
	fHostName
	fUser
	fPort
	fIdentity
	fProxy
	fDest
	fFile
	fExtra
	fieldCount
)

// Directives edited in a dedicated field; everything else goes to "Other options".
var mainKeys = map[int]string{
	fHostName: "HostName",
	fUser:     "User",
	fPort:     "Port",
	fIdentity: "IdentityFile",
	fProxy:    "ProxyJump",
}

var labels = map[int]string{
	fAlias:    "Alias (Host)",
	fHostName: "HostName",
	fUser:     "User",
	fPort:     "Port",
	fIdentity: "IdentityFile",
	fProxy:    "ProxyJump",
	fDest:     "Destination",
	fFile:     "File",
	fExtra:    "Other options",
}

type hostForm struct {
	mode        formMode
	old         *store.Host
	inputs      [fProxy + 1]textinput.Model
	file        textinput.Model
	fileTouched bool
	extra       textarea.Model
	dests       []*store.Source
	dest        int
	focus       int
	err         string
	st          *store.Store
	w, h        int
}

const labelW = 16

func (m *Model) openForm(mode formMode, h *store.Host, near *store.Source) {
	f := &hostForm{mode: mode, st: m.st, dests: m.st.Sources}
	if mode == formEdit {
		f.old = h
	}

	placeholders := map[int]string{
		fAlias:    "mon-serveur",
		fHostName: "10.0.0.1 or DNS name",
		fUser:     "ubuntu",
		fPort:     "22",
		fIdentity: "~/.ssh/id_ed25519",
		fProxy:    "bastion",
	}
	for i := range f.inputs {
		ti := textinput.New()
		ti.Prompt = ""
		ti.Placeholder = placeholders[i]
		ti.ShowSuggestions = true
		f.inputs[i] = ti
	}
	f.inputs[fIdentity].SetSuggestions(m.st.Keys())
	var names []string
	for _, x := range m.st.Hosts {
		if x.Block.IsConcrete() {
			names = append(names, x.Name)
		}
	}
	f.inputs[fProxy].SetSuggestions(names)
	f.inputs[fPort].CharLimit = 5

	f.file = textinput.New()
	f.file.Prompt = ""
	f.file.ShowSuggestions = true
	f.file.Placeholder = "hosts.conf"

	f.extra = textarea.New()
	f.extra.ShowLineNumbers = false
	f.extra.Placeholder = "ForwardAgent yes\nLocalForward 5432 localhost:5432"
	f.extra.SetHeight(5)

	// Default destination: the Host's source, otherwise the group under the
	// cursor, otherwise the first repository.
	dest := near
	if h != nil {
		dest = h.Source
	}
	if dest == nil || (mode == formNew && dest.Kind == store.KindMain) {
		dest = m.st.Sources[0]
		if repos := m.st.Repos(); len(repos) > 0 {
			dest = repos[0]
		}
	}
	for i, s := range f.dests {
		if s == dest {
			f.dest = i
		}
	}

	if h != nil {
		name := h.Name
		if mode == formDuplicate {
			name += "-copy"
		}
		f.inputs[fAlias].SetValue(name)
		var extra []string
		for _, o := range h.Block.Options() {
			placed := false
			for idx, key := range mainKeys {
				if strings.EqualFold(o.Key, key) && f.inputs[idx].Value() == "" {
					f.inputs[idx].SetValue(o.Value)
					placed = true
					break
				}
			}
			if !placed {
				extra = append(extra, o.Key+" "+o.Value)
			}
		}
		f.extra.SetValue(strings.Join(extra, "\n"))
		if h.Source.Kind == store.KindRepo {
			f.file.SetValue(h.File())
			f.fileTouched = true
		}
	}
	f.refreshFile()
	f.resize(m.w, m.h)
	m.form = f
	m.screen = scrForm
}

func (f *hostForm) destSource() *store.Source { return f.dests[f.dest] }

// refreshFile suggests the files of the chosen repository and, until the user
// picks one, derives the file from the alias prefix.
func (f *hostForm) refreshFile() {
	src := f.destSource()
	files := f.st.ConfFiles(src)
	f.file.SetSuggestions(files)
	if f.fileTouched || src.Kind != store.KindRepo {
		return
	}
	alias := strings.TrimSpace(f.inputs[fAlias].Value())
	if alias == "" {
		if len(files) > 0 {
			f.file.SetValue(files[0])
		}
		return
	}
	f.file.SetValue(store.GroupKey(strings.Fields(alias)[0]) + ".conf")
}

func (f *hostForm) resize(w, h int) {
	f.w, f.h = w, h
	iw := max(20, min(70, w-labelW-8))
	for i := range f.inputs {
		f.inputs[i].Width = iw
	}
	f.file.Width = iw
	f.extra.SetWidth(iw)
}

func (f *hostForm) focusCmd() tea.Cmd {
	for i := range f.inputs {
		f.inputs[i].Blur()
	}
	f.file.Blur()
	f.extra.Blur()
	switch {
	case f.focus <= fProxy:
		return f.inputs[f.focus].Focus()
	case f.focus == fFile:
		return f.file.Focus()
	case f.focus == fExtra:
		return f.extra.Focus()
	}
	return nil
}

func (f *hostForm) skip(i int) bool {
	return i == fFile && f.destSource().Kind != store.KindRepo
}

func (f *hostForm) moveFocus(delta int) tea.Cmd {
	i := f.focus
	for {
		i = (i + delta + fieldCount) % fieldCount
		if !f.skip(i) {
			break
		}
	}
	f.focus = i
	return f.focusCmd()
}

func (f *hostForm) forward(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	switch {
	case f.focus <= fProxy:
		f.inputs[f.focus], cmd = f.inputs[f.focus].Update(msg)
	case f.focus == fFile:
		f.file, cmd = f.file.Update(msg)
	case f.focus == fExtra:
		f.extra, cmd = f.extra.Update(msg)
	}
	return cmd
}

// acceptSuggestion completes the field when the cursor is at the end of the input.
func acceptSuggestion(ti *textinput.Model) bool {
	s := ti.CurrentSuggestion()
	if s == "" || s == ti.Value() || ti.Position() < len([]rune(ti.Value())) {
		return false
	}
	ti.SetValue(s)
	ti.CursorEnd()
	return true
}

func (m *Model) updateForm(k tea.KeyMsg) tea.Cmd {
	f := m.form
	switch k.String() {
	case "esc":
		m.form = nil
		m.screen = scrMain
		return nil
	case "ctrl+s":
		return m.saveForm()
	case "tab":
		return f.moveFocus(1)
	case "shift+tab":
		return f.moveFocus(-1)
	case "up":
		if f.focus != fExtra || f.extra.Line() == 0 {
			return f.moveFocus(-1)
		}
	case "down":
		if f.focus != fExtra || f.extra.Line() >= f.extra.LineCount()-1 {
			if f.focus == fExtra {
				return nil
			}
			return f.moveFocus(1)
		}
	case "enter":
		if f.focus != fExtra {
			if f.focus == fIdentity || f.focus == fProxy || f.focus == fFile {
				// enter also accepts the displayed suggestion.
				if f.focus == fFile {
					acceptSuggestion(&f.file)
				} else {
					acceptSuggestion(&f.inputs[f.focus])
				}
			}
			return f.moveFocus(1)
		}
	case "left", "right":
		if f.focus == fDest {
			d := 1
			if k.String() == "left" {
				d = -1
			}
			f.dest = (f.dest + d + len(f.dests)) % len(f.dests)
			f.refreshFile()
			return nil
		}
		if k.String() == "right" {
			if f.focus <= fProxy && acceptSuggestion(&f.inputs[f.focus]) {
				return nil
			}
			if f.focus == fFile && acceptSuggestion(&f.file) {
				return nil
			}
		}
	}
	if f.focus == fDest {
		return nil
	}
	cmd := f.forward(k)
	if f.focus == fAlias {
		f.refreshFile()
	}
	if f.focus == fFile {
		f.fileTouched = true
	}
	f.err = ""
	return cmd
}

func (m *Model) saveForm() tea.Cmd {
	f := m.form
	alias := strings.TrimSpace(f.inputs[fAlias].Value())
	if alias == "" {
		f.err = "the alias is required"
		f.focus = fAlias
		return f.focusCmd()
	}
	if p := strings.TrimSpace(f.inputs[fPort].Value()); p != "" {
		if n, err := strconv.Atoi(p); err != nil || n < 1 || n > 65535 {
			f.err = "the port must be a number between 1 and 65535"
			f.focus = fPort
			return f.focusCmd()
		}
	}
	var opts []sshconfig.Option
	for _, idx := range []int{fHostName, fUser, fPort, fIdentity, fProxy} {
		if v := strings.TrimSpace(f.inputs[idx].Value()); v != "" {
			opts = append(opts, sshconfig.Option{Key: mainKeys[idx], Value: v})
		}
	}
	for n, l := range strings.Split(f.extra.Value(), "\n") {
		key, val := sshconfig.SplitLine(l)
		if key == "" {
			continue
		}
		if val == "" {
			f.err = fmt.Sprintf("other options, line %d: missing value for %s", n+1, key)
			f.focus = fExtra
			return f.focusCmd()
		}
		if strings.EqualFold(key, "host") || strings.EqualFold(key, "match") {
			f.err = fmt.Sprintf("other options, line %d: %s would open a new block", n+1, key)
			f.focus = fExtra
			return f.focusCmd()
		}
		opts = append(opts, sshconfig.Option{Key: key, Value: val})
	}

	target := store.Target{Source: f.destSource(), File: f.file.Value()}
	oldSource := (*store.Source)(nil)
	if f.old != nil {
		oldSource = f.old.Source
	}
	touched, err := m.st.SaveHost(f.old, target, alias, opts)
	if err != nil {
		f.err = err.Error()
		return nil
	}
	if oldSource != nil && oldSource != target.Source && oldSource.Kind == store.KindRepo {
		touched = appendSource(touched, oldSource)
	}
	verb := "add"
	if f.mode == formEdit {
		verb = "update"
	}
	m.form = nil
	m.screen = scrMain
	m.rebuildRows()
	path, _ := m.st.TargetPath(target)
	m.selectHost(path, strings.Join(strings.Fields(alias), " "))

	text := alias + " saved"
	if len(touched) > 0 {
		text += " · syncing…"
	}
	return tea.Batch(m.notify(toastOK, text), m.syncTouched(touched, commitMessage(verb, alias)), m.loadEffective())
}

func (f *hostForm) view(m *Model) string {
	title := "New Host"
	switch f.mode {
	case formEdit:
		title = "Edit " + f.old.Name
	case formDuplicate:
		title = "Duplicate"
	}
	lines := []string{m.titleBar(sMuted.Render(title)), ""}

	label := func(i int) string {
		l := fmt.Sprintf("%-*s", labelW, labels[i])
		if f.focus == i {
			return sAccent.Render("› ") + sBold.Render(l)
		}
		return "  " + sMuted.Render(l)
	}
	for i := fAlias; i <= fProxy; i++ {
		lines = append(lines, " "+label(i)+f.inputs[i].View())
	}

	src := f.destSource()
	dest := "‹ " + src.Label() + " ›"
	if f.focus == fDest {
		dest = sAccent.Render("‹ ") + sBold.Render(src.Label()) + sAccent.Render(" ›")
	}
	hint := map[store.Kind]string{
		store.KindLocal: "personal, never shared",
		store.KindRepo:  "shared through git",
		store.KindMain:  "~/.ssh/config, not shared",
	}[src.Kind]
	lines = append(lines, "", " "+label(fDest)+dest+"  "+sMuted.Render(hint))
	if src.Kind == store.KindRepo {
		files := f.st.ConfFiles(src)
		lines = append(lines, " "+label(fFile)+f.file.View())
		if len(files) > 0 {
			lines = append(lines, " "+strings.Repeat(" ", labelW+2)+sMuted.Render(truncate("existing: "+strings.Join(files, ", "), max(10, m.w-labelW-6))))
		}
	}
	lines = append(lines, "", " "+label(fExtra)+sMuted.Render("one directive per line"))
	extra := lipgloss.NewStyle().PaddingLeft(labelW + 3).Render(f.extra.View())
	lines = append(lines, strings.Split(extra, "\n")...)

	if f.err != "" {
		lines = append(lines, "", " "+sErr.Render("✗ "+f.err))
	}
	body := box(lines, m.w, m.h-1)
	help := helpLine("tab/↑↓", "field", "→", "complete", "←/→", "destination", "ctrl+s", "save and push", "esc", "cancel")
	return body + "\n" + m.footer(help)
}
