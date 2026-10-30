package loginprofile

import (
	"bytes"
	"testing"
)

func TestPortableGitHubHostsSelectsActiveAccountAndDropsHostSettings(t *testing.T) {
	data, err := PortableGitHubHosts([]byte("github.com:\n  user: active\n  git_protocol: ssh\n  oauth_token: old-token\n  users:\n    active:\n      oauth_token: selected-token\n    other:\n      oauth_token: private-other-token\n  aliases:\n    unsafe: '!host-command'\n"))
	if err != nil {
		t.Fatal(err)
	}
	for _, keep := range []string{"selected-token", `"git_protocol":"https"`, `"user":"active"`} {
		if !bytes.Contains(data, []byte(keep)) {
			t.Fatal("active credential lost")
		}
	}
	for _, drop := range []string{"old-token", "private-other-token", "host-command", "aliases"} {
		if bytes.Contains(data, []byte(drop)) {
			t.Fatal("host settings or unrelated credentials copied")
		}
	}
	if _, err = PortableGitHubHosts(data); err != nil {
		t.Fatal("normalized profile is not reusable")
	}
}

func TestPortableGitHubHostsRejectsMissingOrInvalidCredentials(t *testing.T) {
	for _, value := range []string{"", "[]", "github.com:\n  user: keyring-only\n", "bad/host:\n  user: test\n  oauth_token: fixture\n", "github.com:\n  user: bad/user\n  oauth_token: fixture\n", "github.com: [invalid]"} {
		if _, err := PortableGitHubHosts([]byte(value)); err == nil {
			t.Fatal("invalid GitHub profile accepted")
		}
	}
}
