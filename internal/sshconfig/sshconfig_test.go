package sshconfig

import (
	"strings"
	"testing"
)

const sample = `Host *
  StrictHostKeyChecking no

Host fairfair-live-cp-vpn-ext
  IdentityFile ~/.ssh/fairfair-live
  IdentitiesOnly yes
  User ubuntu
#  HostName 57.130.39.121
  HostName 57.128.108.101
  PubKeyAuthentication yes

Host   github.com
	IdentityFile=~/.ssh/github
	User git

Match host *.internal exec "true"
  ProxyJump bastion
`

func TestRoundTrip(t *testing.T) {
	for _, c := range []string{sample, "", "\n", "Host a", "# just a comment\n\n", strings.TrimSuffix(sample, "\n")} {
		if got := Parse(c).String(); got != c {
			t.Errorf("round trip broken\nwant %q\ngot  %q", c, got)
		}
	}
}

func TestBlocks(t *testing.T) {
	f := Parse(sample)
	if len(f.Blocks) != 4 {
		t.Fatalf("expected 4 blocks, got %d", len(f.Blocks))
	}
	gh := f.Find("github.com")
	if gh == nil {
		t.Fatal("github.com not found")
	}
	if got := gh.Get("identityfile"); got != "~/.ssh/github" {
		t.Errorf("IdentityFile = %q", got)
	}
	if f.Blocks[0].IsConcrete() || f.Blocks[3].IsConcrete() || !gh.IsConcrete() {
		t.Error("IsConcrete does not tell wildcards and Match apart")
	}
	cp := f.Find("fairfair-live-cp-vpn-ext")
	if got := cp.Get("HostName"); got != "57.128.108.101" {
		t.Errorf("the commented-out line must not count: HostName = %q", got)
	}
}

func TestSetOptionsKeepsCommentsAndOrder(t *testing.T) {
	f := Parse(sample)
	b := f.Find("fairfair-live-cp-vpn-ext")
	b.SetOptions([]Option{
		{"HostName", "1.2.3.4"},
		{"User", "ubuntu"},
		{"IdentityFile", "~/.ssh/fairfair-live"},
		{"Port", "2222"},
	})
	want := `Host fairfair-live-cp-vpn-ext
  IdentityFile ~/.ssh/fairfair-live
  User ubuntu
#  HostName 57.130.39.121
  HostName 1.2.3.4
  Port 2222`
	if got := b.Text(); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
	if !strings.Contains(f.String(), "\n\nHost   github.com") {
		t.Error("the separation from the next block was lost")
	}
}

func TestSetOptionsMultiValued(t *testing.T) {
	b := NewHostBlock("x", []Option{{"IdentityFile", "a"}, {"IdentityFile", "b"}})
	b.SetOptions([]Option{{"IdentityFile", "a"}, {"IdentityFile", "c"}, {"User", ""}})
	want := "Host x\n  IdentityFile a\n  IdentityFile c"
	if got := b.Text(); got != want {
		t.Errorf("got %q", got)
	}
}

func TestAppendAndRemove(t *testing.T) {
	f := Parse("Host a\n  User x")
	f.Append(NewHostBlock("b", []Option{{"HostName", "10.0.0.1"}}))
	want := "Host a\n  User x\n\nHost b\n  HostName 10.0.0.1\n"
	if got := f.String(); got != want {
		t.Fatalf("got %q", got)
	}
	f.Remove(f.Find("a"))
	if got := f.String(); got != "Host b\n  HostName 10.0.0.1\n" {
		t.Errorf("got %q", got)
	}

	empty := Parse("")
	empty.Append(NewHostBlock("c", nil))
	if got := empty.String(); got != "Host c\n" {
		t.Errorf("empty file: got %q", got)
	}
}
