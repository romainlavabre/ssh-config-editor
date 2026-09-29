package store

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	beginMarker = "# >>> ssh-config-editor (généré, ne pas éditer) >>>"
	endMarker   = "# <<< ssh-config-editor <<<"
)

// managedBlock construit le bloc d'Include placé en tête de ~/.ssh/config.
// Les surcharges locales passent en premier : en ssh, la première valeur gagne.
func managedBlock(p Paths, repos []Repo) string {
	lines := []string{beginMarker, "Include " + quote(p.LocalConf())}
	for _, r := range repos {
		lines = append(lines, "Include "+quote(filepath.Join(p.RepoDir(r.Name), "*.conf")))
	}
	lines = append(lines, endMarker)
	return strings.Join(lines, "\n")
}

func quote(path string) string {
	if strings.ContainsAny(path, " \t") {
		return `"` + path + `"`
	}
	return path
}

// stripManaged retire le bloc géré (et les lignes vides qui le suivent).
func stripManaged(content string) (string, bool) {
	lines := strings.Split(content, "\n")
	start, end := -1, -1
	for i, l := range lines {
		t := strings.TrimSpace(l)
		if start < 0 && strings.HasPrefix(t, "# >>> ssh-config-editor") {
			start = i
		} else if start >= 0 && strings.HasPrefix(t, "# <<< ssh-config-editor") {
			end = i
			break
		}
	}
	if start < 0 || end < 0 {
		return content, false
	}
	after := end + 1
	for after < len(lines)-1 && strings.TrimSpace(lines[after]) == "" {
		after++
	}
	out := append(append([]string(nil), lines[:start]...), lines[after:]...)
	return strings.Join(out, "\n"), true
}

// composeMain replace le bloc géré en tête du contenu de ~/.ssh/config.
func composeMain(block, rest string) string {
	rest = strings.TrimLeft(rest, "\n")
	if strings.TrimSpace(rest) == "" {
		return block + "\n"
	}
	return block + "\n\n" + rest
}

// Validate fait relire la configuration par ssh lui-même, pour refuser une
// option inconnue avant de l'écrire et de la pousser.
func Validate(content string) error {
	sshBin, err := exec.LookPath("ssh")
	if err != nil {
		return nil
	}
	tmp, err := os.CreateTemp("", "ssh-config-editor-*.conf")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	out, err := exec.Command(sshBin, "-F", tmp.Name(), "-G", "ssh-config-editor-probe").CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(strings.ReplaceAll(string(out), tmp.Name(), "config"))
		return fmt.Errorf("ssh refuse cette configuration : %s", msg)
	}
	return nil
}

// writeFileAtomic écrit via un fichier temporaire en gardant les droits existants.
func writeFileAtomic(path, content string, defaultMode os.FileMode) error {
	// Un ~/.ssh/config en lien symbolique (dotfiles) reste un lien.
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	mode := defaultMode
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm()
	}
	dirMode := os.FileMode(0o755)
	if filepath.Base(filepath.Dir(path)) == ".ssh" {
		dirMode = 0o700
	}
	if err := os.MkdirAll(filepath.Dir(path), dirMode); err != nil {
		return err
	}
	tmp := path + ".ssh-config-editor.tmp"
	if err := os.WriteFile(tmp, []byte(content), mode); err != nil {
		return err
	}
	if err := os.Chmod(tmp, mode); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}
