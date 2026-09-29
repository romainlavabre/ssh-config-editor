package sshconfig

import (
	"strings"
	"testing"
)

const base = `Host a
  HostName 10.0.0.1

Host b
  HostName 10.0.0.2

Host c
  HostName 10.0.0.3
`

func TestMergeDisjointChanges(t *testing.T) {
	ours := strings.Replace(base, "10.0.0.1", "10.0.0.11", 1)
	theirs := strings.Replace(base, "10.0.0.2", "10.0.0.22", 1) + "\nHost d\n  HostName 10.0.0.4\n"
	m := Merge3(base, ours, theirs)
	if len(m.Conflicts) != 0 {
		t.Fatalf("unexpected conflicts: %d", len(m.Conflicts))
	}
	got, err := m.Render()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"10.0.0.11", "10.0.0.22", "10.0.0.3", "Host d"} {
		if !strings.Contains(got, want) {
			t.Errorf("%q missing from the result:\n%s", want, got)
		}
	}
	if strings.Index(got, "Host c") > strings.Index(got, "Host d") {
		t.Error("the block added on the other side should keep its place (after c)")
	}
}

func TestMergeDeleteVersusUntouched(t *testing.T) {
	theirs := strings.Replace(base, "Host b\n  HostName 10.0.0.2\n\n", "", 1)
	m := Merge3(base, base, theirs)
	got, _ := m.Render()
	if strings.Contains(got, "Host b") {
		t.Errorf("the remote deletion should apply:\n%s", got)
	}
}

func TestMergeSameHostConflict(t *testing.T) {
	ours := strings.Replace(base, "10.0.0.2", "10.0.0.20", 1)
	theirs := strings.Replace(base, "10.0.0.2", "10.0.0.21", 1)
	m := Merge3(base, ours, theirs)
	if len(m.Conflicts) != 1 || m.Conflicts[0].Name() != "b" {
		t.Fatalf("expected a conflict on b, got %+v", m.Conflicts)
	}
	if _, err := m.Render(); err == nil {
		t.Fatal("Render should fail while the conflict is unresolved")
	}
	m.Conflicts[0].Resolve(m.Conflicts[0].Theirs)
	got, err := m.Render()
	if err != nil || !strings.Contains(got, "10.0.0.21") || strings.Contains(got, "10.0.0.20") {
		t.Errorf("unexpected result (%v):\n%s", err, got)
	}
}

func TestMergeDeleteVersusModify(t *testing.T) {
	ours := strings.Replace(base, "10.0.0.3", "10.0.0.33", 1)
	theirs := strings.Replace(base, "\nHost c\n  HostName 10.0.0.3\n", "", 1)
	m := Merge3(base, ours, theirs)
	if len(m.Conflicts) != 1 {
		t.Fatalf("expected 1 conflict, got %d", len(m.Conflicts))
	}
	c := m.Conflicts[0]
	if !c.Ours.Present || c.Theirs.Present {
		t.Errorf("unexpected sides: %+v", c)
	}
}

func TestMergeAddAddWithoutBase(t *testing.T) {
	m := Merge3("", "Host a\n  User x\n", "Host b\n  User y\n")
	got, _ := m.Render()
	if len(m.Conflicts) != 0 || !strings.Contains(got, "Host a") || !strings.Contains(got, "Host b") {
		t.Errorf("merging independent additions failed:\n%s", got)
	}
}
