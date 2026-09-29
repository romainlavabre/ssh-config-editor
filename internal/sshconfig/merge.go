package sshconfig

import (
	"fmt"
	"strings"
)

// Side est l'état d'un bloc dans une version : absent, ou présent avec son texte.
type Side struct {
	Present bool
	Text    string
}

// Conflict est un bloc modifié différemment des deux côtés.
type Conflict struct {
	Key      string
	Base     Side
	Ours     Side
	Theirs   Side
	Resolved bool
	Result   Side
}

// Name renvoie le nom affichable du bloc en conflit.
func (c *Conflict) Name() string {
	if c.Key == "" {
		return "(préambule)"
	}
	if name, ok := strings.CutPrefix(c.Key, "host "); ok {
		return name
	}
	return c.Key
}

// Resolve fixe la version retenue.
func (c *Conflict) Resolve(s Side) {
	c.Resolved = true
	c.Result = s
}

// Merge est le résultat d'une fusion à 3 voies bloc par bloc.
type Merge struct {
	order     []string
	texts     map[string]Side
	conflicts map[string]*Conflict
	Conflicts []*Conflict
}

// Merge3 fusionne trois versions d'un fichier bloc par bloc : un bloc modifié
// d'un seul côté prend la version modifiée, un bloc modifié différemment des
// deux côtés devient un Conflict à trancher.
func Merge3(base, ours, theirs string) *Merge {
	_, bm := index(Parse(base))
	ok, om := index(Parse(ours))
	tk, tm := index(Parse(theirs))

	order := append([]string(nil), ok...)
	last := -1
	for _, k := range tk {
		if i := indexOf(order, k); i >= 0 {
			last = i
			continue
		}
		at := last + 1
		if at == 0 && len(order) > 0 && order[0] == "" {
			at = 1
		}
		order = append(order[:at], append([]string{k}, order[at:]...)...)
		last = at
	}

	m := &Merge{order: order, texts: map[string]Side{}, conflicts: map[string]*Conflict{}}
	for _, k := range order {
		b, o, t := bm[k], om[k], tm[k]
		switch {
		case o == t:
			m.texts[k] = o
		case o == b:
			m.texts[k] = t
		case t == b:
			m.texts[k] = o
		default:
			c := &Conflict{Key: k, Base: b, Ours: o, Theirs: t}
			m.conflicts[k] = c
			m.Conflicts = append(m.Conflicts, c)
		}
	}
	return m
}

// Unresolved compte les conflits non tranchés.
func (m *Merge) Unresolved() int {
	n := 0
	for _, c := range m.Conflicts {
		if !c.Resolved {
			n++
		}
	}
	return n
}

// Render produit le fichier fusionné. Erreur tant qu'un conflit n'est pas tranché.
func (m *Merge) Render() (string, error) {
	var parts []string
	for _, k := range m.order {
		s, ok := m.texts[k]
		if c := m.conflicts[k]; c != nil {
			if !c.Resolved {
				return "", fmt.Errorf("conflit non résolu sur %s", c.Name())
			}
			s, ok = c.Result, true
		}
		if ok && s.Present && strings.TrimSpace(s.Text) != "" {
			parts = append(parts, strings.TrimRight(s.Text, "\n"))
		}
	}
	if len(parts) == 0 {
		return "", nil
	}
	return strings.Join(parts, "\n\n") + "\n", nil
}

func index(f *File) ([]string, map[string]Side) {
	var keys []string
	m := map[string]Side{}
	if t := f.Preamble.Text(); t != "" {
		keys = append(keys, "")
		m[""] = Side{Present: true, Text: t}
	}
	seen := map[string]int{}
	for _, b := range f.Blocks {
		k := b.Kind() + " " + b.Name()
		seen[k]++
		if n := seen[k]; n > 1 {
			k = fmt.Sprintf("%s #%d", k, n)
		}
		keys = append(keys, k)
		m[k] = Side{Present: true, Text: b.Text()}
	}
	return keys, m
}

func indexOf(s []string, v string) int {
	for i, x := range s {
		if x == v {
			return i
		}
	}
	return -1
}
