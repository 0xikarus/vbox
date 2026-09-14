package workeragent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"strings"
	"syscall"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/provider"
	"github.com/0xikarus/vmbox-service/internal/workerprotocol"
)

type observationBuffer struct{ bytes.Buffer }

func (b *observationBuffer) Write(data []byte) (int, error) {
	if b.Len()+len(data) > 32*1024 {
		return 0, errors.New("runtime observation limit exceeded")
	}
	return b.Buffer.Write(data)
}
func (a *Agent) observeRuntime(ctx context.Context, binding workerprotocol.Binding) json.RawMessage {
	if strings.HasPrefix(binding.BoxID, "compute-slot:") {
		return nil
	}
	readCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	argv := provider.AsWorkloadUser([]string{"vmbox-runtime", "native-sessions", binding.Assignment})
	command := exec.CommandContext(readCtx, argv[0], argv[1:]...)
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error { return syscall.Kill(-command.Process.Pid, syscall.SIGKILL) }
	command.WaitDelay = time.Second
	var output observationBuffer
	command.Stdout = &output
	if command.Run() != nil {
		return nil
	}
	var inventory v1.SessionInventory
	if json.Unmarshal(output.Bytes(), &inventory) != nil || inventory.Assignment != binding.Assignment || inventory.Sessions == nil || len(inventory.Sessions) > 64 {
		return nil
	}
	// A changed assignment during collection makes the snapshot stale immediately.
	if a.validateBinding(ctx, binding) != nil {
		return nil
	}
	return append(json.RawMessage(nil), output.Bytes()...)
}
func (a *Agent) observations(ctx context.Context, peer *workerprotocol.Peer) {
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()
	var sequence uint64
	for {
		binding, err := a.localBinding(ctx)
		if err == nil && a.validateBinding(ctx, binding) == nil {
			sequence++
			observation := workerprotocol.Observation{Sequence: sequence, Binding: binding, Runtime: a.observeRuntime(ctx, binding)}
			if err := peer.PublishObservation(observation); err != nil {
				peer.Close()
				return
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-peer.Done():
			return
		case <-ticker.C:
		}
	}
}
