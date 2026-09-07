package controller

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/0xikarus/vmbox-service/internal/procexec"
)

func TestWorkspaceTransportExitDoesNotWaitForMoreBrowserInput(t *testing.T) {
	input, writer, err := workspaceInput()
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	defer input.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := (procexec.OSRunner{}).RunAttached(ctx, []string{"sh", "-c", "exit 0"}, input, io.Discard, io.Discard)
		done <- err
	}()
	// The browser remains connected and sends nothing after tmux detaches.
	// An exited SSH process must still be reaped so the handler can close WS.
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		input.Close()
		cancel()
		<-done
		t.Fatal("exited transport waited for more browser input")
	}
}
