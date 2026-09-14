package workerprotocol

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func TestExecutePreservesArgvStdinStderrAndExit(t *testing.T) {
	a, b := pair(t)
	req := request("exec-disposable")
	req.Argv = []string{"sh", "-c", `printf '%s' "$1"; cat; printf 'problem' >&2; exit 7`, "sh", "literal; $(false)"}
	out, err := a.Open(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	in, err := b.Accept(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	j := Journal{Directory: t.TempDir()}
	go func() {
		done <- Execute(context.Background(), in, j, func(_ context.Context, got Binding) error {
			if got != req.Binding {
				return errors.New("wrong binding")
			}
			return nil
		})
	}()
	out.Write([]byte("input"))
	out.CloseWrite()
	var stdout, stderr bytes.Buffer
	code, err := ReadOutput(out, &stdout, &stderr)
	if err != nil || code != 7 || stdout.String() != "literal; $(false)input" || stderr.String() != "problem" {
		t.Fatalf("result %d %q %q %v", code, stdout.String(), stderr.String(), err)
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	op, err := j.Inspect(req)
	if err != nil || op.ExitCode == nil || *op.ExitCode != 7 {
		t.Fatalf("result journal %+v %v", op, err)
	}
}
func TestRejectedBindingDoesNotExecuteOrClaim(t *testing.T) {
	a, b := pair(t)
	dir := t.TempDir()
	marker := filepath.Join(dir, "must-not-exist")
	req := request("reject")
	req.Argv = []string{"touch", marker}
	out, err := a.Open(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	in, err := b.Accept(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	j := Journal{Directory: filepath.Join(dir, "journal")}
	go Execute(context.Background(), in, j, func(context.Context, Binding) error { return errors.New("stale assignment") })
	out.CloseWrite()
	if _, err = ReadOutput(out, nil, nil); err == nil {
		t.Fatal("accepted stale assignment")
	}
	if _, err = os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("command executed")
	}
	if _, err = j.Inspect(req); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("rejection claimed operation")
	}
}

func TestCompletedOperationReconcilesWithoutReplayAfterAgentRestart(t *testing.T) {
	a, b := pair(t)
	dir := t.TempDir()
	marker := filepath.Join(dir, "runs")
	req := request("lost-final-frame")
	req.Argv = []string{"sh", "-c", `printf x >> "$1"`, "sh", marker}
	j := Journal{Directory: filepath.Join(dir, "journal")}
	if err := j.Claim(req); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := j.Complete(req, 19); err != nil {
		t.Fatal(err)
	}
	req.Binding.Incarnation = "replacement-agent"
	out, err := a.Open(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	in, err := b.Accept(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	go Execute(context.Background(), in, j, func(_ context.Context, got Binding) error {
		if got != req.Binding {
			return errors.New("wrong restarted binding")
		}
		return nil
	})
	out.CloseWrite()
	code, err := ReadOutput(out, nil, nil)
	if err != nil || code != 19 {
		t.Fatalf("reconciled result %d: %v", code, err)
	}
	data, err := os.ReadFile(marker)
	if err != nil || string(data) != "x" {
		t.Fatalf("completed command replayed: %q %v", data, err)
	}
}

func TestIncompleteOperationRemainsAmbiguousAndNeverReplays(t *testing.T) {
	a, b := pair(t)
	dir := t.TempDir()
	marker := filepath.Join(dir, "must-not-run")
	req := request("ambiguous-after-disconnect")
	req.Argv = []string{"touch", marker}
	j := Journal{Directory: filepath.Join(dir, "journal")}
	if err := j.Claim(req); err != nil {
		t.Fatal(err)
	}
	req.Binding.Incarnation = "replacement-agent"
	out, err := a.Open(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	in, err := b.Accept(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	go Execute(context.Background(), in, j, func(context.Context, Binding) error { return nil })
	out.CloseWrite()
	if _, err := ReadOutput(out, nil, nil); err == nil || !strings.Contains(err.Error(), "result is unknown") {
		t.Fatalf("ambiguous claim was not reported: %v", err)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("ambiguous command replayed")
	}
}

func TestOutputRejectsMissingExitAndOversizedRecords(t *testing.T) {
	for _, data := range [][]byte{
		[]byte("{\"kind\":\"stdout\",\"data\":\"aGk=\"}\n"),
		append(bytes.Repeat([]byte(" "), 128*1024), '\n'),
	} {
		if _, err := ReadOutput(bytes.NewReader(data), nil, nil); err == nil {
			t.Fatal("accepted incomplete or oversized output")
		}
	}
}

func TestAssignmentRevocationCancelsOnlyItsOperation(t *testing.T) {
	for _, command := range []string{"sleep 30", "cat /dev/zero"} {
		t.Run(command, func(t *testing.T) {
			a, b := pair(t)
			// This unrelated process represents a surviving workspace process.
			// It is created solely for this test, never a user worker process.
			sibling := exec.Command("sleep", "30")
			if err := sibling.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = sibling.Process.Kill(); _ = sibling.Wait() })
			dir := t.TempDir()
			marker := filepath.Join(dir, "started")
			req := request("revoked-operation")
			req.Argv = []string{"sh", "-c", `printf ready > "$1"; ` + command, "sh", marker}
			out, err := a.Open(context.Background(), req)
			if err != nil {
				t.Fatal(err)
			}
			defer out.Close()
			in, err := b.Accept(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			var revoked atomic.Bool
			journal := Journal{Directory: filepath.Join(dir, "journal")}
			done := make(chan error, 1)
			go func() {
				done <- Execute(context.Background(), in, journal, func(context.Context, Binding) error {
					if revoked.Load() {
						return errors.New("assignment changed")
					}
					return nil
				})
			}()
			deadline := time.Now().Add(time.Second)
			for {
				if _, err := os.Stat(marker); err == nil {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("disposable command never started")
				}
				time.Sleep(5 * time.Millisecond)
			}
			// Do not consume output: revocation must also unblock exhausted credit.
			revoked.Store(true)
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("revocation reported success")
				}
			case <-time.After(2 * time.Second):
				t.Fatal("revocation waited for client output consumption")
			}
			if err := sibling.Process.Signal(syscall.Signal(0)); err != nil {
				t.Fatal("revocation killed unrelated process")
			}
			op, err := journal.Inspect(req)
			if err != nil || op.ExitCode != nil || op.FinishedAt != nil {
				t.Fatalf("revoked operation acquired a false completion: %+v %v", op, err)
			}
			if err := journal.Claim(req); !errors.Is(err, ErrOperationExists) {
				t.Fatalf("revoked operation can replay: %v", err)
			}
		})
	}
}
