package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

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

// choiceList is a list field: every option stays visible, the selected one is
// marked. An optional last entry takes a typed value (custom path, new file).
type choiceList struct {
	values      []string
	labels      []string // displayed text, parallel to values
	hints       []string
	idx         int
	custom      textinput.Model
	customLabel string // empty: no typed entry
}

func newChoiceList(customLabel, placeholder string) choiceList {
	ti := textinput.New()
	ti.Prompt = ""
	ti.Placeholder = placeholder
	return choiceList{custom: ti, customLabel: customLabel}
}

func (c *choiceList) setOptions(values, labels []string) {
	c.values, c.labels = values, labels
	c.idx = max(0, min(c.idx, c.last()))
}

func (c *choiceList) last() int {
	if c.customLabel != "" {
		return len(c.values)
	}
	return len(c.values) - 1
}

func (c *choiceList) customSelected() bool { return c.customLabel != "" && c.idx >= len(c.values) }

func (c *choiceList) value() string {
	if c.customSelected() {
		return strings.TrimSpace(c.custom.Value())
	}
	if c.idx >= 0 && c.idx < len(c.values) {
		return c.values[c.idx]
	}
	return ""
}

// selectValue selects the option matching v, or types v in the custom entry.
// Without a custom entry, an unknown value is added as an option so it is kept.
func (c *choiceList) selectValue(v string, same func(a, b string) bool) {
	for i, x := range c.values {
		if same(x, v) {
			c.idx = i
			c.custom.SetValue("") // no stale value when the typed entry is picked later
			return
		}
	}
	if c.customLabel == "" {
		c.values = append(c.values, v)
		c.labels = append(c.labels, v)
		c.idx = len(c.values) - 1
		return
	}
	c.idx = len(c.values)
	c.custom.SetValue(v)
	c.custom.CursorEnd()
}

func (c *choiceList) rows(focus bool) []string {
	labels := append([]string(nil), c.labels...)
	if c.customLabel != "" {
		entry := c.customLabel + "…"
		if c.customSelected() {
			if focus {
				entry = c.customLabel + ": " + c.custom.View()
			} else {
				entry = c.customLabel + ": " + c.value()
			}
		}
		labels = append(labels, entry)
	}
	return radioList(labels, c.hints, c.idx, focus)
}

func sameString(a, b string) bool { return a == b }

// samePath compares two paths, ~ expanded.
func samePath(a, b string) bool {
	if a == "" || b == "" {
		return a == b
	}
	return expandHome(a) == expandHome(b)
}

func expandHome(p string) string {
	if rest, ok := strings.CutPrefix(p, "~/"); ok {
		if home, err := os.UserHomeDir(); err == nil {
			p = filepath.Join(home, rest)
		}
	}
	return filepath.Clean(p)
}

// formField is a field of the form, built from a fieldDef.
type formField struct {
	fieldDef
	input textinput.Model // kindText
	list  choiceList      // kindChoice and kindList
}

// directive reports whether the field is written as an ssh directive.
func (x *formField) directive() bool {
	return x.id != idAlias && x.id != idDest && x.id != idFile && x.id != idAdvanced
}

func (x *formField) value() string {
	switch x.kind {
	case kindText:
		return strings.TrimSpace(x.input.Value())
	case kindToggle:
		return ""
	}
	return x.list.value()
}

// takesText reports whether letters typed now go into the field.
func (x *formField) takesText() bool {
	return x.kind == kindText || (x.kind == kindList && x.list.customSelected())
}

type hostForm struct {
	mode        formMode
	old         *store.Host
	fields      []*formField
	byID        map[string]*formField
	dests       []*store.Source
	kept        []sshconfig.Option // directives without a field: written back untouched
	fileTouched bool
	advanced    bool // advanced fields shown
	focus       int
	helpOpen    bool
	top         int // first visible line when the form scrolls
	err         string
	st          *store.Store
	w, h        int
}

const labelW = 24

func (f *hostForm) field(id string) *formField { return f.byID[id] }
func (f *hostForm) cur() *formField            { return f.fields[f.focus] }

func (f *hostForm) focusOn(id string) {
	for i, x := range f.fields {
		if x.id == id {
			f.focus = i
		}
	}
}

// fieldFor returns the field editing an ssh directive, or nil.
func (f *hostForm) fieldFor(key string) *formField {
	for _, x := range f.fields {
		if x.directive() && strings.EqualFold(x.id, key) {
			return x
		}
	}
	return nil
}

func (m *Model) openForm(mode formMode, h *store.Host, near *store.Source) {
	f := &hostForm{mode: mode, st: m.st, dests: m.st.Sources, byID: map[string]*formField{}}
	if mode == formEdit {
		f.old = h
	}
	for _, d := range fieldDefs {
		x := &formField{fieldDef: d}
		switch d.kind {
		case kindText:
			ti := textinput.New()
			ti.Prompt = ""
			ti.Placeholder = d.placeholder
			x.input = ti
		case kindChoice:
			x.list = newChoiceList("", "")
			x.list.setOptions(append([]string{""}, d.choices...), append([]string{"unset"}, d.choices...))
		}
		f.fields = append(f.fields, x)
		f.byID[d.id] = x
	}

	var names []string
	for _, x := range m.st.Hosts {
		if x.Block.IsConcrete() {
			names = append(names, x.Name)
		}
	}
	proxy := f.field("ProxyJump")
	proxy.input.ShowSuggestions = true
	proxy.input.SetSuggestions(names)
	f.field("Port").input.CharLimit = 5

	keys := m.st.Keys()
	identity := f.field("IdentityFile")
	identity.list = newChoiceList("custom path", "~/.ssh/id_ed25519")
	identity.list.setOptions(append([]string{""}, keys...), append([]string{"none"}, keys...))

	hints := map[store.Kind]string{
		store.KindLocal: "personal, never shared",
		store.KindRepo:  "shared through git",
		store.KindMain:  "not shared",
	}
	dest := f.field(idDest)
	dest.list = newChoiceList("", "")
	var destNames, destLabels []string
	for _, s := range f.dests {
		destNames = append(destNames, s.Name)
		destLabels = append(destLabels, s.Label())
		dest.list.hints = append(dest.list.hints, hints[s.Kind])
	}
	dest.list.setOptions(destNames, destLabels)
	f.field(idFile).list = newChoiceList("new file", "hosts.conf")

	// Default destination: the Host's source, otherwise the group under the
	// cursor, otherwise the first repository.
	src := near
	if h != nil {
		src = h.Source
	}
	if src == nil || (mode == formNew && src.Kind == store.KindMain) {
		src = m.st.Sources[0]
		if repos := m.st.Repos(); len(repos) > 0 {
			src = repos[0]
		}
	}
	dest.list.selectValue(src.Name, sameString)

	if h != nil {
		name := h.Name
		if mode == formDuplicate {
			name += "-copy"
		}
		f.field(idAlias).input.SetValue(name)
		filled := map[*formField]bool{}
		for _, o := range h.Block.Options() {
			x := f.fieldFor(o.Key)
			if x == nil || filled[x] {
				f.kept = append(f.kept, o)
				continue
			}
			filled[x] = true
			switch {
			case x.kind == kindText:
				x.input.SetValue(o.Value)
			case x.id == "IdentityFile":
				x.list.selectValue(o.Value, samePath)
			default:
				x.list.selectValue(o.Value, strings.EqualFold)
			}
		}
	}
	f.refreshFile()
	f.resize(m.w, m.h)
	m.form = f
	m.screen = scrForm
}

func (f *hostForm) destSource() *store.Source { return f.dests[f.field(idDest).list.idx] }

// refreshFile lists the files of the chosen repository and selects one: the
// user's choice if they made one, otherwise the Host's current file, otherwise
// the file derived from the alias prefix (a new file if it does not exist yet).
func (f *hostForm) refreshFile() {
	file := &f.field(idFile).list
	src := f.destSource()
	want := file.value() // read before the options change
	files := f.st.ConfFiles(src)
	file.setOptions(files, files)
	if !f.fileTouched {
		alias := f.field(idAlias).value()
		switch {
		case f.old != nil && f.old.Source == src:
			want = f.old.File()
		case alias != "":
			want = store.GroupKey(strings.Fields(alias)[0]) + ".conf"
		case len(files) > 0:
			want = files[0]
		}
	}
	file.selectValue(want, sameString)
}

func (f *hostForm) resize(w, h int) {
	f.w, f.h = w, h
	iw := max(20, min(60, w-labelW-8))
	for _, x := range f.fields {
		x.input.Width = iw
		x.list.custom.Width = max(10, iw-16)
	}
}

func (f *hostForm) focusCmd() tea.Cmd {
	for _, x := range f.fields {
		x.input.Blur()
		x.list.custom.Blur()
	}
	x := f.cur()
	switch {
	case x.kind == kindText:
		return x.input.Focus()
	case x.kind == kindList && x.list.customSelected():
		return x.list.custom.Focus()
	}
	return nil
}

func (f *hostForm) skip(i int) bool {
	x := f.fields[i]
	return (x.advanced && !f.advanced) || (x.id == idFile && f.destSource().Kind != store.KindRepo)
}

// advancedSet counts the advanced fields that have a value.
func (f *hostForm) advancedSet() int {
	n := 0
	for _, x := range f.fields {
		if x.advanced && x.value() != "" {
			n++
		}
	}
	return n
}

func (f *hostForm) moveFocus(delta int) tea.Cmd {
	i := f.focus
	for {
		i = (i + delta + len(f.fields)) % len(f.fields)
		if !f.skip(i) {
			break
		}
	}
	f.focus = i
	return f.focusCmd()
}

// moveInList moves the selection of a vertical list; leaving the list at the
// top or bottom moves to the previous or next field.
func (f *hostForm) moveInList(delta int) tea.Cmd {
	x := f.cur()
	next := x.list.idx + delta
	if next < 0 || next > x.list.last() {
		return f.moveFocus(delta)
	}
	x.list.idx = next
	switch x.id {
	case idDest:
		f.refreshFile()
	case idFile:
		f.fileTouched = true
	}
	return f.focusCmd()
}

func (f *hostForm) forward(msg tea.Msg) tea.Cmd {
	x := f.cur()
	var cmd tea.Cmd
	switch {
	case x.kind == kindText:
		x.input, cmd = x.input.Update(msg)
	case x.kind == kindList && x.list.customSelected():
		x.list.custom, cmd = x.list.custom.Update(msg)
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
	x := f.cur()
	if f.helpOpen {
		// Any key closes the help; esc must not also close the form.
		f.helpOpen = false
		return nil
	}
	if x.kind == kindToggle {
		switch k.String() {
		case "enter", " ":
			f.advanced = !f.advanced
			return nil
		case "right":
			f.advanced = true
			return nil
		case "left":
			f.advanced = false
			return nil
		}
	}
	switch k.String() {
	case "esc":
		m.form = nil
		m.screen = scrMain
		return nil
	case "ctrl+s":
		return m.saveForm()
	case "alt+h":
		f.helpOpen = true
		return nil
	case "h":
		if !x.takesText() {
			f.helpOpen = true
			return nil
		}
	case "tab":
		return f.moveFocus(1)
	case "shift+tab":
		return f.moveFocus(-1)
	case "up", "down":
		delta := 1
		if k.String() == "up" {
			delta = -1
		}
		if x.kind == kindList {
			return f.moveInList(delta)
		}
		return f.moveFocus(delta)
	case "left", "right":
		if x.kind == kindChoice {
			delta := 1
			if k.String() == "left" {
				delta = -1
			}
			x.list.idx = (x.list.idx + delta + len(x.list.values)) % len(x.list.values)
			return nil
		}
		if k.String() == "right" && x.kind == kindText && acceptSuggestion(&x.input) {
			return nil
		}
	case "enter":
		if x.kind == kindText {
			// enter also accepts the displayed suggestion.
			acceptSuggestion(&x.input)
		}
		return f.moveFocus(1)
	}

	f.err = ""
	switch x.kind {
	case kindChoice, kindToggle:
		return nil
	case kindList:
		if x.list.customLabel == "" {
			return nil
		}
		if x.id == idFile {
			f.fileTouched = true
		}
		if x.list.customSelected() {
			return f.forward(k)
		}
		// Typing on a listed option switches to the typed entry.
		if k.Type != tea.KeyRunes {
			return nil
		}
		x.list.idx = len(x.list.values)
		x.list.custom.SetValue("")
		return tea.Batch(f.focusCmd(), f.forward(k))
	}
	cmd := f.forward(k)
	if x.id == idAlias {
		f.refreshFile()
	}
	return cmd
}

func (m *Model) saveForm() tea.Cmd {
	f := m.form
	fail := func(id, msg string) tea.Cmd {
		f.err = msg
		if f.field(id).advanced {
			f.advanced = true // the faulty field must be visible
		}
		f.focusOn(id)
		return f.focusCmd()
	}
	alias := f.field(idAlias).value()
	if alias == "" {
		return fail(idAlias, "the alias is required")
	}
	for _, x := range f.fields {
		if !x.numeric || x.value() == "" {
			continue
		}
		n, err := strconv.Atoi(x.value())
		if err != nil || n < 0 || (x.id == "Port" && (n < 1 || n > 65535)) {
			return fail(x.id, x.title()+" must be a number")
		}
	}
	identity := f.field("IdentityFile")
	if identity.list.customSelected() && identity.value() == "" {
		return fail("IdentityFile", "type the key path, or pick none")
	}

	var opts []sshconfig.Option
	for _, x := range f.fields {
		if x.directive() && x.value() != "" {
			opts = append(opts, sshconfig.Option{Key: x.id, Value: x.value()})
		}
	}
	opts = append(opts, f.kept...)

	target := store.Target{Source: f.destSource(), File: f.field(idFile).value()}
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
	header := []string{m.titleBar(sMuted.Render(title))}

	var body []string
	indent := strings.Repeat(" ", labelW+3)
	// First and last line of the focused field (help included): kept visible.
	focusStart, focusEnd := 0, 0
	section := ""
	for i, x := range f.fields {
		if f.skip(i) {
			continue
		}
		focus := i == f.focus
		if x.kind == kindToggle {
			// The "Advanced options" line stands in for its section heading.
			section = x.section
			arrow := "▸"
			if f.advanced {
				arrow = "▾"
			}
			text := sTitle.Render(arrow + " " + x.title())
			count := "none set"
			if n := f.advancedSet(); n > 0 {
				count = fmt.Sprintf("%d set", n)
			}
			lead := "  "
			if focus {
				lead = sAccent.Render("› ")
			}
			body = append(body, "")
			if focus {
				focusStart = len(body)
			}
			body = append(body, " "+lead+text+"  "+sMuted.Render(count))
		} else {
			if x.section != section {
				body = append(body, "", " "+sTitle.Render(x.section))
				section = x.section
			}
			label := "  " + sMuted.Render(fmt.Sprintf("%-*s", labelW, x.title()))
			if focus {
				label = sAccent.Render("› ") + sBold.Render(fmt.Sprintf("%-*s", labelW, x.title()))
			}
			var rows []string
			switch x.kind {
			case kindText:
				rows = []string{x.input.View()}
			case kindChoice:
				rows = []string{inlineChoices(x.list.labels, x.list.idx, focus)}
			case kindList:
				rows = x.list.rows(focus)
			}
			if focus {
				focusStart = len(body)
			}
			for j, r := range rows {
				if j == 0 {
					body = append(body, " "+label+r)
				} else {
					body = append(body, indent+r)
				}
			}
		}
		if focus && f.helpOpen {
			width := max(30, min(70, m.w-len(indent)-6))
			pop := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(cAccent).
				Padding(0, 1).Width(width).
				Render(sBold.Render(x.title()) + "\n" + x.help + "\n" + sMuted.Render("any key to close"))
			for _, l := range strings.Split(pop, "\n") {
				body = append(body, indent+l)
			}
		}
		if focus {
			focusEnd = len(body) - 1
		}
	}
	if len(f.kept) > 0 {
		body = append(body, "", " "+sTitle.Render("Kept as is")+sMuted.Render("  no field for these directives: written back untouched"))
		for _, o := range f.kept {
			body = append(body, "   "+sAccent.Render(o.Key)+" "+o.Value)
		}
	}

	var footer []string
	if f.err != "" {
		footer = append(footer, "", " "+sErr.Render("✗ "+f.err))
	}
	avail := max(3, m.h-1-len(header)-len(footer))
	visible := body
	if len(body) > avail {
		// Show the whole field plus a little context; if it is taller than the
		// screen, its first line wins.
		const margin = 2
		if focusEnd+margin >= f.top+avail {
			f.top = focusEnd + margin - avail + 1
		}
		if focusStart-margin < f.top {
			f.top = focusStart - margin
		}
		f.top = max(0, min(f.top, len(body)-avail))
		visible = append([]string(nil), body[f.top:f.top+avail]...)
		if f.top > 0 {
			visible[0] = sMuted.Render("   ↑ more")
		}
		if f.top+avail < len(body) {
			visible[len(visible)-1] = sMuted.Render("   ↓ more")
		}
	} else {
		f.top = 0
	}

	helpKey := "h"
	if f.cur().takesText() {
		helpKey = "alt+h"
	}
	lines := append(append(header, visible...), footer...)
	help := helpLine("tab/↑↓", "field", "←/→ ↑↓", "choose", helpKey, "explain field", "ctrl+s", "save and push", "esc", "cancel")
	return box(lines, m.w, m.h-1) + "\n" + m.footer(help)
}
