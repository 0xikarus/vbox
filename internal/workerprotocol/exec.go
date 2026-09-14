package workerprotocol

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// Output records keep stderr and process completion distinct from stdout.
// Output is transported, never recorded in the operation journal.
type Output struct {
	Kind     string `json:"kind"`
	Data     []byte `json:"data,omitempty"`
	ExitCode *int   `json:"exitCode,omitempty"`
	Error    string `json:"error,omitempty"`
}

type outputWriter struct {
	mu      *sync.Mutex
	encoder *json.Encoder
	kind    string
}

func (w outputWriter) Write(data []byte) (int, error) {
	total := 0
	for len(data) > 0 {
		n := min(len(data), chunkSize)
		w.mu.Lock()
		err := w.encoder.Encode(Output{Kind: w.kind, Data: data[:n]})
		w.mu.Unlock()
		if err != nil {
			return total, err
		}
		total += n
		data = data[n:]
	}
	return total, nil
}

// Execute handles an already-authenticated command. validate must check the
// binding against authoritative local state; there is deliberately no default
// accepting validator. The handler never retries a command.
func Execute(ctx context.Context, s *Stream, journal Journal, validate func(context.Context, Binding) error) error {
	encoder := json.NewEncoder(s)
	reject := func(reason string) error {
		err := encoder.Encode(Output{Kind: "error", Error: reason})
		if err != nil {
			return err
		}
		return s.CloseWrite()
	}
	if s.Request.Rebind != nil || len(s.Request.Argv) == 0 {
		return reject("worker execution request required")
	}
	if validate == nil {
		return reject("worker assignment validator unavailable")
	}
	if err := validate(ctx, s.Request.Binding); err != nil {
		return reject("worker assignment unavailable or changed")
	}
	if err := journal.Claim(s.Request); err != nil {
		if errors.Is(err, ErrOperationExists) {
			op, inspectErr := journal.Inspect(s.Request)
			if inspectErr != nil {
				return reject("operation already claimed by a different request")
			}
			if op.FinishedAt == nil || op.ExitCode == nil || *op.ExitCode < 0 {
				return reject("operation already claimed; result is unknown")
			}
			// The original output may already have reached the caller and is not
			// journaled because it can contain secrets. Its durable exit status is
			// safe to reconcile without starting another process.
			if err := encoder.Encode(Output{Kind: "exit", ExitCode: op.ExitCode}); err != nil {
				return err
			}
			return s.CloseWrite()
		}
		return reject("worker operation journal unavailable")
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var revoked atomic.Bool
	// Cancel only this operation's process group. Long-lived tmux servers and
	// sessions are managed separately by vmbox-runtime and must survive viewers.
	go func() {
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-s.closed:
				cancel()
				return
			case <-s.peer.ctx.Done():
				cancel()
				return
			case <-runCtx.Done():
				return
			case <-ticker.C:
				if runCtx.Err() != nil {
					return
				}
				if validate(runCtx, s.Request.Binding) != nil {
					if runCtx.Err() != nil {
						return
					}
					revoked.Store(true)
					cancel()
					// Unblock a writer whose client has exhausted stream credit.
					// Revocation cannot wait for the stale viewer to read output.
					s.Close()
					return
				}
			}
		}
	}()
	command := exec.CommandContext(runCtx, s.Request.Argv[0], s.Request.Argv[1:]...)
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error {
		if command.Process == nil {
			return nil
		}
		return syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
	}
	command.WaitDelay = 2 * time.Second
	var mu sync.Mutex
	command.Stdout = outputWriter{mu: &mu, encoder: encoder, kind: "stdout"}
	command.Stderr = outputWriter{mu: &mu, encoder: encoder, kind: "stderr"}
	// StdinPipe avoids os/exec waiting forever for a copier blocked reading a
	// stream after a command that exits without consuming stdin.
	stdin, err := command.StdinPipe()
	if err != nil {
		return reject("worker command input unavailable")
	}
	if err = command.Start(); err != nil {
		stdin.Close()
		// A launch failure is distinct from a process exit; leave the claim unknown.
		return reject("worker command could not start")
	}
	go func() { defer stdin.Close(); io.Copy(stdin, s) }()
	err = command.Wait()
	if revoked.Load() {
		return errors.New("worker assignment changed during operation; reconcile result")
	}
	code := 0
	if err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() < 0 {
			// Do not invent an exit code for signal/transport termination.
			return reject("worker command interrupted; result may require reconciliation")
		}
		code = exit.ExitCode()
	}
	if err = journal.Complete(s.Request, code); err != nil {
		return reject("worker result could not be persisted")
	}
	mu.Lock()
	err = encoder.Encode(Output{Kind: "exit", ExitCode: &code})
	mu.Unlock()
	if err != nil {
		return err
	}
	return s.CloseWrite()
}

// ReadOutput requires an explicit final exit record. EOF without one is never a
// successful command result. Writers receive output immediately with bounded
// memory; callers wanting capture must impose their own capture limit.
func ReadOutput(s io.Reader, stdout, stderr io.Writer) (int, error) {
	reader := bufio.NewReaderSize(s, 64*1024)
	for {
		var event Output
		line, err := reader.ReadSlice('\n')
		if err != nil {
			if errors.Is(err, io.EOF) {
				err = io.ErrUnexpectedEOF
			}
			return 0, err
		}
		if err = json.Unmarshal(line, &event); err != nil {
			return 0, err
		}
		if len(event.Data) > chunkSize {
			return 0, errors.New("oversized worker output record")
		}
		switch event.Kind {
		case "stdout", "stderr":
			writer := stdout
			if event.Kind == "stderr" {
				writer = stderr
			}
			if writer == nil {
				writer = io.Discard
			}
			if _, err := writer.Write(event.Data); err != nil {
				return 0, err
			}
		case "exit":
			if event.ExitCode == nil || *event.ExitCode < 0 {
				return 0, errors.New("invalid worker exit record")
			}
			return *event.ExitCode, nil
		case "error":
			return 0, errors.New("worker command unavailable: " + event.Error)
		default:
			return 0, errors.New("invalid worker output record")
		}
	}
}
