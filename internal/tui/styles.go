package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

var (
	cAccent = lipgloss.AdaptiveColor{Light: "#5B3FD9", Dark: "#B4A1FF"}
	cMuted  = lipgloss.AdaptiveColor{Light: "#6E6E78", Dark: "#8A8A96"}
	cFaint  = lipgloss.AdaptiveColor{Light: "#C4C4CC", Dark: "#44444E"}
	cOK     = lipgloss.AdaptiveColor{Light: "#1E7F4F", Dark: "#5FD39A"}
	cWarn   = lipgloss.AdaptiveColor{Light: "#9A5800", Dark: "#F2B45A"}
	cErr    = lipgloss.AdaptiveColor{Light: "#C0322B", Dark: "#FF7A70"}
	cSelBg  = lipgloss.AdaptiveColor{Light: "#E9E4FF", Dark: "#2F2850"}

	sTitle  = lipgloss.NewStyle().Bold(true).Foreground(cAccent)
	sBold   = lipgloss.NewStyle().Bold(true)
	sMuted  = lipgloss.NewStyle().Foreground(cMuted)
	sOK     = lipgloss.NewStyle().Foreground(cOK)
	sWarn   = lipgloss.NewStyle().Foreground(cWarn)
	sErr    = lipgloss.NewStyle().Foreground(cErr)
	sKey    = lipgloss.NewStyle().Foreground(cAccent).Bold(true)
	sAccent = lipgloss.NewStyle().Foreground(cAccent)
	sSel    = lipgloss.NewStyle().Background(cSelBg).Bold(true)

	sPane      = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(cFaint).Padding(0, 1)
	sPaneFocus = sPane.BorderForeground(cAccent)
	sDialog    = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(cAccent).Padding(1, 2)
)

// helpLine renders "key action · key action".
func helpLine(pairs ...string) string {
	var parts []string
	for i := 0; i+1 < len(pairs); i += 2 {
		parts = append(parts, sKey.Render(pairs[i])+" "+sMuted.Render(pairs[i+1]))
	}
	return strings.Join(parts, sMuted.Render(" · "))
}

// truncate cuts a line (styles included) to w columns.
func truncate(s string, w int) string {
	if w <= 0 {
		return ""
	}
	return ansi.Truncate(s, w, "…")
}

// box renders exactly h lines of w columns.
func box(lines []string, w, h int) string {
	out := make([]string, h)
	for i := 0; i < h; i++ {
		l := ""
		if i < len(lines) {
			l = truncate(lines[i], w)
		}
		if pad := w - lipgloss.Width(l); pad > 0 {
			l += strings.Repeat(" ", pad)
		}
		out[i] = l
	}
	return strings.Join(out, "\n")
}

// pane frames content of w×h inner columns/lines.
func pane(title string, lines []string, w, h int, focus bool) string {
	st := sPane
	if focus {
		st = sPaneFocus
	}
	body := box(lines, w, h)
	rendered := st.Render(body)
	if title == "" {
		return rendered
	}
	// Title embedded in the top border.
	rows := strings.Split(rendered, "\n")
	t := " " + title + " "
	if lipgloss.Width(t) < lipgloss.Width(rows[0])-4 {
		border := lipgloss.NewStyle().Foreground(cFaint)
		if focus {
			border = lipgloss.NewStyle().Foreground(cAccent)
		}
		rest := lipgloss.Width(rows[0]) - 3 - lipgloss.Width(t)
		rows[0] = border.Render("╭─") + sBold.Render(t) + border.Render(strings.Repeat("─", rest)+"╮")
	}
	return strings.Join(rows, "\n")
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
