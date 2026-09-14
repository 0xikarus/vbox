package workeragent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/0xikarus/vmbox-service/internal/workerprotocol"
)

// replaceBinding is a controller-authenticated compare-and-swap. It modifies
// only the private agent binding, never volumes, runtime sessions or processes.
// Active command handlers observe the change and revoke their own streams.
func (a *Agent) replaceBinding(ctx context.Context, expected, next workerprotocol.Binding) error {
	a.bindingMu.Lock()
	defer a.bindingMu.Unlock()
	for _, binding := range []workerprotocol.Binding{expected, next} {
		if binding.AccountID != a.Config.AccountID || binding.SlotID != a.Config.SlotID || binding.Incarnation != a.Incarnation || binding.BoxID == "" || binding.Assignment == "" {
			return errors.New("invalid assignment transition scope")
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// An acknowledgement may have been lost. Repeating an already-applied
	// desired state is safe and does not re-execute any workspace command.
	if a.validateBinding(ctx, next) == nil {
		return nil
	}
	if err := a.validateBinding(ctx, expected); err != nil {
		return errors.New("assignment transition expected binding changed")
	}
	dir := filepath.Dir(a.Config.BindingFile)
	file, err := os.CreateTemp(dir, ".worker-binding-")
	if err != nil {
		return err
	}
	name := file.Name()
	defer os.Remove(name)
	defer file.Close()
	if err = json.NewEncoder(file).Encode(next); err != nil {
		return err
	}
	if err = file.Sync(); err != nil {
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = os.Rename(name, a.Config.BindingFile); err != nil {
		return err
	}
	parent, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer parent.Close()
	return parent.Sync()
}

func (a *Agent) rebindStream(ctx context.Context, stream *workerprotocol.Stream) {
	event := workerprotocol.Output{Kind: "error", Error: "worker assignment transition rejected"}
	if len(stream.Request.Argv) == 0 && stream.Request.Rebind != nil {
		if a.replaceBinding(ctx, stream.Request.Binding, *stream.Request.Rebind) == nil {
			code := 0
			event = workerprotocol.Output{Kind: "exit", ExitCode: &code}
		}
	}
	_ = json.NewEncoder(stream).Encode(event)
	_ = stream.CloseWrite()
}
