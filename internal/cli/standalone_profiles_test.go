package cli

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/0xikarus/vmbox-service/internal/procexec"
)

func TestStandaloneProfileImportIsPortableAndPrivate(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".claude")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string]string{".credentials.json": `{"synthetic":"test-secret"}`, "settings.json": `{"model":"test-model","hooks":{"evil":"host-command"},"statusLine":{"command":"host-command"},"enabledPlugins":{"host-plugin":true}}`} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, ".claude.json"), []byte(`{"hasCompletedOnboarding":true,"projects":{"host-path":{}},"mcpServers":{"host-tool":{"command":"host-command"}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	data, err := standaloneProfileArchive([]string{"claude=" + dir})
	if err != nil {
		t.Fatal(err)
	}
	defer clear(data)
	tr := tar.NewReader(bytes.NewReader(data))
	count := 0
	for {
		h, e := tr.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			t.Fatal(e)
		}
		if h.Uid != 10001 || h.Gid != 10001 {
			t.Fatal("incorrect workload ownership")
		}
		if strings.Contains(h.Name, "..") || strings.HasPrefix(h.Name, "/") {
			t.Fatal("unsafe destination")
		}
		if h.Typeflag == tar.TypeDir {
			continue
		}
		count++
		if h.Mode != 0600 {
			t.Fatal("profile is not private")
		}
		b, e := io.ReadAll(tr)
		if e != nil {
			t.Fatal(e)
		}
		if h.Name != ".claude/.credentials.json" {
			if bytes.Contains(b, []byte("host-command")) || bytes.Contains(b, []byte("host-path")) {
				t.Fatal("host command/path was imported")
			}
			var settings map[string]any
			if e = json.Unmarshal(b, &settings); e != nil {
				t.Fatal(e)
			}
		}
	}
	if count != 3 {
		t.Fatalf("copied %d profile files", count)
	}
}

func TestStandaloneGitHubImportUsesSelectedKeyringAccount(t *testing.T) {
	runner := &procexec.FakeRunner{Results: []procexec.Result{{Stdout: []byte("selected-fixture-token\n")}}}
	a := &App{Runner: runner}
	archive, err := a.standaloneProfileArchive(context.Background(), []string{"github=github.com:chosen"})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(runner.Calls[0].Argv, " "); got != "gh auth token --hostname github.com --user chosen" {
		t.Fatal("wrong account exported")
	}
	tr := tar.NewReader(bytes.NewReader(archive))
	found := false
	for {
		h, e := tr.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			t.Fatal(e)
		}
		if h.Typeflag == tar.TypeReg {
			data, _ := io.ReadAll(tr)
			if h.Name != ".config/gh/hosts.yml" || h.Mode != 0600 || h.Uid != 10001 || !bytes.Contains(data, []byte("selected-fixture-token")) {
				t.Fatal("incorrect private GitHub import")
			}
			found = true
		}
	}
	if !found {
		t.Fatal("missing GitHub configuration")
	}
}

func TestStandaloneGitHubFileImportExcludesAliasesAndRejectsLinks(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "hosts.yml")
	if err := os.WriteFile(file, []byte("github.com:\n  user: fixture\n  oauth_token: fixture-token\n  aliases:\n    evil: '!host-command'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	archive, err := standaloneProfileArchive([]string{"github=" + dir})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(archive, []byte("host-command")) {
		t.Fatal("host alias imported")
	}
	link := filepath.Join(dir, "link.yml")
	if err = os.Symlink(file, link); err != nil {
		t.Fatal(err)
	}
	if _, err = standaloneProfileArchive([]string{"github=" + link}); err == nil {
		t.Fatal("symlink accepted")
	}
	if _, err = standaloneProfileArchive([]string{"github=" + dir, "github=" + dir}); err == nil {
		t.Fatal("duplicate GitHub accounts accepted")
	}
}
func TestStandaloneProfileImportRejectsDuplicateApplications(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".credentials.json"), []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := standaloneProfileArchive([]string{"claude=" + dir, "claude=" + dir}); err == nil {
		t.Fatal("duplicate application accepted")
	}
}
