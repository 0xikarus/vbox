package boxruntime

import (
	"context"
	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestIdleHibernateRealTmux(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux unavailable")
	}
	t.Setenv("TMUX_TMPDIR", t.TempDir())
	t.Setenv("TMUX", "")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	t.Cleanup(func() { _, _ = tmuxOutput(context.Background(), "kill-server") })
	root := filepath.Join(t.TempDir(), ".vmbox")
	if err := SetNativeAssignment(ctx, strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	if _, err := tmuxOutput(ctx, "new-session", "-d", "-s", "sibling", "sleep 30"); err != nil {
		t.Fatal(err)
	}
	if _, err := PrepareIdleHibernate(ctx, root); err == nil {
		t.Fatal("live sibling allowed hibernation")
	}
	if _, err := tmuxOutput(ctx, "has-session", "-t", "=sibling"); err != nil {
		t.Fatal("sibling killed", err)
	}
	if _, err := tmuxOutput(ctx, "kill-session", "-t", "=sibling"); err != nil {
		t.Fatal(err)
	}
	if _, err := PrepareIdleHibernate(ctx, root); err != nil {
		t.Fatal("empty server could not hibernate", err)
	}
}

func TestProcessArgv(t *testing.T) {
	prompt := "-dangerous ' $HOME; ä\nsecond line"
	for agent, want := range map[string][]string{"codex": {"codex", "exec", "--skip-git-repo-check", "--", prompt}, "claude": {"claude", "-p", "--", prompt}, "opencode": {"opencode", "run", "--", prompt}, "shell": {"/bin/bash", "-lc", prompt}} {
		got, err := processArgv(agent, prompt)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: %q %v", agent, got, err)
		}
	}
	if _, err := processArgv("unknown", prompt); err == nil {
		t.Fatal("unsupported agent accepted")
	}
}

func TestProcessOptionsAreLiteralArguments(t *testing.T) {
	for _, agent := range []string{"codex", "claude", "opencode"} {
		task := v1.ProcessTask{Agent: agent, Prompt: "prompt; $(false)", Model: "custom-model", Args: []string{"--option", "literal value; $(false)"}}
		got, err := processTaskArgv(task)
		if err != nil {
			t.Fatal(err)
		}
		want := []string{"--option", "literal value; $(false)", "--model", "custom-model", "--", task.Prompt}
		if !reflect.DeepEqual(got[len(got)-len(want):], want) {
			t.Fatalf("argv changed: %q", got)
		}
	}
	for _, task := range []v1.ProcessTask{{Agent: "shell", Model: "x"}, {Agent: "codex", Args: []string{"--"}}, {Agent: "claude", Args: []string{"bad\x00arg"}}} {
		if _, err := processTaskArgv(task); err == nil {
			t.Fatal("invalid options accepted")
		}
	}
}

func TestProcessRecordsActualExitAndNoReplay(t *testing.T) {
	for _, test := range []struct {
		name, prompt, output string
		code                 int
	}{{"success", "printf 'unique ä\\n'", "unique ä\n", 0}, {"failure", "printf 'before\\n'; exit 7", "before\n", 7}} {
		t.Run(test.name, func(t *testing.T) {
			base := t.TempDir()
			root := filepath.Join(base, ".vmbox")
			if err := os.Mkdir(filepath.Join(base, "workspace"), 0700); err != nil {
				t.Fatal(err)
			}
			dir, _ := processDir(root, "test")
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
			task := v1.ProcessTask{ID: "test", Agent: "shell", Prompt: test.prompt, Session: "test", CreatedAt: time.Now().UTC()}
			if err := writeProcessJSON(filepath.Join(dir, "task.json"), task); err != nil {
				t.Fatal(err)
			}
			if err := RunProcess(root, "test"); err != nil {
				t.Fatal(err)
			}
			result, err := ReadProcess(root, "test")
			if err != nil {
				t.Fatal(err)
			}
			if result.State != "exited" || result.ExitCode == nil || *result.ExitCode != test.code || result.Output != test.output || result.StartedAt == nil || result.FinishedAt == nil {
				t.Fatalf("%+v", result)
			}
			if err = RunProcess(root, "test"); err == nil {
				t.Fatal("execution replayed")
			}
		})
	}
}

func TestProcessOutputBounded(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "log")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	w := &cappedOutput{file: f}
	p := []byte(strings.Repeat("x", ProcessOutputLimit+100))
	if n, err := w.Write(p); err != nil || n != len(p) || !w.truncated {
		t.Fatal(n, err)
	}
	st, _ := f.Stat()
	if st.Size() != ProcessOutputLimit {
		t.Fatal(st.Size())
	}
}

func TestProcessDesktopPreparationFailureRemainsRetryable(t *testing.T) {
	bin := t.TempDir()
	for _, name := range []string{"Xtigervnc", "tmux"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\nexit 1\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)
	root := filepath.Join(t.TempDir(), ".vmbox")
	task := v1.ProcessTask{ID: "prepare-test", Session: "prepare-test", Agent: "shell", Prompt: "true"}
	if err := StartProcess(context.Background(), root, task); err == nil {
		t.Fatal("missing desktop assignment accepted")
	}
	dir, _ := processDir(root, task.ID)
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("preparation failure claimed the execution journal")
	}
	// Retry with the legacy shell-only image. It must be able to claim the same ID.
	if err := os.Remove(filepath.Join(bin, "Xtigervnc")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "tmux"), []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := StartProcess(context.Background(), root, task); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "task.json")); err != nil {
		t.Fatal(err)
	}
}

func TestProcessExistingJournalDoesNotReopenDesktop(t *testing.T) {
	bin := t.TempDir()
	marker := filepath.Join(bin, "desktop-was-probed")
	for _, name := range []string{"Xtigervnc", "tmux"} {
		script := "#!/bin/sh\nprintf touched > " + shellQuote(marker) + "\nexit 1\n"
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)
	root := filepath.Join(t.TempDir(), ".vmbox")
	task := v1.ProcessTask{ID: "existing-test", Session: "existing-test", Agent: "shell", Prompt: "true"}
	dir, _ := processDir(root, task.ID)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := StartProcess(context.Background(), root, task); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("existing execution caused desktop/process side effects")
	}
}
