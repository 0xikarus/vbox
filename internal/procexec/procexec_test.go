package procexec

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

func TestOSRunnerUnsetsInheritedEnvironment(t *testing.T) {
	t.Setenv("VMBOX_REMOVE_ME", "inherited")
	runner := OSRunner{Env: map[string]string{"VMBOX_KEEP_ME": "selected"}, Unset: []string{"VMBOX_REMOVE_ME"}}
	result, err := runner.Run(context.Background(), []string{"sh", "-c", `printf '%s|%s' "$VMBOX_REMOVE_ME" "$VMBOX_KEEP_ME"`}, nil, nil, nil)
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("run=%+v err=%v", result, err)
	}
	if got := strings.TrimSpace(string(result.Stdout)); got != "|selected" {
		t.Fatalf("environment=%q", got)
	}
}

func TestRunAttachedWritesDirectlyWithoutCapture(t *testing.T) {
	var stdout bytes.Buffer
	result, err := (OSRunner{}).RunAttached(context.Background(), []string{"sh", "-c", "printf attached"}, nil, &stdout, nil)
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("result=%+v error=%v", result, err)
	}
	if stdout.String() != "attached" || len(result.Stdout) != 0 {
		t.Fatalf("stdout=%q captured=%q", stdout.String(), result.Stdout)
	}
}

func TestOSRunnerCancellationSendsGracefulHangup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cmd, err := (OSRunner{}).command(ctx, []string{"sh", "-c", `trap 'printf H; exit 0' HUP; printf R; while :; do :; done`})
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	ready := make([]byte, 1)
	if _, err := io.ReadFull(stdout, ready); err != nil || string(ready) != "R" {
		t.Fatalf("ready=%q err=%v", ready, err)
	}
	started := time.Now()
	cancel()
	rest, readErr := io.ReadAll(stdout)
	waitErr := cmd.Wait()
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(rest) != "H" {
		t.Fatalf("child did not handle hangup: %q", rest)
	}
	if waitErr != nil && !errors.Is(waitErr, context.Canceled) {
		t.Fatalf("wait error=%v", waitErr)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("cancellation took %s", elapsed)
	}
}
