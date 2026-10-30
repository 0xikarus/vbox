package standalone

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestImportedGitHubProfileAuthenticatesGHAndConfiguresHTTPSGit(t *testing.T) {
	gh, err := exec.LookPath("gh")
	if err != nil {
		t.Skip("GitHub CLI required for offline credential interoperability test")
	}
	home := t.TempDir()
	config := filepath.Join(home, ".config", "gh")
	if err = os.MkdirAll(config, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(config, "hosts.yml"), []byte("github.com:\n  user: fixture\n  oauth_token: synthetic-github-token\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_ENTERPRISE_TOKEN", "")
	t.Setenv("GITHUB_ENTERPRISE_TOKEN", "")
	if err = configureGitHub(context.Background(), home); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(gh, "auth", "token", "--hostname", "github.com", "--user", "fixture")
	cmd.Env = append(os.Environ(), "HOME="+home, "GH_CONFIG_DIR="+config)
	token, err := cmd.Output()
	if err != nil || strings.TrimSpace(string(token)) != "synthetic-github-token" {
		t.Fatal("gh could not use imported token")
	}
	gitconfig, err := os.ReadFile(filepath.Join(home, ".gitconfig"))
	if err != nil || !strings.Contains(string(gitconfig), "auth git-credential") {
		t.Fatal("HTTPS Git credential helper missing")
	}
	info, _ := os.Stat(filepath.Join(config, "hosts.yml"))
	if info.Mode().Perm() != 0600 {
		t.Fatal("credential permissions changed")
	}
}
