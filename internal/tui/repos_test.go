package tui

import "testing"

func TestNameFromURL(t *testing.T) {
	cases := map[string]string{
		"git@github.com:fairfair/ssh-config.git":         "fairfair",
		"git@github.com:romainlavabre/ssh-config-editor": "romainlavabre",
		"https://github.com/fairfair/ssh-config.git":     "fairfair",
		"https://gitlab.com/marea/infra/ssh.git":         "marea",
		"ssh://git@git.example.com:2222/ops/ssh.git":     "ops",
		"git@host:ssh-config.git":                        "ssh-config",
		"/srv/git/team.git":                              "team",
		"./team.git":                                     "team",
		"file:///srv/git/team.git":                       "team",
		"":                                               "",
	}
	for url, want := range cases {
		if got := nameFromURL(url); got != want {
			t.Errorf("nameFromURL(%q) = %q, want %q", url, got, want)
		}
	}
}
