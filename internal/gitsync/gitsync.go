// Package gitsync synchronise un dépôt de configurations : commit, fetch,
// fusion Host par Host, push. Il passe par le binaire git pour réutiliser les
// clés et les identifiants de l'utilisateur.
package gitsync

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/romainlavabre/ssh-config-editor/internal/sshconfig"
)

// Repo est un clone local.
type Repo struct {
	Dir    string
	URL    string
	Branch string
}

// Status résume l'écart avec le distant.
type Status struct {
	Ahead    int
	Behind   int
	Dirty    bool
	Merging  bool
	NoRemote bool // la branche n'existe pas encore sur le distant
}

// FileConflict regroupe les conflits d'un fichier.
type FileConflict struct {
	Path  string // relatif au dépôt
	Merge *sshconfig.Merge
}

// Result est l'issue d'une synchronisation.
type Result struct {
	Conflicts []FileConflict
	Offline   bool   // distant injoignable : les commits restent en local
	Warning   string // détail quand Offline
}

// GitError porte la sortie d'erreur de git.
type GitError struct {
	Args   []string
	Stderr string
	Err    error
}

func (e *GitError) Error() string {
	msg := strings.TrimSpace(e.Stderr)
	if msg == "" {
		msg = e.Err.Error()
	}
	return fmt.Sprintf("git %s : %s", e.Args[0], msg)
}

func (e *GitError) Unwrap() error { return e.Err }

var (
	identityOnce sync.Once
	identityEnv  []string
)

// fallbackIdentity fournit une identité quand git n'en a aucune, sinon
// commit et merge échouent sur un poste jamais configuré.
func fallbackIdentity() []string {
	identityOnce.Do(func() {
		if os.Getenv("GIT_COMMITTER_EMAIL") != "" {
			return
		}
		cmd := exec.Command("git", "config", "--get", "user.email")
		cmd.Dir = os.TempDir()
		if out, _ := cmd.Output(); strings.TrimSpace(string(out)) != "" {
			return
		}
		name, host := author()
		email := name + "@" + host
		identityEnv = []string{
			"GIT_AUTHOR_NAME=" + name, "GIT_AUTHOR_EMAIL=" + email,
			"GIT_COMMITTER_NAME=" + name, "GIT_COMMITTER_EMAIL=" + email,
		}
	})
	return identityEnv
}

func env() []string {
	e := append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "LC_ALL=C", "GIT_MERGE_AUTOEDIT=no")
	if os.Getenv("GIT_SSH_COMMAND") == "" {
		// Jamais de demande de mot de passe : elle bloquerait l'interface.
		e = append(e, "GIT_SSH_COMMAND=ssh -o BatchMode=yes")
	}
	return append(e, fallbackIdentity()...)
}

func run(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = env()
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return out.String(), &GitError{Args: args, Stderr: errb.String(), Err: err}
	}
	return out.String(), nil
}

func (r Repo) git(args ...string) (string, error) { return run(r.Dir, args...) }

func (r Repo) ok(args ...string) bool {
	_, err := r.git(args...)
	return err == nil
}

// Clone récupère le dépôt et se place sur la branche, même si le distant est vide.
func Clone(url, dir, branch string) (Repo, error) {
	r := Repo{Dir: dir, URL: url, Branch: branch}
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return r, err
	}
	if _, err := run(filepath.Dir(dir), "clone", "--quiet", url, dir); err != nil {
		return r, err
	}
	var err error
	switch {
	case r.hasRemoteBranch():
		_, err = r.git("checkout", "--quiet", "-B", branch, "--track", "origin/"+branch)
	case !r.hasHead():
		_, err = r.git("symbolic-ref", "HEAD", "refs/heads/"+branch)
	default:
		_, err = r.git("checkout", "--quiet", "-B", branch)
	}
	return r, err
}

func (r Repo) remoteRef() string { return "refs/remotes/origin/" + r.Branch }

func (r Repo) hasRemoteBranch() bool {
	return r.ok("rev-parse", "--verify", "--quiet", r.remoteRef())
}

func (r Repo) hasHead() bool { return r.ok("rev-parse", "--verify", "--quiet", "HEAD") }

// Merging indique une fusion en attente de résolution.
func (r Repo) Merging() bool { return r.ok("rev-parse", "--verify", "--quiet", "MERGE_HEAD") }

// Status calcule l'écart local/distant sans toucher au réseau.
func (r Repo) Status() (Status, error) {
	var s Status
	out, err := r.git("status", "--porcelain")
	if err != nil {
		return s, err
	}
	s.Dirty = strings.TrimSpace(out) != ""
	s.Merging = r.Merging()
	if !r.hasHead() {
		s.NoRemote = !r.hasRemoteBranch()
		return s, nil
	}
	if !r.hasRemoteBranch() {
		s.NoRemote = true
		out, err := r.git("rev-list", "--count", "HEAD")
		if err != nil {
			return s, err
		}
		s.Ahead, _ = strconv.Atoi(strings.TrimSpace(out))
		return s, nil
	}
	out, err = r.git("rev-list", "--left-right", "--count", "HEAD..."+r.remoteRef())
	if err != nil {
		return s, err
	}
	f := strings.Fields(out)
	if len(f) == 2 {
		s.Ahead, _ = strconv.Atoi(f[0])
		s.Behind, _ = strconv.Atoi(f[1])
	}
	return s, nil
}

// Sync commite les modifications locales, récupère le distant, fusionne Host
// par Host puis pousse. Si un même Host a été modifié des deux côtés, la fusion
// reste en cours et Result.Conflicts liste ce qu'il faut trancher : rien n'est
// poussé tant que Resolve n'a pas été appelé.
func (r Repo) Sync(message string) (Result, error) {
	if r.Merging() {
		conflicts, err := r.PendingConflicts()
		if err != nil || len(conflicts) > 0 {
			return Result{Conflicts: conflicts}, err
		}
		if _, err := r.commit("--no-edit"); err != nil {
			return Result{}, err
		}
	}
	if err := r.commitAll(message); err != nil {
		return Result{}, err
	}
	for attempt := 0; attempt < 3; attempt++ {
		if _, err := r.git("fetch", "--quiet", "--prune", "origin"); err != nil {
			return Result{Offline: true, Warning: err.Error()}, nil
		}
		if r.hasRemoteBranch() {
			res, err := r.merge()
			if err != nil || len(res.Conflicts) > 0 {
				return res, err
			}
		}
		st, err := r.Status()
		if err != nil {
			return Result{}, err
		}
		if st.Ahead == 0 {
			return Result{}, nil
		}
		_, err = r.git("push", "--quiet", "origin", "HEAD:refs/heads/"+r.Branch)
		if err == nil {
			// Aligne la référence distante même si le refspec de fetch ne la couvre pas.
			_, _ = r.git("update-ref", r.remoteRef(), "HEAD")
			return Result{}, nil
		}
		if !isRejected(err) {
			return Result{Offline: true, Warning: err.Error()}, nil
		}
	}
	return Result{}, errors.New("push refusé trois fois de suite : le dépôt bouge trop vite, réessayez")
}

func isRejected(err error) bool {
	var ge *GitError
	if !errors.As(err, &ge) {
		return false
	}
	s := ge.Stderr
	return strings.Contains(s, "rejected") || strings.Contains(s, "non-fast-forward") || strings.Contains(s, "fetch first")
}

func (r Repo) merge() (Result, error) {
	if !r.hasHead() {
		_, err := r.git("checkout", "--quiet", "-B", r.Branch, "--track", "origin/"+r.Branch)
		return Result{}, err
	}
	_, err := r.git("merge", "--quiet", "--no-commit", "--allow-unrelated-histories", "origin/"+r.Branch)
	if err == nil {
		if r.Merging() {
			_, err = r.commit("--no-edit")
		}
		return Result{}, err
	}
	if !r.Merging() {
		return Result{}, err
	}
	conflicts, err := r.autoResolve()
	if err != nil || len(conflicts) > 0 {
		return Result{Conflicts: conflicts}, err
	}
	_, err = r.commit("--no-edit")
	return Result{}, err
}

func (r Repo) unmerged() ([]string, error) {
	out, err := r.git("diff", "--name-only", "--diff-filter=U")
	if err != nil {
		return nil, err
	}
	var files []string
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			files = append(files, l)
		}
	}
	return files, nil
}

// autoResolve rejoue la fusion Host par Host sur chaque fichier en conflit
// texte : un Host modifié d'un seul côté prend cette version, seuls les Host
// modifiés des deux côtés remontent.
func (r Repo) autoResolve() ([]FileConflict, error) {
	files, err := r.unmerged()
	if err != nil {
		return nil, err
	}
	var conflicts []FileConflict
	for _, f := range files {
		if !strings.HasSuffix(f, ".conf") {
			// Hors configuration (README…) : la version distante gagne.
			if _, err := r.git("checkout", "--theirs", "--", f); err != nil {
				_, err = r.git("rm", "--quiet", "--", f)
				if err != nil {
					return nil, err
				}
				continue
			}
			if _, err := r.git("add", "--", f); err != nil {
				return nil, err
			}
			continue
		}
		m := r.merge3(f)
		if len(m.Conflicts) > 0 {
			conflicts = append(conflicts, FileConflict{Path: f, Merge: m})
			continue
		}
		if err := r.writeResolved(f, m); err != nil {
			return nil, err
		}
	}
	return conflicts, nil
}

func (r Repo) merge3(path string) *sshconfig.Merge {
	show := func(stage int) string {
		out, err := r.git("show", fmt.Sprintf(":%d:%s", stage, path))
		if err != nil {
			return ""
		}
		return out
	}
	return sshconfig.Merge3(show(1), show(2), show(3))
}

func (r Repo) writeResolved(path string, m *sshconfig.Merge) error {
	content, err := m.Render()
	if err != nil {
		return err
	}
	abs := filepath.Join(r.Dir, path)
	if content == "" {
		if err := os.Remove(abs); err != nil && !os.IsNotExist(err) {
			return err
		}
		_, err = r.git("rm", "--quiet", "--cached", "--ignore-unmatch", "--", path)
		return err
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		return err
	}
	_, err = r.git("add", "--", path)
	return err
}

// PendingConflicts recalcule les conflits d'une fusion en cours.
func (r Repo) PendingConflicts() ([]FileConflict, error) {
	if !r.Merging() {
		return nil, nil
	}
	return r.autoResolve()
}

// Resolve écrit les versions tranchées et termine la fusion. Il reste à
// appeler Sync pour pousser.
func (r Repo) Resolve(files []FileConflict) error {
	for _, fc := range files {
		if err := r.writeResolved(fc.Path, fc.Merge); err != nil {
			return err
		}
	}
	left, err := r.unmerged()
	if err != nil {
		return err
	}
	if len(left) > 0 {
		return fmt.Errorf("fichiers encore en conflit : %s", strings.Join(left, ", "))
	}
	_, err = r.commit("--no-edit")
	return err
}

// AbortMerge abandonne la fusion en cours ; les commits locaux restent à pousser.
func (r Repo) AbortMerge() error {
	_, err := r.git("merge", "--abort")
	return err
}

func (r Repo) commitAll(message string) error {
	if _, err := r.git("add", "--all"); err != nil {
		return err
	}
	if r.ok("diff", "--cached", "--quiet") {
		return nil
	}
	if message == "" {
		message = "ssh-config-editor: modifications locales"
	}
	_, err := r.commit("-m", message)
	return err
}

func (r Repo) commit(args ...string) (string, error) {
	return r.git(append([]string{"commit", "--quiet"}, args...)...)
}

func author() (string, string) {
	name := "ssh-config-editor"
	if u, err := user.Current(); err == nil && u.Username != "" {
		name = u.Username
	}
	host, _ := os.Hostname()
	if host == "" {
		host = "localhost"
	}
	return name, host
}

// Author renvoie "utilisateur@machine" pour les messages de commit.
func Author() string {
	name, host := author()
	return name + "@" + host
}
