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
	for _, c := range []string{sample, "", "\n", "Host a", "# juste un commentaire\n\n", strings.TrimSuffix(sample, "\n")} {
		if got := Parse(c).String(); got != c {
			t.Errorf("aller-retour cassé\nattendu %q\nobtenu  %q", c, got)
		}
	}
}

func TestBlocks(t *testing.T) {
	f := Parse(sample)
	if len(f.Blocks) != 4 {
		t.Fatalf("attendu 4 blocs, obtenu %d", len(f.Blocks))
	}
	gh := f.Find("github.com")
	if gh == nil {
		t.Fatal("github.com introuvable")
	}
	if got := gh.Get("identityfile"); got != "~/.ssh/github" {
		t.Errorf("IdentityFile = %q", got)
	}
	if f.Blocks[0].IsConcrete() || f.Blocks[3].IsConcrete() || !gh.IsConcrete() {
		t.Error("IsConcrete ne distingue pas les jokers et les Match")
	}
	cp := f.Find("fairfair-live-cp-vpn-ext")
	if got := cp.Get("HostName"); got != "57.128.108.101" {
		t.Errorf("la ligne commentée ne doit pas compter : HostName = %q", got)
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
		t.Errorf("obtenu :\n%s\nattendu :\n%s", got, want)
	}
	if !strings.Contains(f.String(), "\n\nHost   github.com") {
		t.Error("la séparation avec le bloc suivant a été perdue")
	}
}

func TestSetOptionsMultiValued(t *testing.T) {
	b := NewHostBlock("x", []Option{{"IdentityFile", "a"}, {"IdentityFile", "b"}})
	b.SetOptions([]Option{{"IdentityFile", "a"}, {"IdentityFile", "c"}, {"User", ""}})
	want := "Host x\n  IdentityFile a\n  IdentityFile c"
	if got := b.Text(); got != want {
		t.Errorf("obtenu %q", got)
	}
}

func TestAppendAndRemove(t *testing.T) {
	f := Parse("Host a\n  User x")
	f.Append(NewHostBlock("b", []Option{{"HostName", "10.0.0.1"}}))
	want := "Host a\n  User x\n\nHost b\n  HostName 10.0.0.1\n"
	if got := f.String(); got != want {
		t.Fatalf("obtenu %q", got)
	}
	f.Remove(f.Find("a"))
	if got := f.String(); got != "Host b\n  HostName 10.0.0.1\n" {
		t.Errorf("obtenu %q", got)
	}

	empty := Parse("")
	empty.Append(NewHostBlock("c", nil))
	if got := empty.String(); got != "Host c\n" {
		t.Errorf("fichier vide : obtenu %q", got)
	}
}
