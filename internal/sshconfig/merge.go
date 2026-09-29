package sshconfig

import (
	"fmt"
	"strings"
)

// Side is the state of a block in one version: absent, or present with its text.
type Side struct {
	Present bool
	Text    string
}

// Conflict is a block changed differently on both sides.
type Conflict struct {
	Key      string
	Base     Side
	Ours     Side
	Theirs   Side
	Resolved bool
	Result   Side
}

// Name returns the display name of the conflicting block.
func (c *Conflict) Name() string {
	if c.Key == "" {
		return "(préambule)"
	}
	if name, ok := strings.CutPrefix(c.Key, "host "); ok {
		return name
	}
	return c.Key
}

// Resolve sets the chosen version.
func (c *Conflict) Resolve(s Side) {
	c.Resolved = true
	c.Result = s
}

// Merge is the result of a block-by-block three-way merge.
type Merge struct {
	order     []string
	texts     map[string]Side
	conflicts map[string]*Conflict
	Conflicts []*Conflict
}

// Merge3 merges three versions of a file block by block: a block changed on
// one side only takes the changed version, a block changed differently on
// both sides becomes a Conflict to resolve.
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

// Unresolved counts the conflicts not yet resolved.
func (m *Merge) Unresolved() int {
	n := 0
	for _, c := range m.Conflicts {
		if !c.Resolved {
			n++
		}
	}
	return n
}

// Render produces the merged file. Fails while a conflict is unresolved.
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
