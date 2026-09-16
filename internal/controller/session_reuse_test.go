package controller

import (
	"testing"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
)

func TestReusableInteractiveSession(t *testing.T) {
	sessions := []v1.Session{{Name: "task-running"}, {Name: "vmbox-desktop"}, {Name: "shell-work"}, {Name: "codex-work"}}
	for _, test := range []struct{ preferred, want string }{
		{"shell-work", "shell-work"},
		{"codex-work", "codex-work"},
		{"missing", "codex-work"},
		{"vmbox-desktop", "codex-work"},
		{"task-running", "codex-work"},
	} {
		if got := reusableInteractiveSession(sessions, test.preferred); got != test.want {
			t.Fatalf("preferred %q: got %q, want %q", test.preferred, got, test.want)
		}
	}
	if got := reusableInteractiveSession([]v1.Session{{Name: "task-complete"}, {Name: "vmbox-desktop"}, {Name: "shell-partial", Partial: true}}, "shell-partial"); got != "" {
		t.Fatalf("reused ineligible session %q", got)
	}
}
