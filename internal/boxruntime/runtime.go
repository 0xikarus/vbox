package boxruntime

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/events"
)

type Runtime struct {
	Root      string
	Heartbeat time.Duration
	Secrets   []string
}

func New(root string) *Runtime {
	if root == "" {
		root = os.Getenv("VMBOX_RUNTIME_DIR")
	}
	if root == "" {
		root = "/data/.vmbox"
	}
	return &Runtime{Root: root, Heartbeat: 10 * time.Second}
}

func ID(prefix string) string {
	var value [12]byte
	if _, err := rand.Read(value[:]); err != nil {
		return prefix + strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return prefix + hex.EncodeToString(value[:])
}

func (r *Runtime) Run(ctx context.Context, argv []string, stdout, stderr io.Writer) (v1.Run, error) {
	if len(argv) == 0 {
		return v1.Run{}, fmt.Errorf("command argv cannot be empty")
	}
	runID := os.Getenv("VMBOX_RUN_ID")
	if runID == "" {
		runID = ID("run_")
	}
	dir := filepath.Join(r.Root, "runs", runID)
	store, err := events.NewStore(dir, r.Secrets)
	if err != nil {
		return v1.Run{}, err
	}
	run := v1.Run{ID: runID, State: v1.JobRunning, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(), StartedAt: ptr(time.Now().UTC()), Request: v1.CreateRunRequest{Command: append([]string(nil), argv...)}}
	if err := store.WriteStatus(run); err != nil {
		return run, err
	}
	if err := os.MkdirAll(r.Root, 0700); err == nil {
		_ = os.WriteFile(filepath.Join(r.Root, "current-run"), []byte(runID+"\n"), 0600)
	}
	_, _ = r.append(store, v1.Event{RunID: runID, Type: "state", State: v1.JobRunning, Message: "command started"})
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Env = runtimeEnvironment(runID, r.Root)
	outPipe, err := cmd.StdoutPipe()
	if err != nil {
		return run, err
	}
	errPipe, err := cmd.StderrPipe()
	if err != nil {
		return run, err
	}
	cmd.Stdin = os.Stdin
	if startErr := cmd.Start(); startErr != nil {
		now := time.Now().UTC()
		exit := 127
		run.State = v1.JobFailed
		run.FinishedAt = &now
		run.UpdatedAt = now
		run.ExitCode = &exit
		run.LastActivity = store.Sanitize(startErr.Error())
		_, _ = r.append(store, v1.Event{RunID: runID, Type: "state", State: run.State, Message: "command failed to start", Timestamp: now})
		if statusErr := store.WriteStatus(run); statusErr != nil {
			return run, errors.Join(startErr, statusErr)
		}
		return run, fmt.Errorf("start command: %w", startErr)
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	wg.Add(2)
	pump := func(stream string, src io.Reader, dst io.Writer) {
		defer wg.Done()
		reader := bufio.NewReaderSize(src, 64*1024)
		for {
			raw, readErr := reader.ReadBytes('\n')
			if len(raw) == 0 && readErr != nil {
				break
			}
			mu.Lock()
			now := time.Now().UTC()
			run.LastOutputAt = &now
			run.LastActivity = store.Sanitize(string(raw))
			run.UpdatedAt = now
			_, _ = r.append(store, v1.Event{RunID: runID, Type: "output", Stream: stream, Message: string(raw), Timestamp: now})
			_ = store.WriteStatus(run)
			if dst != nil {
				_, _ = dst.Write(raw)
			}
			mu.Unlock()
			if readErr != nil {
				break
			}
		}
	}
	go pump("stdout", outPipe, stdout)
	go pump("stderr", errPipe, stderr)
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(r.Heartbeat)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case now := <-ticker.C:
				mu.Lock()
				run.LastHeartbeatAt = &now
				run.UpdatedAt = now
				_, _ = r.append(store, v1.Event{RunID: runID, Type: "heartbeat", Timestamp: now})
				_ = store.WriteStatus(run)
				mu.Unlock()
			}
		}
	}()
	wg.Wait()
	waitErr := cmd.Wait()
	close(done)
	exit := 0
	if waitErr != nil {
		var ee *exec.ExitError
		if errors.As(waitErr, &ee) {
			exit = ee.ExitCode()
		} else {
			exit = 127
		}
	}
	now := time.Now().UTC()
	mu.Lock()
	run.FinishedAt = &now
	run.UpdatedAt = now
	run.ExitCode = &exit
	if exit == 0 {
		run.State = v1.JobSucceeded
	} else {
		run.State = v1.JobFailed
	}
	_, _ = r.append(store, v1.Event{RunID: runID, Type: "state", State: run.State, Message: fmt.Sprintf("command exited with status %d", exit), Timestamp: now})
	err = store.WriteStatus(run)
	mu.Unlock()
	return run, err
}

func (r *Runtime) RunDetached(argv []string) (string, error) {
	if len(argv) == 0 {
		return "", fmt.Errorf("command argv cannot be empty")
	}
	runID := os.Getenv("VMBOX_RUN_ID")
	if runID == "" {
		runID = ID("run_")
	}
	if store, err := r.StoreFor(runID); err == nil {
		if _, err := store.ReadStatus(); err == nil {
			return runID, nil
		}
	}
	executable, err := os.Executable()
	if err != nil {
		return "", err
	}
	args := []string{"run", "--internal-background", "--"}
	args = append(args, argv...)
	cmd := exec.Command(executable, args...)
	cmd.Env = runtimeEnvironment(runID, r.Root)
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return "", err
	}
	return runID, cmd.Process.Release()
}

func runtimeEnvironment(runID, root string) []string {
	result := make([]string, 0, len(os.Environ())+2)
	for _, item := range os.Environ() {
		key, _, ok := strings.Cut(item, "=")
		if ok && (key == "VMBOX_RUN_ID" || key == "VMBOX_RUNTIME_DIR") {
			continue
		}
		result = append(result, item)
	}
	return append(result, "VMBOX_RUN_ID="+runID, "VMBOX_RUNTIME_DIR="+root)
}

func (r *Runtime) CurrentRunID() (string, error) {
	data, err := os.ReadFile(filepath.Join(r.Root, "current-run"))
	if err != nil {
		return "", err
	}
	id := strings.TrimSpace(string(data))
	if id == "" {
		return "", fmt.Errorf("no active run")
	}
	return id, nil
}
func (r *Runtime) StoreFor(runID string) (*events.Store, error) {
	if runID == "" {
		var err error
		runID, err = r.CurrentRunID()
		if err != nil {
			return nil, err
		}
	}
	return events.NewStore(filepath.Join(r.Root, "runs", runID), r.Secrets)
}

func (r *Runtime) Report(runID, kind, message string) error {
	store, err := r.StoreFor(runID)
	if err != nil {
		return err
	}
	run, err := store.ReadStatus()
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	run.UpdatedAt = now
	run.LastActivity = message
	switch kind {
	case "needs_input":
		run.State = v1.JobNeedsInput
	case "resumed":
		run.State = v1.JobRunning
	case "success":
		run.State = v1.JobSucceeded
	case "failed":
		run.State = v1.JobFailed
	}
	_, err = r.append(store, v1.Event{RunID: run.ID, Type: kind, State: run.State, Message: message, Timestamp: now})
	if err != nil {
		return err
	}
	return store.WriteStatus(run)
}

func (r *Runtime) append(store *events.Store, event v1.Event) (v1.Event, error) {
	stored, err := store.Append(event)
	if err != nil {
		return stored, err
	}
	_ = r.push(stored)
	return stored, nil
}
func (r *Runtime) push(event v1.Event) error {
	controller := strings.TrimRight(os.Getenv("VMBOX_CONTROLLER_URL"), "/")
	key := os.Getenv("VMBOX_EVENT_KEY")
	if controller == "" || key == "" || event.RunID == "" {
		return nil
	}
	var err error
	event.Signature, err = events.Sign([]byte(key), event)
	if err != nil {
		return err
	}
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, controller+"/v1/runs/"+event.RunID+"/events", bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "VMBox "+event.RunID+"."+key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("controller event: %s", resp.Status)
	}
	return nil
}

func (r *Runtime) Ask(ctx context.Context, runID, prompt string, timeout time.Duration) (string, error) {
	if runID == "" {
		var err error
		runID, err = r.CurrentRunID()
		if err != nil {
			return "", err
		}
	}
	questionID := ID("q_")
	store, err := r.StoreFor(runID)
	if err != nil {
		return "", err
	}
	question := v1.Question{ID: questionID, RunID: runID, Prompt: store.Sanitize(prompt), State: "open", Blocking: true, AskedAt: time.Now().UTC()}
	data, _ := json.Marshal(question)
	qdir := filepath.Join(r.Root, "runs", runID, "questions")
	if err := os.MkdirAll(qdir, 0700); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(qdir, questionID+".json"), data, 0600); err != nil {
		return "", err
	}
	run, err := store.ReadStatus()
	if err != nil {
		return "", err
	}
	run.State = v1.JobNeedsInput
	run.UpdatedAt = time.Now().UTC()
	eventData, _ := json.Marshal(map[string]string{"questionId": questionID})
	if _, err := r.append(store, v1.Event{RunID: runID, Type: "needs_input", State: v1.JobNeedsInput, Message: prompt, Data: eventData, Timestamp: run.UpdatedAt}); err != nil {
		return "", err
	}
	if err := store.WriteStatus(run); err != nil {
		return "", err
	}
	var deadline <-chan time.Time
	if timeout > 0 {
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		deadline = timer.C
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-deadline:
			return "", fmt.Errorf("question timed out")
		case <-ticker.C:
			if answer, ok, err := r.controllerAnswer(ctx, runID, questionID); err == nil && ok {
				_ = r.Report(runID, "resumed", "answer received")
				return answer, nil
			}
			data, err := os.ReadFile(filepath.Join(qdir, questionID+".answer"))
			if err == nil {
				answer := strings.TrimSpace(string(data))
				_ = r.Report(runID, "resumed", "answer received")
				return answer, nil
			}
			if !errors.Is(err, os.ErrNotExist) {
				return "", err
			}
		}
	}
}

func (r *Runtime) controllerAnswer(parent context.Context, runID, questionID string) (string, bool, error) {
	controller := strings.TrimRight(os.Getenv("VMBOX_CONTROLLER_URL"), "/")
	key := os.Getenv("VMBOX_EVENT_KEY")
	if controller == "" || key == "" {
		return "", false, nil
	}
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, controller+"/v1/runs/"+runID+"/questions/"+questionID+"/answer", nil)
	if err != nil {
		return "", false, err
	}
	req.Header.Set("Authorization", "VMBox "+runID+"."+key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return "", false, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", false, fmt.Errorf("controller answer: %s", resp.Status)
	}
	var question v1.Question
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&question); err != nil {
		return "", false, err
	}
	return question.Answer, question.State == "answered", nil
}

func DecodeArgv(encoded string) ([]string, error) {
	data, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return nil, err
	}
	var argv []string
	if err := json.Unmarshal(data, &argv); err != nil {
		return nil, err
	}
	if len(argv) == 0 {
		return nil, fmt.Errorf("empty encoded argv")
	}
	return argv, nil
}
func ptr[T any](value T) *T { return &value }
