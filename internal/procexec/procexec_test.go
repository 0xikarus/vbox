package procexec

import (
	"bytes"
	"context"
	"strings"
	"testing"
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
