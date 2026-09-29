package tui

import (
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/romainlavabre/ssh-config-editor/internal/store"
)

const (
	rURL = iota
	rName
	rBranch
)

type reposView struct {
	cursor      int
	adding      bool
	inputs      [3]textinput.Model
	focus       int
	nameTouched bool
	err         string
	cloning     string
}

func (m *Model) openRepos() {
	if m.reposV == nil {
		m.reposV = &reposView{}
	}
	m.reposV.adding = false
	m.reposV.err = ""
	m.reposV.resize(m.w)
	m.screen = scrRepos
}

func (v *reposView) startAdding() tea.Cmd {
	ph := []string{"git@github.com:team/ssh-config.git", "team", store.DefaultBranch}
	for i := range v.inputs {
		ti := textinput.New()
		ti.Prompt = ""
		ti.Placeholder = ph[i]
		v.inputs[i] = ti
	}
	v.inputs[rBranch].SetValue(store.DefaultBranch)
	v.adding, v.focus, v.nameTouched, v.err = true, rURL, false, ""
	return v.inputs[rURL].Focus()
}

func (v *reposView) resize(w int) {
	for i := range v.inputs {
		v.inputs[i].Width = max(20, min(70, w-24))
	}
}

func (v *reposView) forward(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	v.inputs[v.focus], cmd = v.inputs[v.focus].Update(msg)
	return cmd
}

func (v *reposView) setFocus(i int) tea.Cmd {
	v.focus = (i + len(v.inputs)) % len(v.inputs)
	for j := range v.inputs {
		v.inputs[j].Blur()
	}
	return v.inputs[v.focus].Focus()
}

// nameFromURL derives a repository name from the organization in its URL:
// git@github.com:fairfair/ssh-config.git → fairfair,
// https://gitlab.com/marea/infra/ssh.git → marea.
// A local path has no organization: its directory name is used (/srv/team.git → team).
func nameFromURL(url string) string {
	url = strings.TrimSuffix(strings.TrimSpace(url), "/")
	url = strings.TrimSuffix(url, ".git")
	var repoPath string
	switch {
	case strings.Contains(url, "://"):
		// scheme://[user@]host[:port]/org/repo; file:// is a local path.
		rest := url[strings.Index(url, "://")+3:]
		if strings.HasPrefix(url, "file://") {
			return path.Base(rest)
		}
		if i := strings.Index(rest, "/"); i >= 0 {
			repoPath = rest[i+1:]
		}
	case !strings.HasPrefix(url, "/") && !strings.HasPrefix(url, ".") && strings.Contains(url, ":"):
		// scp-like: [user@]host:org/repo
		repoPath = url[strings.Index(url, ":")+1:]
	default:
		if name := path.Base(url); name != "." && name != "/" {
			return name
		}
		return ""
	}
	parts := strings.Split(strings.Trim(repoPath, "/"), "/")
	if len(parts) >= 2 {
		return parts[0]
	}
	return parts[0] // host:repo without organization
}

func (m *Model) updateRepos(k tea.KeyMsg) tea.Cmd {
	v := m.reposV
	if v.cloning != "" {
		return nil
	}
	if v.adding {
		switch k.String() {
		case "esc":
			v.adding = false
			return nil
		case "tab", "down":
			return v.setFocus(v.focus + 1)
		case "shift+tab", "up":
			return v.setFocus(v.focus - 1)
		case "enter":
			if v.focus < rBranch {
				return v.setFocus(v.focus + 1)
			}
			return m.submitRepo()
		case "ctrl+s":
			return m.submitRepo()
		}
		cmd := v.forward(k)
		switch v.focus {
		case rURL:
			if !v.nameTouched {
				v.inputs[rName].SetValue(nameFromURL(v.inputs[rURL].Value()))
			}
		case rName:
			v.nameTouched = true
		}
		v.err = ""
		return cmd
	}

	repos := m.st.Repos()
	switch k.String() {
	case "esc", "q", "R":
		m.screen = scrMain
		m.rebuildRows()
	case "up", "k":
		v.cursor = max(0, v.cursor-1)
	case "down", "j":
		v.cursor = min(max(0, len(repos)-1), v.cursor+1)
	case "a", "n":
		return v.startAdding()
	case "K", "shift+up", "J", "shift+down":
		if len(repos) == 0 {
			return nil
		}
		d := 1
		if k.String() == "K" || k.String() == "shift+up" {
			d = -1
		}
		if err := m.st.MoveRepo(repos[v.cursor].Name, d); err != nil {
			return m.notify(toastErr, err.Error())
		}
		v.cursor = max(0, min(len(repos)-1, v.cursor+d))
		m.rebuildRows()
		return m.notify(toastOK, "priority updated in ~/.ssh/config")
	case "s":
		if len(repos) > 0 {
			return m.requestSync(repos[v.cursor], "")
		}
	case "x", "delete":
		if len(repos) > 0 {
			m.openRemoveRepoDialog(repos[v.cursor], false)
		}
	}
	return nil
}

func (m *Model) submitRepo() tea.Cmd {
	v := m.reposV
	r, err := m.st.CheckRepo(store.Repo{
		URL:    v.inputs[rURL].Value(),
		Name:   v.inputs[rName].Value(),
		Branch: v.inputs[rBranch].Value(),
	})
	if err != nil {
		v.err = err.Error()
		return nil
	}
	v.cloning = r.Name
	paths := m.st.Paths
	return func() tea.Msg { return cloneDoneMsg{repo: r, err: store.CloneRepo(paths, r)} }
}

func (m *Model) onCloneDone(msg cloneDoneMsg) tea.Cmd {
	v := m.reposV
	v.cloning = ""
	if msg.err != nil {
		v.err = "cannot clone: " + firstLine(msg.err.Error())
		return nil
	}
	if err := m.st.RegisterRepo(msg.repo); err != nil {
		v.err = err.Error()
		return nil
	}
	v.adding = false
	v.cursor = len(m.st.Repos()) - 1
	m.rebuildRows()
	n := len(m.st.HostsOf(m.st.Source(msg.repo.Name)))
	return tea.Batch(
		m.notify(toastOK, fmt.Sprintf("%s cloned · %d %s · added to ~/.ssh/config", msg.repo.Name, n, plural(n, "Host", "Hosts"))),
		m.requestSync(m.st.Source(msg.repo.Name), ""),
	)
}

func (m *Model) openRemoveRepoDialog(src *store.Source, force bool) {
	body := "The local clone is deleted and its Hosts disappear from ~/.ssh/config. The remote repository is left untouched."
	if force {
		body = "Some changes were not pushed: they will be lost."
	}
	m.dialog = &dialog{
		title:  "Remove repository " + src.Name + "?",
		body:   body,
		danger: true,
		choices: []choice{
			{key: "n", label: "no, keep it"},
			{key: "y", label: "yes, remove", run: func(m *Model) tea.Cmd {
				err := m.st.RemoveRepo(src.Name, force)
				if errors.Is(err, store.ErrUnpushed) {
					m.openRemoveRepoDialog(src, true)
					return nil
				}
				if err != nil {
					return m.notify(toastErr, err.Error())
				}
				delete(m.repos, src.Name)
				m.reposV.cursor = max(0, m.reposV.cursor-1)
				m.rebuildRows()
				return m.notify(toastOK, src.Name+" removed")
			}},
		},
	}
}

func (m *Model) viewRepos() string {
	v := m.reposV
	lines := []string{m.titleBar(sMuted.Render("repositories")), ""}
	lines = append(lines, " "+sBold.Render("Include order in ~/.ssh/config")+sMuted.Render("  (in ssh, the first value wins)"), "")
	lines = append(lines, "   "+sMuted.Render("0.")+" "+sBold.Render("local")+"  "+sMuted.Render(m.st.Paths.LocalConf()+" · personal overrides"))
	repos := m.st.Repos()
	for i, src := range repos {
		rs := m.repo(src.Name)
		state := m.repoBadge(src.Name)
		switch {
		case rs.err != "":
			state += " " + sErr.Render(firstLine(rs.err))
		case rs.status.Ahead > 0:
			state += " " + sWarn.Render("not pushed")
		}
		line := fmt.Sprintf("%s %s  %s  %s", sMuted.Render(fmt.Sprintf("%d.", i+1)), sBold.Render(src.Name),
			sMuted.Render(src.Repo.URL+" ("+src.Repo.Branch+")"), state)
		if i == v.cursor && !v.adding {
			lines = append(lines, sAccent.Render(" › ")+line)
		} else {
			lines = append(lines, "   "+line)
		}
	}
	if len(repos) == 0 {
		lines = append(lines, "", "   "+sMuted.Render("No repository. Create an empty git repository (GitHub, GitLab…) then add it with a."))
	}
	lines = append(lines, "   "+sMuted.Render(fmt.Sprintf("%d. ~/.ssh/config · everything else, read last", len(repos)+1)))

	if v.adding {
		lines = append(lines, "", " "+sTitle.Render("Add a repository"), "")
		lbl := []string{"Git URL", "Name", "Branch"}
		for i := range v.inputs {
			l := fmt.Sprintf("%-10s", lbl[i])
			if v.focus == i {
				l = sAccent.Render("› ") + sBold.Render(l)
			} else {
				l = "  " + sMuted.Render(l)
			}
			lines = append(lines, " "+l+v.inputs[i].View())
		}
		lines = append(lines, "", " "+sMuted.Render("An empty repository is fine: the first saved Host fills it."))
	}
	if v.cloning != "" {
		lines = append(lines, "", " "+sWarn.Render("⟳ cloning "+v.cloning+"…"))
	}
	if v.err != "" {
		lines = append(lines, "", " "+sErr.Render("✗ "+v.err))
	}

	help := helpLine("a", "add", "x", "remove", "K/J", "priority", "s", "sync", "esc", "back")
	if v.adding {
		help = helpLine("tab", "field", "enter", "clone", "esc", "cancel")
	}
	return box(lines, m.w, m.h-1) + "\n" + m.footer(help)
}
