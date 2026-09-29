// Package sshconfig lit et réécrit un fichier ssh_config sans perte :
// chaque ligne est conservée telle quelle, seules les lignes modifiées sont réécrites.
package sshconfig

import "strings"

// Option est une directive "Clé valeur".
type Option struct {
	Key   string
	Value string
}

// Block est un bloc Host ou Match, ou le préambule (Header vide).
type Block struct {
	Header string   // ligne brute "Host a b" ; vide pour le préambule
	Lines  []string // lignes brutes qui suivent l'en-tête : options, commentaires, lignes vides
}

// File est un fichier ssh_config découpé en préambule + blocs.
type File struct {
	Preamble     *Block
	Blocks       []*Block
	finalNewline bool
}

// Parse découpe un contenu ssh_config. Parse(c).String() == c pour tout c.
func Parse(content string) *File {
	f := &File{Preamble: &Block{}, finalNewline: true}
	if content == "" {
		return f
	}
	lines := strings.Split(content, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	} else {
		f.finalNewline = false
	}
	cur := f.Preamble
	for _, l := range lines {
		key, _ := SplitLine(l)
		switch strings.ToLower(key) {
		case "host", "match":
			cur = &Block{Header: l}
			f.Blocks = append(f.Blocks, cur)
		default:
			cur.Lines = append(cur.Lines, l)
		}
	}
	return f
}

// String restitue le fichier.
func (f *File) String() string {
	var all []string
	all = append(all, f.Preamble.Lines...)
	for _, b := range f.Blocks {
		all = append(all, b.Header)
		all = append(all, b.Lines...)
	}
	if len(all) == 0 {
		return ""
	}
	s := strings.Join(all, "\n")
	if f.finalNewline {
		s += "\n"
	}
	return s
}

// Hosts renvoie les blocs Host, dans l'ordre du fichier.
func (f *File) Hosts() []*Block {
	var out []*Block
	for _, b := range f.Blocks {
		if b.Kind() == "host" {
			out = append(out, b)
		}
	}
	return out
}

// Find renvoie le premier bloc Host portant ce nom, ou nil.
func (f *File) Find(name string) *Block {
	name = normalize(name)
	for _, b := range f.Blocks {
		if b.Kind() == "host" && b.Name() == name {
			return b
		}
	}
	return nil
}

// Remove retire un bloc du fichier.
func (f *File) Remove(target *Block) bool {
	for i, b := range f.Blocks {
		if b == target {
			f.Blocks = append(f.Blocks[:i], f.Blocks[i+1:]...)
			return true
		}
	}
	return false
}

// Append ajoute un bloc en fin de fichier, séparé du précédent par une ligne vide.
func (f *File) Append(b *Block) {
	prev := f.Preamble
	if len(f.Blocks) > 0 {
		prev = f.Blocks[len(f.Blocks)-1]
	}
	if prev.Header != "" || len(prev.Lines) > 0 {
		if len(prev.Lines) == 0 || strings.TrimSpace(prev.Lines[len(prev.Lines)-1]) != "" {
			prev.Lines = append(prev.Lines, "")
		}
	}
	f.Blocks = append(f.Blocks, b)
	f.finalNewline = true
}

// Kind vaut "host", "match", ou "" pour le préambule.
func (b *Block) Kind() string {
	k, _ := SplitLine(b.Header)
	return strings.ToLower(k)
}

// Name renvoie les motifs de l'en-tête, espaces normalisés.
func (b *Block) Name() string {
	_, v := SplitLine(b.Header)
	return normalize(v)
}

// IsConcrete indique un bloc Host sans joker : un alias sur lequel on peut se connecter.
func (b *Block) IsConcrete() bool {
	if b.Kind() != "host" {
		return false
	}
	return !strings.ContainsAny(b.Name(), "*?!")
}

// SetName renomme un bloc Host.
func (b *Block) SetName(name string) {
	b.Header = "Host " + normalize(name)
}

// Options renvoie les directives du bloc, dans l'ordre.
func (b *Block) Options() []Option {
	var out []Option
	for _, l := range b.Lines {
		if k, v := SplitLine(l); k != "" {
			out = append(out, Option{Key: k, Value: v})
		}
	}
	return out
}

// Get renvoie la première valeur de la clé (insensible à la casse), ou "".
func (b *Block) Get(key string) string {
	for _, o := range b.Options() {
		if strings.EqualFold(o.Key, key) {
			return o.Value
		}
	}
	return ""
}

// SetOptions remplace l'ensemble des directives du bloc par opts. Les lignes
// existantes sont réutilisées clé par clé (dans l'ordre d'apparition), les
// commentaires restent en place, les nouvelles clés sont ajoutées après la
// dernière directive.
func (b *Block) SetOptions(opts []Option) {
	var wanted []Option
	for _, o := range opts {
		if strings.TrimSpace(o.Key) != "" && strings.TrimSpace(o.Value) != "" {
			wanted = append(wanted, Option{Key: strings.TrimSpace(o.Key), Value: strings.TrimSpace(o.Value)})
		}
	}
	ind := b.indent()
	consumed := make([]bool, len(wanted))
	var out []string
	lastOpt := -1
	for _, l := range b.Lines {
		k, v := SplitLine(l)
		if k == "" {
			out = append(out, l)
			continue
		}
		j := -1
		for i, o := range wanted {
			if !consumed[i] && strings.EqualFold(o.Key, k) {
				j = i
				break
			}
		}
		if j < 0 {
			continue
		}
		consumed[j] = true
		if wanted[j].Value != v {
			lead := l[:len(l)-len(strings.TrimLeft(l, " \t"))]
			l = lead + k + " " + wanted[j].Value
		}
		out = append(out, l)
		lastOpt = len(out) - 1
	}
	var extra []string
	for i, o := range wanted {
		if !consumed[i] {
			extra = append(extra, ind+o.Key+" "+o.Value)
		}
	}
	pos := lastOpt + 1
	if lastOpt < 0 {
		pos = len(out)
		for pos > 0 && strings.TrimSpace(out[pos-1]) == "" {
			pos--
		}
	}
	res := make([]string, 0, len(out)+len(extra))
	res = append(res, out[:pos]...)
	res = append(res, extra...)
	res = append(res, out[pos:]...)
	b.Lines = res
}

// Text renvoie le bloc sans les lignes vides finales.
func (b *Block) Text() string {
	var lines []string
	if b.Header != "" {
		lines = append(lines, b.Header)
	}
	lines = append(lines, b.Lines...)
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	for len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
		lines = lines[1:]
	}
	return strings.Join(lines, "\n")
}

// Clone copie le bloc, sans ses lignes vides finales.
func (b *Block) Clone() *Block {
	c := &Block{Header: b.Header, Lines: append([]string(nil), b.Lines...)}
	for len(c.Lines) > 0 && strings.TrimSpace(c.Lines[len(c.Lines)-1]) == "" {
		c.Lines = c.Lines[:len(c.Lines)-1]
	}
	return c
}

// NewHostBlock construit un bloc Host.
func NewHostBlock(name string, opts []Option) *Block {
	b := &Block{}
	b.SetName(name)
	b.SetOptions(opts)
	return b
}

func (b *Block) indent() string {
	for _, l := range b.Lines {
		if k, _ := SplitLine(l); k != "" {
			return l[:len(l)-len(strings.TrimLeft(l, " \t"))]
		}
	}
	return "  "
}

// SplitLine découpe une ligne en clé et valeur ("Key value" ou "Key=value").
// Renvoie "", "" pour une ligne vide ou un commentaire.
func SplitLine(l string) (string, string) {
	t := strings.TrimSpace(l)
	if t == "" || strings.HasPrefix(t, "#") {
		return "", ""
	}
	i := strings.IndexAny(t, " \t=")
	if i < 0 {
		return t, ""
	}
	rest := strings.TrimLeft(t[i:], " \t")
	if strings.HasPrefix(rest, "=") {
		rest = strings.TrimLeft(rest[1:], " \t")
	}
	return t[:i], strings.TrimSpace(rest)
}

func normalize(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
