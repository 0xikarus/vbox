package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/0xikarus/vmbox-service/internal/boxruntime"
	"github.com/0xikarus/vmbox-service/internal/provider"
)

func TestReceiveFilesWritesOneChecksummedBatch(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "home", ".codex", "auth.json")
	second := filepath.Join(root, "workspace", "AGENTS.md")
	request := boxruntime.SyncRequest{Files: []boxruntime.SyncFile{
		{Path: first, Mode: "0600", Data: []byte("secret")},
		{Path: second, Mode: "0644", Data: []byte("instructions")},
	}}
	payload, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := receiveFiles(bytes.NewReader(payload), root, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	expected := sha256.Sum256(payload)
	if digest != fmt.Sprintf("%x", expected[:]) {
		t.Fatalf("digest=%q", digest)
	}
	for path, want := range map[string]string{first: "secret", second: "instructions"} {
		data, err := os.ReadFile(path)
		if err != nil || string(data) != want {
			t.Fatalf("file %s data=%q err=%v", path, data, err)
		}
	}
	if info, err := os.Stat(first); err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("first mode=%v err=%v", info, err)
	}
}

func TestReceiveFilesRejectsDestinationOutsideRoot(t *testing.T) {
	root := t.TempDir()
	payload, _ := json.Marshal(boxruntime.SyncRequest{Files: []boxruntime.SyncFile{{Path: filepath.Join(root, "..", "escape"), Mode: "0600", Data: []byte("no")}}})
	if _, err := receiveFiles(bytes.NewReader(payload), root, nil, nil); err == nil {
		t.Fatal("sync accepted a destination outside its root")
	}
}

func TestReceiveFilesRemovesStaleProfileFiles(t *testing.T) {
	root := t.TempDir()
	stale := filepath.Join(root, "home", ".claude", ".credentials.json")
	if err := os.MkdirAll(filepath.Dir(stale), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stale, []byte("stale-claude-login"), 0600); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(root, "home", ".codex", "auth.json")
	payload, err := json.Marshal(map[string]any{
		"files":  []boxruntime.SyncFile{{Path: destination, Mode: "0600", Data: []byte("new-codex-login")}},
		"remove": []string{stale},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := receiveFiles(bytes.NewReader(payload), root, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale Claude credential survived Codex profile selection: %v", err)
	}
	if data, err := os.ReadFile(destination); err != nil || string(data) != "new-codex-login" {
		t.Fatalf("selected Codex credential=%q err=%v", data, err)
	}
}

func TestReceiveFilesAllowsRemoveOnlyProfileClear(t *testing.T) {
	root := t.TempDir()
	stale := filepath.Join(root, "home", ".codex", "auth.json")
	if err := os.MkdirAll(filepath.Dir(stale), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stale, []byte("stale"), 0600); err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]any{"files": []boxruntime.SyncFile{}, "remove": []string{stale}})
	if _, err := receiveFiles(bytes.NewReader(payload), root, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(stale); !os.IsNotExist(err) {
		t.Fatalf("cleared profile credential still exists: %v", err)
	}
}

type setupCall struct {
	argv  []string
	stdin string
}

// recordingSetupExecutor answers as a box whose synced credentials belong to the
// workload user. rootOnly makes it behave like the production failure this
// package guards against: credentials only root can read, so a check performed
// by root reports success that the agents can never reproduce.
func recordingSetupExecutor(calls *[]setupCall, rootOnly bool) setupCommand {
	return func(_ context.Context, stdin io.Reader, stdout, _ io.Writer, name string, args ...string) error {
		var input []byte
		if stdin != nil {
			input, _ = io.ReadAll(stdin)
		}
		argv := append([]string{name}, args...)
		*calls = append(*calls, setupCall{argv: argv, stdin: string(input)})
		asWorkload := boxruntime.RunsAsWorkloadUser(argv)
		command := setupCommandName(argv)
		if rootOnly && asWorkload {
			switch command {
			case "codex":
				return fmt.Errorf("not logged in")
			case "claude":
				_, _ = io.WriteString(stdout, `{"loggedIn":false}`)
				return nil
			case "gh":
				if strings.Contains(strings.Join(argv, " "), "auth status") {
					return fmt.Errorf("not logged in")
				}
			}
		}
		if command == "claude" {
			_, _ = io.WriteString(stdout, `{"loggedIn":true}`)
		}
		return nil
	}
}

// setupCommandName returns the program a possibly privilege-dropped argv runs.
func setupCommandName(argv []string) string {
	for index := 0; index < len(argv); index++ {
		switch argv[index] {
		case "sudo", "env", "-n", "-H", "--", "-u":
			continue
		}
		if argv[index] == provider.WorkloadUser || strings.Contains(argv[index], "=") {
			continue
		}
		return argv[index]
	}
	return ""
}

func TestPerformSetupConsolidatesCredentialAndAuthWork(t *testing.T) {
	var calls []setupCall
	request := boxruntime.SetupRequest{
		Workspace:    "/data/workspace",
		GitHub:       &boxruntime.GitHubSetup{Host: "github.com", User: "octocat", Protocol: "ssh", Token: "github-secret", Name: "Octo Cat", Email: "octocat@users.noreply.github.com"},
		Applications: []string{"codex", "claude", "codex"},
	}
	result, err := performSetup(context.Background(), request, recordingSetupExecutor(&calls, false), 1000)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(result.Authentication, map[string]bool{"codex": true, "claude": true}) {
		t.Fatalf("result=%+v calls=%#v", result, calls)
	}
	if calls[0].stdin != "github-secret\n" || strings.Contains(strings.Join(calls[0].argv, " "), "github-secret") {
		t.Fatalf("GitHub token transport=%#v", calls[0])
	}
	var commands []string
	for _, call := range calls {
		commands = append(commands, strings.Join(call.argv, " "))
	}
	joined := strings.Join(commands, "\n")
	for _, want := range []string{
		"gh auth login --hostname github.com --git-protocol ssh --with-token",
		"gh auth setup-git --hostname github.com",
		"git config --global user.name Octo Cat",
		"git config --global user.email octocat@users.noreply.github.com",
		"gh auth status --hostname github.com",
		"vmbox-entrypoint --configure-agent-trust /data/workspace",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("setup did not run %q:\n%s", want, joined)
		}
	}
}

// TestPerformSetupRunsEveryStepAsTheWorkloadUser pins the production failure in
// which the runtime performed GitHub login, Git identity setup, and credential
// verification as root: root wrote configuration the agents could not read, and
// answered the verification from its own HOME.
func TestPerformSetupRunsEveryStepAsTheWorkloadUser(t *testing.T) {
	var calls []setupCall
	request := boxruntime.SetupRequest{
		Workspace:    "/data/workspace",
		GitHub:       &boxruntime.GitHubSetup{Host: "github.com", User: "octocat", Protocol: "https", Token: "github-secret", Name: "Octo Cat", Email: "octocat@example.invalid"},
		Applications: []string{"codex", "claude"},
	}
	if _, err := performSetup(context.Background(), request, recordingSetupExecutor(&calls, false), 0); err != nil {
		t.Fatal(err)
	}
	if len(calls) == 0 {
		t.Fatal("setup ran no commands")
	}
	for _, call := range calls {
		if !boxruntime.RunsAsWorkloadUser(call.argv) {
			t.Fatalf("setup step ran with root privileges: %#v", call.argv)
		}
		if call.argv[0] != "sudo" {
			t.Fatalf("root setup step did not drop privileges: %#v", call.argv)
		}
	}
}

// TestRootOwnedCredentialsCannotVerifyAuthentication is the regression test for
// the false-positive report: a box where only root can see the synced logins
// must report the agents as unauthenticated, never as ready. It fails as soon as
// verification stops dropping to the workload user.
func TestRootOwnedCredentialsCannotVerifyAuthentication(t *testing.T) {
	var calls []setupCall
	request := boxruntime.SetupRequest{
		Workspace:    "/data/workspace",
		Applications: []string{"codex", "claude"},
	}
	result, err := performSetup(context.Background(), request, recordingSetupExecutor(&calls, true), 0)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(result.Authentication, map[string]bool{"codex": false, "claude": false}) {
		t.Fatalf("root-only credentials produced a false positive: %+v", result.Authentication)
	}
}

// TestStaleAgentCredentialIsNeverVerifiedByExitStatusAlone reproduces the second
// production failure, the one that outlives the ownership fix. A freshly created
// task box held a Claude credential that was already correct in every way the
// ownership work checks: owned by vmbox:vmbox, mode 0600, readable by the agent.
// The token behind it was simply stale. `claude auth status --json` still exited
// with status zero, and said so only in its structured payload:
//
//	{"loggedIn":false}
//
// Treating that exit status as the verdict hands the operator a box reported as
// ready whose agent cannot log in. The parsed loggedIn field is the only
// acceptable evidence, and a payload that does not parse is not evidence at all.
func TestStaleAgentCredentialIsNeverVerifiedByExitStatusAlone(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		stdout   string
		verified bool
	}{
		{name: "the live failure: exit zero, loggedIn false", stdout: `{"loggedIn":false}`},
		{name: "human-readable output that does not parse", stdout: "not logged in\n"},
		{name: "no payload at all", stdout: ""},
		{name: "a payload that never mentions loggedIn", stdout: `{"account":"someone@example.invalid"}`},
		{name: "a genuine login", stdout: `{"loggedIn":true}`, verified: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			var argv []string
			// Every command succeeds. Exit status zero is exactly what the
			// production box reported, so it must never decide this on its own.
			execute := func(_ context.Context, _ io.Reader, stdout, _ io.Writer, name string, args ...string) error {
				full := append([]string{name}, args...)
				if setupCommandName(full) == "claude" {
					argv = full
					_, _ = io.WriteString(stdout, testCase.stdout)
				}
				return nil
			}
			// euid 1000 is the unprivileged runtime the ownership fix leaves
			// behind, so this covers a box whose credentials are already correct.
			request := boxruntime.SetupRequest{Workspace: "/data/workspace", Applications: []string{"claude"}}
			result, err := performSetup(context.Background(), request, execute, 1000)
			if err != nil {
				t.Fatal(err)
			}
			if result.Authentication["claude"] != testCase.verified {
				t.Fatalf("exit-zero output %q verified as %v, want %v", testCase.stdout, result.Authentication["claude"], testCase.verified)
			}
			if !boxruntime.RunsAsWorkloadUser(argv) {
				t.Fatalf("authentication was not checked as the workload user: %#v", argv)
			}
			if !strings.Contains(strings.Join(argv, " "), "--json") {
				t.Fatalf("authentication check did not ask for the structured payload: %#v", argv)
			}
		})
	}
}

// TestRootOnlyGitHubCredentialCannotVerifyAuthentication ensures a successful
// root login can never be mistaken for a GitHub login available to agents.
func TestRootOnlyGitHubCredentialCannotVerifyAuthentication(t *testing.T) {
	var calls []setupCall
	request := boxruntime.SetupRequest{
		Workspace: "/data/workspace",
		GitHub:    &boxruntime.GitHubSetup{Host: "github.com", User: "octocat", Protocol: "https", Token: "github-secret"},
	}
	_, err := performSetup(context.Background(), request, recordingSetupExecutor(&calls, true), 0)
	if err == nil || !strings.Contains(err.Error(), "verify GitHub authentication as workload user") {
		t.Fatalf("root-only GitHub credential verified: err=%v calls=%#v", err, calls)
	}
	for _, call := range calls {
		if !boxruntime.RunsAsWorkloadUser(call.argv) {
			t.Fatalf("GitHub setup step ran as root: %#v", call.argv)
		}
	}
}

// TestSyncedFilesAreHandedToTheWorkloadUser covers the ownership half of the
// same failure: root wrote the profiles, so the agents never saw them.
func TestSyncedFilesAreHandedToTheWorkloadUser(t *testing.T) {
	root := t.TempDir()
	profile := filepath.Join(root, "home", ".codex", "auth.json")
	instructions := filepath.Join(root, "workspace", "AGENTS.md")
	payload, err := json.Marshal(boxruntime.SyncRequest{Files: []boxruntime.SyncFile{
		{Path: profile, Mode: "0600", Data: []byte("secret")},
		{Path: instructions, Mode: "0644", Data: []byte("instructions")},
	}})
	if err != nil {
		t.Fatal(err)
	}
	owner := &boxruntime.Ownership{UID: 10001, GID: 10001}
	owned := make(map[string][2]int)
	chown := func(path string, uid, gid int) error {
		owned[path] = [2]int{uid, gid}
		return nil
	}
	if _, err := receiveFiles(bytes.NewReader(payload), root, owner, chown); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		filepath.Join(root, "home"), filepath.Join(root, "home", ".codex"), profile,
		filepath.Join(root, "workspace"), instructions,
	} {
		identity, ok := owned[path]
		if !ok {
			// A temporary file is renamed into place, so match the final path
			// through its recorded temporary name when necessary.
			for recorded, value := range owned {
				if filepath.Dir(recorded) == filepath.Dir(path) && strings.HasPrefix(filepath.Base(recorded), ".vmbox-upload-") {
					identity, ok = value, true
					break
				}
			}
		}
		if !ok || identity != [2]int{10001, 10001} {
			t.Fatalf("%s was not handed to vmbox:vmbox (owned=%v)", path, owned)
		}
	}
	for _, directory := range []string{filepath.Join(root, "home"), filepath.Join(root, "home", ".codex")} {
		info, err := os.Stat(directory)
		if err != nil || info.Mode().Perm() != boxruntime.PrivateDirectoryMode {
			t.Fatalf("directory %s mode=%v err=%v", directory, info.Mode().Perm(), err)
		}
	}
	if info, err := os.Stat(profile); err != nil || info.Mode().Perm() != boxruntime.CredentialMode {
		t.Fatalf("credential mode=%v err=%v", info.Mode().Perm(), err)
	}
}

// TestSyncKeepsExistingDirectoriesIntact makes sure the ownership pass never
// narrows a directory the box already had, such as /data/workspace.
func TestSyncKeepsExistingDirectoriesIntact(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	if err := os.Mkdir(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(boxruntime.SyncRequest{Files: []boxruntime.SyncFile{
		{Path: filepath.Join(workspace, "AGENTS.md"), Mode: "0644", Data: []byte("instructions")},
	}})
	var chowned []string
	chown := func(path string, _, _ int) error {
		chowned = append(chowned, path)
		return nil
	}
	if _, err := receiveFiles(bytes.NewReader(payload), root, &boxruntime.Ownership{UID: 10001, GID: 10001}, chown); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(workspace)
	if err != nil || info.Mode().Perm() != 0o755 {
		t.Fatalf("existing workspace mode=%v err=%v", info.Mode().Perm(), err)
	}
	for _, path := range chowned {
		if path == workspace {
			t.Fatal("sync took ownership of a directory it did not create")
		}
	}
}

func TestParseReport(t *testing.T) {
	tests := []struct {
		args          []string
		kind, message string
		wantErr       bool
	}{
		{args: []string{"working"}, kind: "progress", message: "working"},
		{args: []string{"--progress", "running", "tests"}, kind: "progress", message: "running tests"},
		{args: []string{"--needs-input", "which", "version?"}, kind: "needs_input", message: "which version?"},
		{args: []string{"--unknown", "message"}, wantErr: true},
		{args: []string{"--needs-input"}, wantErr: true},
	}
	for _, test := range tests {
		kind, message, err := parseReport(test.args)
		if (err != nil) != test.wantErr || kind != test.kind || message != test.message {
			t.Errorf("parseReport(%q) = %q, %q, %v", test.args, kind, message, err)
		}
	}
}
