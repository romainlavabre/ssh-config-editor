// Package sshconfig reads and rewrites an ssh_config file losslessly:
// every line is kept as is, only modified lines are rewritten.
package sshconfig

import "strings"

// Option is a "Key value" directive.
type Option struct {
	Key   string
	Value string
}

// Block is a Host or Match block, or the preamble (empty Header).
type Block struct {
	Header string   // raw "Host a b" line; empty for the preamble
	Lines  []string // raw lines after the header: options, comments, blank lines
}

// File is an ssh_config file split into preamble + blocks.
type File struct {
	Preamble     *Block
	Blocks       []*Block
	finalNewline bool
}

// Parse splits ssh_config content. Parse(c).String() == c for every c.
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

// String renders the file back.
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

// Hosts returns the Host blocks, in file order.
func (f *File) Hosts() []*Block {
	var out []*Block
	for _, b := range f.Blocks {
		if b.Kind() == "host" {
			out = append(out, b)
		}
	}
	return out
}

// Find returns the first Host block with this name, or nil.
func (f *File) Find(name string) *Block {
	name = normalize(name)
	for _, b := range f.Blocks {
		if b.Kind() == "host" && b.Name() == name {
			return b
		}
	}
	return nil
}

// Remove deletes a block from the file.
func (f *File) Remove(target *Block) bool {
	for i, b := range f.Blocks {
		if b == target {
			f.Blocks = append(f.Blocks[:i], f.Blocks[i+1:]...)
			return true
		}
	}
	return false
}

// Append adds a block at the end of the file, separated from the previous one by a blank line.
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

// Kind is "host", "match", or "" for the preamble.
func (b *Block) Kind() string {
	k, _ := SplitLine(b.Header)
	return strings.ToLower(k)
}

// Name returns the header patterns, with normalized spaces.
func (b *Block) Name() string {
	_, v := SplitLine(b.Header)
	return normalize(v)
}

// IsConcrete reports a Host block without wildcards: an alias you can connect to.
func (b *Block) IsConcrete() bool {
	if b.Kind() != "host" {
		return false
	}
	return !strings.ContainsAny(b.Name(), "*?!")
}

// SetName renames a Host block.
func (b *Block) SetName(name string) {
	b.Header = "Host " + normalize(name)
}

// Options returns the block's directives, in order.
func (b *Block) Options() []Option {
	var out []Option
	for _, l := range b.Lines {
		if k, v := SplitLine(l); k != "" {
			out = append(out, Option{Key: k, Value: v})
		}
	}
	return out
}

// Get returns the first value of the key (case-insensitive), or "".
func (b *Block) Get(key string) string {
	for _, o := range b.Options() {
		if strings.EqualFold(o.Key, key) {
			return o.Value
		}
	}
	return ""
}

// SetOptions replaces all the block's directives with opts. Existing lines
// are reused key by key (in order of appearance), comments stay in place,
// and new keys are added after the last directive.
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

// Text returns the block without its trailing blank lines.
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

// Clone copies the block, without its trailing blank lines.
func (b *Block) Clone() *Block {
	c := &Block{Header: b.Header, Lines: append([]string(nil), b.Lines...)}
	for len(c.Lines) > 0 && strings.TrimSpace(c.Lines[len(c.Lines)-1]) == "" {
		c.Lines = c.Lines[:len(c.Lines)-1]
	}
	return c
}

// NewHostBlock builds a Host block.
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

// SplitLine splits a line into key and value ("Key value" or "Key=value").
// Returns "", "" for a blank line or a comment.
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
