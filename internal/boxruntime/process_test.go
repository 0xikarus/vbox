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
	for agent, want := range map[string][]string{"codex": {"codex", "exec", "--skip-git-repo-check", "--", prompt}, "claude": {"claude", "-p", "--", prompt}, "shell": {"/bin/bash", "-lc", prompt}} {
		got, err := processArgv(agent, prompt)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: %q %v", agent, got, err)
		}
	}
	if _, err := processArgv("opencode", prompt); err == nil {
		t.Fatal("unsupported agent accepted")
	}
}

func TestProcessOptionsAreLiteralArguments(t *testing.T) {
	for _, agent := range []string{"codex", "claude"} {
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
