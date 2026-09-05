package boxruntime

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// This test uses a real, private tmux server, never an existing user socket.
func TestNativeSessionsRealTmux(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("native tmux unavailable")
	}
	t.Setenv("TMUX_TMPDIR", t.TempDir())
	t.Setenv("TMUX", "")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	t.Cleanup(func() { _, _ = tmuxOutput(context.Background(), "kill-server") })
	fence := strings.Repeat("a", 64)
	if err := SetNativeAssignment(ctx, fence); err != nil {
		t.Fatal(err)
	}
	inv, err := NativeSessions(ctx, fence)
	if err != nil {
		t.Fatal(err)
	}
	if len(inv.Sessions) != 0 {
		t.Fatalf("unexpected sessions: %+v", inv)
	}
	for _, name := range []string{"test", "test-other", "space ü"} {
		if _, err := tmuxOutput(ctx, "new-session", "-d", "-s", name, "sleep 30"); err != nil {
			t.Fatal(err)
		}
	}
	inv, err = NativeSessions(ctx, fence)
	if err != nil {
		t.Fatal(err)
	}
	if len(inv.Sessions) != 3 {
		t.Fatalf("sessions=%+v", inv)
	}
	var id, inc string
	for _, s := range inv.Sessions {
		if s.Name == "test" {
			id = s.ID
			inc = s.Incarnation
		}
	}
	if _, err := tmuxOutput(ctx, "kill-session", "-t", id); err != nil {
		t.Fatal(err)
	}
	if _, err := tmuxOutput(ctx, "new-session", "-d", "-s", "test", "sleep 30"); err != nil {
		t.Fatal(err)
	}
	after, err := NativeSessions(ctx, fence)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range after.Sessions {
		if s.Name == "test" && s.Incarnation == inc {
			t.Fatal("recreated session reused identity")
		}
	}
	if _, err := NativeSessions(ctx, strings.Repeat("b", 64)); err == nil {
		t.Fatal("stale assignment accepted")
	}
	if err := NativeAttach(ctx, fence, "test", inc); err == nil {
		t.Fatal("name accepted instead of exact ID")
	}
	old := after.Sessions[0]
	if _, err = tmuxOutput(ctx, "kill-server"); err != nil {
		t.Fatal(err)
	}
	// kill-server can return before the old daemon releases its socket.
	// Retry only this idempotent test setup, never attached terminal input.
	for attempt := 0; attempt < 20; attempt++ {
		err = SetNativeAssignment(ctx, fence)
		if err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tmuxOutput(ctx, "new-session", "-d", "-s", old.Name, "sleep 30"); err != nil {
		t.Fatal(err)
	}
	if err = NativeAttach(ctx, fence, old.ID, old.Incarnation); err == nil {
		t.Fatal("old server incarnation accepted after re-enable")
	}
}
