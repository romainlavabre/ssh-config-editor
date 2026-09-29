package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/romainlavabre/ssh-config-editor/internal/store"
)

type importView struct {
	groups   []store.ImportGroup
	dests    []*store.Source // dests[0] == nil: leave in ~/.ssh/config
	choice   []int
	cursor   int
	expanded map[int]bool
	top      int
}

func (m *Model) openImport() {
	v := &importView{groups: m.st.ImportCandidates(), expanded: map[int]bool{}}
	v.dests = append([]*store.Source{nil}, m.st.Sources[:len(m.st.Sources)-1]...) // local + repositories
	v.choice = make([]int, len(v.groups))
	m.imp = v
	m.screen = scrImport
}

func (v *importView) destLabel(i int) string {
	if v.dests[i] == nil {
		return "laisser"
	}
	return v.dests[i].Label()
}

func (m *Model) updateImport(k tea.KeyMsg) tea.Cmd {
	v := m.imp
	if len(v.groups) == 0 {
		m.screen = scrMain
		return nil
	}
	switch k.String() {
	case "esc", "q":
		m.screen = scrMain
	case "up", "k":
		v.cursor = max(0, v.cursor-1)
	case "down", "j":
		v.cursor = min(len(v.groups)-1, v.cursor+1)
	case "left", "h":
		v.choice[v.cursor] = (v.choice[v.cursor] - 1 + len(v.dests)) % len(v.dests)
	case "right", "l", "tab":
		v.choice[v.cursor] = (v.choice[v.cursor] + 1) % len(v.dests)
	case " ":
		v.expanded[v.cursor] = !v.expanded[v.cursor]
	case "a":
		for i := range v.choice {
			v.choice[i] = v.choice[v.cursor]
		}
	case "R":
		m.openRepos()
	case "enter", "ctrl+s":
		m.confirmImport()
	}
	return nil
}

func (v *importView) summary() (map[string]int, int) {
	counts := map[string]int{}
	left := 0
	for i, g := range v.groups {
		if v.dests[v.choice[i]] == nil {
			left += len(g.Hosts)
			continue
		}
		counts[v.destLabel(v.choice[i])] += len(g.Hosts)
	}
	return counts, left
}

func (m *Model) confirmImport() {
	v := m.imp
	counts, left := v.summary()
	if len(counts) == 0 {
		m.dialog = &dialog{title: "Rien à déplacer", body: "Choisissez une destination avec ←/→ pour au moins un groupe.", choices: []choice{{label: "ok"}}}
		return
	}
	var parts []string
	for _, d := range v.dests[1:] {
		if n := counts[d.Label()]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d → %s", n, d.Label()))
		}
	}
	body := strings.Join(parts, ", ") + fmt.Sprintf(". %d restent dans ~/.ssh/config.", left) +
		"\n\n~/.ssh/config est sauvegardé avant d'être réécrit. Les dépôts sont poussés aussitôt."
	m.dialog = &dialog{
		title: "Importer ?",
		body:  body,
		choices: []choice{
			{key: "y", label: "importer", run: func(m *Model) tea.Cmd {
				assign := map[string]*store.Source{}
				for i, g := range v.groups {
					assign[g.Key] = v.dests[v.choice[i]]
				}
				backup, touched, err := m.st.Import(assign)
				if err != nil {
					return m.notify(toastErr, err.Error())
				}
				m.screen = scrMain
				m.rebuildRows()
				return tea.Batch(
					m.notify(toastOK, "import terminé · sauvegarde : "+backup),
					m.syncTouched(touched, commitMessage("importe", fmt.Sprintf("%d Host", total(counts)))),
				)
			}},
			{key: "n", label: "revenir"},
		},
	}
}

func total(counts map[string]int) int {
	n := 0
	for _, c := range counts {
		n += c
	}
	return n
}

func (m *Model) viewImport() string {
	v := m.imp
	lines := []string{m.titleBar(sMuted.Render("import de ~/.ssh/config")), ""}
	if len(v.groups) == 0 {
		lines = append(lines, " Rien à importer : ~/.ssh/config ne contient plus de Host concret.", "", " "+sMuted.Render("une touche pour revenir"))
		return box(lines, m.w, m.h)
	}
	lines = append(lines,
		" Les Host de ~/.ssh/config sont regroupés par préfixe. Pour chaque groupe, choisissez où le ranger :",
		" "+sMuted.Render("un dépôt (partagé), local (perso) ou laisser (reste dans ~/.ssh/config). Host * et Match restent en place."),
	)
	if len(m.st.Repos()) == 0 {
		lines = append(lines, " "+sWarn.Render("Aucun dépôt configuré : R pour en ajouter un avant d'importer."))
	}
	lines = append(lines, "")

	var rows []string
	cursorRow := 0
	for i, g := range v.groups {
		if i == v.cursor {
			cursorRow = len(rows)
		}
		arrow := "▸"
		if v.expanded[i] {
			arrow = "▾"
		}
		dest := v.destLabel(v.choice[i])
		destS := sMuted.Render("‹ " + dest + " ›")
		if v.dests[v.choice[i]] != nil {
			destS = sOK.Render("‹ " + dest + " ›")
		}
		name := g.Key + " " + sMuted.Render(fmt.Sprintf("(%d)", len(g.Hosts)))
		name += strings.Repeat(" ", max(1, 30-lipgloss.Width(name)))
		line := fmt.Sprintf("%s %s  %s", sAccent.Render(arrow), name, destS)
		if v.dests[v.choice[i]] != nil && v.dests[v.choice[i]].Kind == store.KindRepo {
			line += sMuted.Render("  → " + g.Key + ".conf")
		}
		if i == v.cursor {
			rows = append(rows, sAccent.Render(" › ")+line)
		} else {
			rows = append(rows, "   "+line)
		}
		if v.expanded[i] {
			for _, h := range g.Hosts {
				rows = append(rows, "       "+sMuted.Render(h.Name+"  "+h.Block.Get("HostName")))
			}
		}
	}
	avail := max(3, m.h-len(lines)-4)
	if cursorRow < v.top {
		v.top = cursorRow
	}
	if cursorRow >= v.top+avail {
		v.top = cursorRow - avail + 1
	}
	end := min(len(rows), v.top+avail)
	lines = append(lines, rows[v.top:end]...)

	counts, left := v.summary()
	var parts []string
	for _, d := range v.dests[1:] {
		if n := counts[d.Label()]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d → %s", n, d.Label()))
		}
	}
	parts = append(parts, fmt.Sprintf("%d restent", left))
	lines = append(lines, "", " "+sBold.Render(strings.Join(parts, " · ")))

	help := helpLine("←/→", "destination", "space", "voir les Host", "a", "appliquer à tous", "enter", "importer", "R", "dépôts", "esc", "retour")
	return box(lines, m.w, m.h-1) + "\n" + m.footer(help)
}
