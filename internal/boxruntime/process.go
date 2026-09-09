package boxruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sync"
	"syscall"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
)

const ProcessOutputLimit = 1 << 20

var processID = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

func processDir(root, id string) (string, error) {
	if !processID.MatchString(id) {
		return "", fmt.Errorf("invalid process ID")
	}
	return filepath.Join(root, "processes", id), nil
}

func processArgv(agent, prompt string) ([]string, error) {
	return processTaskArgv(v1.ProcessTask{Agent: agent, Prompt: prompt})
}

func processTaskArgv(task v1.ProcessTask) ([]string, error) {
	if err := v1.ValidateSetupScript(task.SetupScript); err != nil {
		return nil, err
	}
	if err := v1.ValidateTools(task.Tools); err != nil {
		return nil, err
	}
	if err := v1.ValidateProcessOptions(task.Agent, task.Model, task.Args); err != nil {
		return nil, err
	}
	agent, prompt := task.Agent, task.Prompt
	options := append([]string{}, task.Args...)
	if task.Model != "" {
		options = append(options, "--model", task.Model)
	}
	switch agent {
	case "codex":
		// A logical workspace need not itself be a Git checkout. This does not
		// change the configured sandbox, model, authentication, or permissions.
		return append(append([]string{"codex", "exec", "--skip-git-repo-check"}, options...), "--", prompt), nil
	case "claude":
		return append(append([]string{"claude", "-p"}, options...), "--", prompt), nil
	case "shell":
		return []string{"/bin/bash", "-lc", prompt}, nil
	default:
		return nil, fmt.Errorf("one-shot agent must be codex, claude, or shell")
	}
}

func writeProcessJSON(path string, value any) error {
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".result-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(b); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Rename(f.Name(), path); err != nil {
		return err
	}
	d, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

// An existing journal never causes another launch, including after an ambiguous
// SSH result or controller restart. Unknown outcomes require inspection.
func StartProcess(ctx context.Context, root string, task v1.ProcessTask) error {
	if _, err := processTaskArgv(task); err != nil {
		return err
	}
	dir, err := processDir(root, task.ID)
	if err != nil {
		return err
	}
	if !processID.MatchString(task.Session) {
		return fmt.Errorf("invalid process session")
	}
	if err = os.MkdirAll(filepath.Dir(dir), 0700); err != nil {
		return err
	}
	if err = os.Mkdir(dir, 0700); errors.Is(err, os.ErrExist) {
		return nil
	} else if err != nil {
		return err
	}
	task.State = "starting"
	if err = writeProcessJSON(filepath.Join(dir, "task.json"), task); err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	_, err = tmuxOutput(ctx, "new-session", "-d", "-s", task.Session, "-c", filepath.Join(filepath.Dir(root), "workspace"), "--", exe, "process-run", root, task.ID)
	if err == nil {
		err = ApplyTmuxContext(ctx, root, task.Session)
	}
	return err
}

// cappedOutput drains unlimited child output while retaining only a bounded
// prefix. Serialized writes keep stdout/stderr in the observed order.
type cappedOutput struct {
	mu        sync.Mutex
	file      *os.File
	n         int
	truncated bool
	display   io.Writer
}

func (w *cappedOutput) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n := len(p)
	keep := min(n, ProcessOutputLimit-w.n)
	if keep > 0 {
		if _, err := w.file.Write(p[:keep]); err != nil {
			return 0, err
		}
		w.n += keep
	}
	if keep < n {
		w.truncated = true
	}
	if w.display != nil {
		_, _ = w.display.Write(p)
	}
	return n, nil
}

func RunProcess(root, id string) error {
	dir, err := processDir(root, id)
	if err != nil {
		return err
	}
	claim, err := os.OpenFile(filepath.Join(dir, "claimed"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return fmt.Errorf("execution already claimed or unavailable: %w", err)
	}
	if err = claim.Sync(); err != nil {
		claim.Close()
		return err
	}
	claim.Close()
	b, err := os.ReadFile(filepath.Join(dir, "task.json"))
	if err != nil {
		return err
	}
	var task v1.ProcessTask
	if err = json.Unmarshal(b, &task); err != nil {
		return err
	}
	argv, err := processTaskArgv(task)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	task.StartedAt = &now
	task.State = "running"
	if err = writeProcessJSON(filepath.Join(dir, "result.json"), task); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(dir, "output"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	w := &cappedOutput{file: f, display: os.Stdout}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = filepath.Join(filepath.Dir(root), "workspace")
	cmd.Stdout = w
	cmd.Stderr = w
	setupErr := InstallTools(context.Background(), filepath.Join(filepath.Dir(root), "home"), task.Tools, w)
	if setupErr == nil {
		setupErr = ConfigureToolSetup(context.Background(), filepath.Join(filepath.Dir(root), "home"), task.SetupScript, w)
	}
	if setupErr == nil {
		err = cmd.Run()
	} else {
		err = setupErr
	}
	task.State = "exited"
	if cmd.ProcessState != nil {
		status := cmd.ProcessState.Sys().(syscall.WaitStatus)
		if status.Signaled() {
			task.Signal = int(status.Signal())
		} else {
			code := cmd.ProcessState.ExitCode()
			task.ExitCode = &code
		}
	} else {
		task.State = "launch_failed"
		_, _ = w.Write([]byte("process could not start: " + err.Error() + "\n"))
		if setupErr != nil {
			task.State = "setup_failed"
		}
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	finished := time.Now().UTC()
	task.FinishedAt = &finished
	task.OutputTruncated = w.truncated
	return writeProcessJSON(filepath.Join(dir, "result.json"), task)
}

func ReadProcess(root, id string) (v1.ProcessTask, error) {
	var task v1.ProcessTask
	dir, err := processDir(root, id)
	if err != nil {
		return task, err
	}
	b, err := os.ReadFile(filepath.Join(dir, "result.json"))
	if errors.Is(err, os.ErrNotExist) {
		b, err = os.ReadFile(filepath.Join(dir, "task.json"))
	}
	if err != nil {
		return task, err
	}
	if err = json.Unmarshal(b, &task); err != nil {
		return task, err
	}
	f, err := os.Open(filepath.Join(dir, "output"))
	if err == nil {
		defer f.Close()
		b, err = io.ReadAll(io.LimitReader(f, ProcessOutputLimit))
		task.Output = string(b)
	} else if errors.Is(err, os.ErrNotExist) {
		err = nil
	}
	return task, err
}
