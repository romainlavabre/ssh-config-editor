package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// choice is a dialog option; a nil run just closes the dialog.
type choice struct {
	key   string
	label string
	run   func(m *Model) tea.Cmd
}

// dialog is a modal window: a confirmation or a pick from a list.
type dialog struct {
	title   string
	body    string
	choices []choice
	cursor  int
	danger  bool
}

func (m *Model) updateDialog(k tea.KeyMsg) tea.Cmd {
	d := m.dialog
	pick := func(i int) tea.Cmd {
		m.dialog = nil
		if run := d.choices[i].run; run != nil {
			return run(m)
		}
		return nil
	}
	switch k.String() {
	case "esc", "q":
		m.dialog = nil
		return nil
	case "up", "k", "shift+tab", "left":
		d.cursor = (d.cursor - 1 + len(d.choices)) % len(d.choices)
	case "down", "j", "tab", "right":
		d.cursor = (d.cursor + 1) % len(d.choices)
	case "enter":
		return pick(d.cursor)
	default:
		for i, c := range d.choices {
			if c.key != "" && c.key == k.String() {
				return pick(i)
			}
		}
	}
	return nil
}

func (d *dialog) view(w, h int) string {
	width := min(64, w-8)
	title := sTitle.Render(d.title)
	if d.danger {
		title = sErr.Bold(true).Render(d.title)
	}
	lines := []string{title}
	if d.body != "" {
		lines = append(lines, "", lipgloss.NewStyle().Width(width).Render(d.body))
	}
	lines = append(lines, "")
	for i, c := range d.choices {
		label := c.label
		if c.key != "" {
			label = sKey.Render(c.key) + "  " + label
		}
		if i == d.cursor {
			lines = append(lines, sAccent.Render("› ")+sBold.Render(label))
		} else {
			lines = append(lines, "  "+label)
		}
	}
	lines = append(lines, "", helpLine("↑↓", "choose", "enter", "confirm", "esc", "cancel"))
	st := sDialog
	if d.danger {
		st = st.BorderForeground(cErr)
	}
	return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, st.Render(strings.Join(lines, "\n")))
}
