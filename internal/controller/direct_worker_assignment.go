package controller

import (
	"context"
	"errors"
	"time"

	"github.com/0xikarus/vmbox-service/internal/workerprotocol"
)

// synchronizeBinding serializes assignment changes on one authenticated peer.
// The supplied authority check reads the controller database; agent claims are
// never authority to select a box or an assignment.
func (c *directWorkerConnection) synchronizeBinding(ctx context.Context, desired workerprotocol.Binding, authoritative func(context.Context) error) error {
	c.assignmentMu.Lock()
	defer c.assignmentMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if !c.bindingControl {
		// Earlier agents continue enforcing their immutable local binding. They
		// cannot silently adopt a new assignment through this compatibility path.
		return nil
	}
	if authoritative == nil {
		return errors.New("worker assignment authority unavailable")
	}
	if err := authoritative(ctx); err != nil {
		return err
	}
	if c.binding == desired {
		return nil
	}
	if desired.AccountID != c.Worker.AccountID || desired.SlotID != c.Worker.SlotID || desired.Incarnation != c.Worker.Incarnation || desired.BoxID == "" || desired.Assignment == "" {
		return errors.New("worker assignment scope changed")
	}
	updateCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	stream, err := c.Peer.Open(updateCtx, workerprotocol.Request{Binding: c.binding, OperationID: uuid(), Rebind: &desired})
	if err != nil {
		return err
	}
	defer stream.Close()
	// ReadOutput has no context argument; close the stream on cancellation,
	// including a missing acknowledgement while the peer remains connected.
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-updateCtx.Done():
			stream.Close()
		case <-done:
		}
	}()
	if err = stream.CloseWrite(); err != nil {
		c.Peer.Close()
		return err
	}
	code, err := workerprotocol.ReadOutput(stream, nil, nil)
	if err != nil || code != 0 {
		// The agent may have applied the update. Reconnect to observe its local
		// binding before choosing another transition; never guess the old state.
		c.Peer.Close()
		return errors.New("worker assignment acknowledgement unavailable")
	}
	// Preserve the acknowledged local state even if the database changed while
	// awaiting it. The next resolver can CAS from that state to the new binding.
	c.binding = desired
	return authoritative(ctx)
}
