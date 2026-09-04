package boxruntime

import (
	"errors"
	"os"
	"os/user"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestWorkloadArgvDropsRootAndPinsTheWorkloadHome(t *testing.T) {
	name, args := WorkloadArgv(0, "gh", []string{"auth", "setup-git"})
	argv := append([]string{name}, args...)
	if name != "sudo" {
		t.Fatalf("root argv did not drop privileges: %#v", argv)
	}
	joined := strings.Join(argv, " ")
	if !strings.Contains(joined, "-u vmbox") || !strings.Contains(joined, "HOME=/data/home") {
		t.Fatalf("root argv=%q", joined)
	}
	if !RunsAsWorkloadUser(argv) {
		t.Fatalf("root argv was not recognized as unprivileged: %#v", argv)
	}

	name, args = WorkloadArgv(10001, "gh", []string{"auth", "setup-git"})
	argv = append([]string{name}, args...)
	if !RunsAsWorkloadUser(argv) || !strings.Contains(strings.Join(argv, " "), "HOME=/data/home") {
		t.Fatalf("unprivileged argv=%#v", argv)
	}
}

func TestRunsAsWorkloadUserRejectsRootCommands(t *testing.T) {
	for _, argv := range [][]string{
		nil,
		{"gh", "auth", "status"},
		{"env", "HOME=/root", "gh", "auth", "status"},
		{"sudo", "-n", "-H", "-u", "root", "--", "env", "HOME=/data/home", "gh"},
	} {
		if RunsAsWorkloadUser(argv) {
			t.Fatalf("argv accepted as unprivileged: %#v", argv)
		}
	}
}

func TestLookupWorkloadOwnershipSkipsUnprivilegedRuntimes(t *testing.T) {
	owner, err := LookupWorkloadOwnership(10001, func(string) (*user.User, error) {
		t.Fatal("unprivileged runtime looked up the workload account")
		return nil, nil
	})
	if err != nil || owner != nil {
		t.Fatalf("owner=%v err=%v", owner, err)
	}

	owner, err = LookupWorkloadOwnership(0, func(name string) (*user.User, error) {
		if name != "vmbox" {
			t.Fatalf("looked up %q", name)
		}
		return &user.User{Uid: "10001", Gid: "10002"}, nil
	})
	if err != nil || owner == nil || *owner != (Ownership{UID: 10001, GID: 10002}) {
		t.Fatalf("owner=%v err=%v", owner, err)
	}

	if _, err := LookupWorkloadOwnership(0, func(string) (*user.User, error) {
		return nil, errors.New("no such user")
	}); err == nil {
		t.Fatal("a root runtime accepted an unresolvable workload account")
	}
}

func TestEnsurePrivateDirectoryOwnsOnlyWhatItCreates(t *testing.T) {
	root := t.TempDir()
	existing := filepath.Join(root, "workspace")
	if err := os.Mkdir(existing, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(existing, 0o755); err != nil {
		t.Fatal(err)
	}
	var chowned []string
	chown := func(path string, uid, gid int) error {
		if uid != 10001 || gid != 10001 {
			t.Fatalf("%s handed to %d:%d", path, uid, gid)
		}
		chowned = append(chowned, path)
		return nil
	}
	owner := &Ownership{UID: 10001, GID: 10001}
	if err := EnsurePrivateDirectory(root, filepath.Join(root, "home", ".config", "gh"), owner, chown); err != nil {
		t.Fatal(err)
	}
	if err := EnsurePrivateDirectory(root, existing, owner, chown); err != nil {
		t.Fatal(err)
	}
	want := []string{
		filepath.Join(root, "home"),
		filepath.Join(root, "home", ".config"),
		filepath.Join(root, "home", ".config", "gh"),
	}
	if !reflect.DeepEqual(chowned, want) {
		t.Fatalf("chowned=%v want=%v", chowned, want)
	}
	for _, directory := range want {
		info, err := os.Stat(directory)
		if err != nil || info.Mode().Perm() != PrivateDirectoryMode {
			t.Fatalf("%s mode=%v err=%v", directory, info.Mode().Perm(), err)
		}
	}
	if info, err := os.Stat(existing); err != nil || info.Mode().Perm() != 0o755 {
		t.Fatalf("existing directory was narrowed: mode=%v err=%v", info.Mode().Perm(), err)
	}
}

func TestEnsurePrivateDirectoryRejectsEscapes(t *testing.T) {
	root := t.TempDir()
	if err := EnsurePrivateDirectory(root, filepath.Join(root, "..", "escape"), nil, nil); err == nil {
		t.Fatal("directory outside the sync root was accepted")
	}
}

func TestCheckSyncedFileModeKeepsCredentialsPrivate(t *testing.T) {
	tests := []struct {
		path    string
		mode    os.FileMode
		wantErr bool
	}{
		{path: "/data/home/.codex/auth.json", mode: 0o600},
		{path: "/data/home/.claude.json", mode: 0o400},
		{path: "/data/home/.codex/auth.json", mode: 0o644, wantErr: true},
		{path: "/data/home/.config/gh/hosts.yml", mode: 0o660, wantErr: true},
		{path: "/data/workspace/AGENTS.md", mode: 0o644},
		{path: "/data/workspace/AGENTS.md", mode: 0o666, wantErr: true},
	}
	for _, test := range tests {
		err := CheckSyncedFileMode(test.path, test.mode)
		if (err != nil) != test.wantErr {
			t.Errorf("CheckSyncedFileMode(%q, %04o) = %v", test.path, test.mode, err)
		}
	}
}

func TestGitIdentityArgumentsSkipUnresolvedFields(t *testing.T) {
	if arguments := GitIdentityArguments(nil); arguments != nil {
		t.Fatalf("arguments=%v", arguments)
	}
	arguments := GitIdentityArguments(&GitHubSetup{Name: " Octo Cat ", Email: ""})
	want := [][]string{{"config", "--global", "user.name", "Octo Cat"}}
	if !reflect.DeepEqual(arguments, want) {
		t.Fatalf("arguments=%v want=%v", arguments, want)
	}
}

func TestSecureGitHubConfigNarrowsHomeAndCredentialFiles(t *testing.T) {
	for _, expected := range []string{
		`chmod 0700 "$HOME"`,
		`chmod 0700 "${HOME}/.config" "$config"`,
		`chmod 0600 "$config/hosts.yml"`,
		`chmod 0600 "${HOME}/.gitconfig"`,
	} {
		if !strings.Contains(SecureGitHubConfigScript, expected) {
			t.Fatalf("secure GitHub script missing %q: %s", expected, SecureGitHubConfigScript)
		}
	}
}
