package boxruntime

import (
	"context"
	"strings"
	"testing"
)

func TestMascotSamplesOnlyManagedSessionAndBoundsText(t *testing.T) {
	original := tmuxCommand
	t.Cleanup(func() { tmuxCommand = original })
	tmuxCommand = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		switch args[0] {
		case "list-sessions":
			return []byte("vmbox-internal-mcp-http\nplain-shell\ncodex-good\nclaude-good\n"), nil
		case "show-environment":
			if args[2] == "=codex-good" {
				return []byte(taskAgentEnvironment + "=codex"), nil
			}
			if args[2] == "=claude-good" {
				return []byte(taskAgentEnvironment + "=claude"), nil
			}
			return []byte(taskAgentEnvironment + "=shell"), nil
		case "capture-pane":
			if args[len(args)-1] != "=codex-good:0.0" {
				t.Fatalf("captured wrong pane: %v", args)
			}
			return []byte(strings.Repeat("x", 9000) + "\nFixed it. Tests passed.\n"), nil
		}
		return nil, nil
	}
	sessions := mascotSessions(context.Background())
	if strings.Join(sessions, ",") != "codex-good,claude-good" {
		t.Fatalf("sessions=%v", sessions)
	}
	sample, err := mascotSample(context.Background(), "codex-good")
	if err != nil || len(sample) > mascotSampleBytes || !strings.HasSuffix(sample, "Tests passed.") {
		t.Fatalf("sample length=%d err=%v", len(sample), err)
	}
}
