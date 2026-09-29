package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/romainlavabre/ssh-config-editor/internal/gitsync"
	"github.com/romainlavabre/ssh-config-editor/internal/sshconfig"
)

type conflictItem struct {
	file int
	c    *sshconfig.Conflict
}

type conflictView struct {
	repo    string
	files   []gitsync.FileConflict
	items   []conflictItem
	cur     int
	editing bool
	editor  textarea.Model
	err     string
	w, h    int
}

func (m *Model) openConflict(repo string) {
	rs := m.repo(repo)
	v := &conflictView{repo: repo, files: rs.conflicts}
	for i, fc := range v.files {
		for _, c := range fc.Merge.Conflicts {
			v.items = append(v.items, conflictItem{file: i, c: c})
		}
	}
	v.editor = textarea.New()
	v.editor.ShowLineNumbers = false
	v.resize(m.w, m.h)
	m.conflict = v
	m.screen = scrConflict
}

func (v *conflictView) resize(w, h int) {
	v.w, v.h = w, h
	v.editor.SetWidth(max(20, w-6))
	v.editor.SetHeight(max(5, h-12))
}

func (v *conflictView) item() conflictItem { return v.items[v.cur] }

func (m *Model) updateConflict(k tea.KeyMsg) tea.Cmd {
	v := m.conflict
	if v.editing {
		switch k.String() {
		case "esc":
			v.editing = false
			v.editor.Blur()
			return nil
		case "ctrl+s":
			text := strings.TrimSpace(v.editor.Value())
			v.item().c.Resolve(sshconfig.Side{Present: text != "", Text: text})
			v.editing = false
			v.editor.Blur()
			v.next()
			return nil
		}
		var cmd tea.Cmd
		v.editor, cmd = v.editor.Update(k)
		return cmd
	}

	it := v.item()
	switch k.String() {
	case "esc", "q":
		m.screen = scrMain
		return m.notify(toastWarn, "conflict on "+v.repo+" pending: nothing is pushed · press C to resume")
	case "left", "h", "p", "shift+tab":
		v.cur = (v.cur - 1 + len(v.items)) % len(v.items)
	case "right", "l", "n", "tab":
		v.cur = (v.cur + 1) % len(v.items)
	case "m":
		it.c.Resolve(it.c.Ours)
		v.next()
	case "t":
		it.c.Resolve(it.c.Theirs)
		v.next()
	case "e":
		text := it.c.Ours.Text
		switch {
		case it.c.Resolved:
			text = it.c.Result.Text
		case !it.c.Ours.Present:
			text = it.c.Theirs.Text
		}
		v.editor.SetValue(text)
		v.editing = true
		return v.editor.Focus()
	case "enter":
		for i, x := range v.items {
			if !x.c.Resolved {
				v.cur = i
				v.err = "resolve every Host before applying"
				return nil
			}
		}
		return m.applyResolution()
	case "a":
		m.dialog = &dialog{
			title:  "Abort the merge?",
			body:   "The repository goes back to your version, your commits remain to be pushed. The merge will be retried on the next sync.",
			danger: true,
			choices: []choice{
				{key: "n", label: "no"},
				{key: "y", label: "yes, abort", run: func(m *Model) tea.Cmd {
					src := m.st.Source(v.repo)
					if err := src.Git().AbortMerge(); err != nil {
						return m.notify(toastErr, err.Error())
					}
					m.repo(v.repo).conflicts = nil
					m.screen = scrMain
					m.reload()
					return m.notify(toastWarn, "merge aborted · "+v.repo+" remains to be pushed")
				}},
			},
		}
	}
	return nil
}

// next jumps to the next unresolved Host.
func (v *conflictView) next() {
	v.err = ""
	for i := 1; i <= len(v.items); i++ {
		j := (v.cur + i) % len(v.items)
		if !v.items[j].c.Resolved {
			v.cur = j
			return
		}
	}
}

func (m *Model) applyResolution() tea.Cmd {
	v := m.conflict
	src := m.st.Source(v.repo)
	if src == nil {
		return m.notify(toastErr, "repository not found: "+v.repo)
	}
	rs := m.repo(v.repo)
	rs.conflicts = nil
	rs.syncing = true
	m.screen = scrMain
	g, files, name := src.Git(), v.files, v.repo
	return tea.Batch(
		m.notify(toastInfo, "resolution applied · pushing…"),
		func() tea.Msg {
			if err := g.Resolve(files); err != nil {
				status, _ := g.Status()
				return syncDoneMsg{repo: name, err: err, status: status}
			}
			res, err := g.Sync("")
			status, _ := g.Status()
			return syncDoneMsg{repo: name, res: res, err: err, status: status}
		},
	)
}

func (m *Model) viewConflict() string {
	v := m.conflict
	it := v.item()
	fc := v.files[it.file]
	resolved := 0
	for _, x := range v.items {
		if x.c.Resolved {
			resolved++
		}
	}
	lines := []string{
		m.titleBar(sErr.Render(fmt.Sprintf("conflict on %s · %d/%d resolved", v.repo, resolved, len(v.items)))),
		"",
		fmt.Sprintf(" %s %s  %s", sWarn.Render("!"), sBold.Render(it.c.Name()), sMuted.Render(fmt.Sprintf("%s · Host %d/%d", fc.Path, v.cur+1, len(v.items)))),
		" " + sMuted.Render("You and a teammate both changed this Host. Nothing is pushed until it is resolved."),
		"",
	}

	if v.editing {
		lines = append(lines, " "+sBold.Render("Final version")+sMuted.Render("  (empty = delete the Host)"))
		lines = append(lines, strings.Split(lipgloss.NewStyle().PaddingLeft(1).Render(v.editor.View()), "\n")...)
		help := helpLine("ctrl+s", "keep this version", "esc", "cancel editing")
		return box(lines, m.w, m.h-1) + "\n" + m.footer(help)
	}

	colW := max(20, (m.w-10)/2)
	colH := max(4, m.h-len(lines)-6)
	ours := sideLines(it.c.Ours, it.c.Theirs)
	theirs := sideLines(it.c.Theirs, it.c.Ours)
	oursFocus := it.c.Resolved && it.c.Result == it.c.Ours
	theirsFocus := it.c.Resolved && it.c.Result == it.c.Theirs
	cols := lipgloss.JoinHorizontal(lipgloss.Top,
		pane("My version (m)", ours, colW, colH, oursFocus),
		" ",
		pane("Their version (t)", theirs, colW, colH, theirsFocus),
	)
	lines = append(lines, strings.Split(lipgloss.NewStyle().PaddingLeft(1).Render(cols), "\n")...)

	state := sMuted.Render("pending")
	if it.c.Resolved {
		switch it.c.Result {
		case it.c.Ours:
			state = sOK.Render("✓ my version")
		case it.c.Theirs:
			state = sOK.Render("✓ their version")
		default:
			state = sOK.Render("✓ edited version")
			if !it.c.Result.Present {
				state = sOK.Render("✓ deleted")
			}
		}
	}
	lines = append(lines, " Choice: "+state)
	if v.err != "" {
		lines = append(lines, " "+sErr.Render("✗ "+v.err))
	}
	help := helpLine("m", "mine", "t", "theirs", "e", "edit", "←/→", "Host", "enter", "apply and push", "a", "abort", "esc", "later")
	return box(lines, m.w, m.h-1) + "\n" + m.footer(help)
}

// sideLines highlights the lines missing from the other version.
func sideLines(s, other sshconfig.Side) []string {
	if !s.Present {
		return []string{sMuted.Render("(Host deleted on this side)")}
	}
	seen := map[string]bool{}
	for _, l := range strings.Split(other.Text, "\n") {
		seen[strings.TrimSpace(l)] = true
	}
	var out []string
	for _, l := range strings.Split(s.Text, "\n") {
		if other.Present && !seen[strings.TrimSpace(l)] {
			out = append(out, sWarn.Render("▌")+sBold.Render(l))
		} else {
			out = append(out, " "+highlight(l))
		}
	}
	return out
}
