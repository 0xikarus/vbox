package controller

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/0xikarus/vmbox-service/internal/provider"
	providerbootstrap "github.com/0xikarus/vmbox-service/internal/provider/bootstrap"
	"github.com/0xikarus/vmbox-service/internal/workerprotocol"
)

const directWorkerTransport = "controller-worker"

type directWorkerProvider struct {
	provider.Provider
	server                *Server
	accountID, credential string
}

func (p *directWorkerProvider) UsageBatch(ctx context.Context, ids []string) (map[string]provider.Usage, error) {
	if batch, ok := p.Provider.(provider.BatchUsageProvider); ok {
		return batch.UsageBatch(ctx, ids)
	}
	result := make(map[string]provider.Usage, len(ids))
	for _, id := range ids {
		usage, err := p.Provider.Usage(ctx, id)
		if err != nil {
			return nil, err
		}
		result[id] = usage
	}
	return result, nil
}

func (p *directWorkerProvider) resolve(ctx context.Context, serviceID string) (*directWorkerConnection, workerprotocol.Binding, bool, error) {
	w, enrolled, err := p.server.Store.DirectWorkerForService(ctx, p.accountID, p.Name(), p.credential, serviceID)
	if err != nil || !enrolled {
		return nil, workerprotocol.Binding{}, enrolled, err
	}
	p.server.mu.Lock()
	connection := p.server.directWorkers[w.ID]
	p.server.mu.Unlock()
	if connection == nil || connection.Worker.Epoch != w.Epoch || connection.Worker.Incarnation != w.Incarnation {
		return nil, workerprotocol.Binding{}, true, errors.New("worker agent reconnecting")
	}
	select {
	case <-connection.Peer.Done():
		return nil, workerprotocol.Binding{}, true, errors.New("worker connection unavailable")
	default:
	}
	binding, err := p.server.Store.WorkerBinding(ctx, w)
	if err != nil {
		return nil, workerprotocol.Binding{}, true, err
	}
	if err := connection.synchronizeBinding(ctx, binding, func(checkCtx context.Context) error {
		current, enabled, err := p.server.Store.DirectWorkerForService(checkCtx, p.accountID, p.Name(), p.credential, serviceID)
		if err != nil {
			return err
		}
		if !enabled || current.ID != w.ID || current.Epoch != w.Epoch || current.Incarnation != w.Incarnation {
			return errors.New("worker connection ownership changed")
		}
		currentBinding, err := p.server.Store.WorkerBinding(checkCtx, current)
		if err != nil {
			return err
		}
		if currentBinding != binding {
			return errors.New("worker assignment changed")
		}
		return nil
	}); err != nil {
		return nil, workerprotocol.Binding{}, true, err
	}
	return connection, binding, true, nil
}

func (p *directWorkerProvider) Connection(ctx context.Context, id string) (provider.Connection, error) {
	connection, binding, enrolled, err := p.resolve(ctx, id)
	if err != nil {
		return provider.Connection{}, err
	}
	if !enrolled {
		return p.Provider.Connection(ctx, id)
	}
	return provider.Connection{Transport: directWorkerTransport, Endpoint: id, Metadata: map[string]string{
		"workerId": connection.Worker.ID, "accountId": binding.AccountID, "slotId": binding.SlotID, "boxId": binding.BoxID, "assignment": binding.Assignment, "connectionRevision": binding.Incarnation,
	}}, nil
}
func bindingForConnection(conn provider.Connection) workerprotocol.Binding {
	return workerprotocol.Binding{AccountID: conn.Metadata["accountId"], BoxID: conn.Metadata["boxId"], SlotID: conn.Metadata["slotId"], Assignment: conn.Metadata["assignment"], Incarnation: conn.Metadata["connectionRevision"]}
}
func (p *directWorkerProvider) Exec(ctx context.Context, id string, argv []string, opts provider.ExecOptions) (provider.ExecResult, error) {
	_, binding, enrolled, err := p.resolve(ctx, id)
	if err != nil {
		return provider.ExecResult{}, err
	}
	if !enrolled {
		return p.Provider.Exec(ctx, id, argv, opts)
	}
	if opts.Detach {
		argv = append([]string{"vmbox-runtime", "run", "--detach", "--"}, argv...)
		opts.Detach = false
	}
	return p.execute(ctx, id, binding, argv, opts, false)
}
func (p *directWorkerProvider) ExecConnection(ctx context.Context, conn provider.Connection, argv []string, opts provider.ExecOptions) (provider.ExecResult, error) {
	if conn.Transport != directWorkerTransport {
		// A stale legacy connection cannot bypass a slot's explicit migration.
		_, _, enrolled, err := p.resolve(ctx, conn.Endpoint)
		if err != nil {
			return provider.ExecResult{}, err
		}
		if enrolled {
			return provider.ExecResult{}, errors.New("worker transport changed")
		}
		legacy, ok := p.Provider.(provider.ConnectionExecutor)
		if !ok {
			return provider.ExecResult{}, provider.ErrUnsupported
		}
		return legacy.ExecConnection(ctx, conn, argv, opts)
	}
	return p.execute(ctx, conn.Endpoint, bindingForConnection(conn), argv, opts, false)
}
func (p *directWorkerProvider) StreamConnection(ctx context.Context, conn provider.Connection, argv []string, opts provider.ExecOptions) (provider.ExecResult, error) {
	if conn.Transport != directWorkerTransport {
		_, _, enrolled, err := p.resolve(ctx, conn.Endpoint)
		if err != nil {
			return provider.ExecResult{}, err
		}
		if enrolled {
			return provider.ExecResult{}, errors.New("worker transport changed")
		}
		legacy, ok := p.Provider.(provider.ConnectionStreamer)
		if !ok {
			return provider.ExecResult{}, provider.ErrUnsupported
		}
		return legacy.StreamConnection(ctx, conn, argv, opts)
	}
	return p.execute(ctx, conn.Endpoint, bindingForConnection(conn), argv, opts, true)
}

type workerCapture struct{ bytes.Buffer }

func (b *workerCapture) Write(data []byte) (int, error) {
	if b.Len()+len(data) > 16*1024*1024 {
		return 0, errors.New("worker command capture limit exceeded")
	}
	return b.Buffer.Write(data)
}
func (p *directWorkerProvider) execute(ctx context.Context, id string, expected workerprotocol.Binding, argv []string, opts provider.ExecOptions, streaming bool) (provider.ExecResult, error) {
	result := provider.ExecResult{StartedAt: time.Now().UTC()}
	if len(argv) == 0 || opts.Interactive || opts.Detach {
		return result, errors.New("worker transport requires runtime-managed sessions")
	}
	connection, binding, enrolled, err := p.resolve(ctx, id)
	if err != nil {
		return result, err
	}
	if !enrolled || binding != expected {
		return result, errors.New("worker assignment or incarnation changed")
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stream, err := connection.Peer.Open(ctx, workerprotocol.Request{Binding: binding, OperationID: uuid(), Argv: provider.AsWorkloadUser(argv)})
	if err != nil {
		return result, err
	}
	defer stream.Close()
	// Revalidate without holding a transaction for the stream lifetime. Lifecycle
	// mutations will also explicitly invalidate affected connections.
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				stream.Close()
				return
			case <-ticker.C:
				current, next, ok, err := p.resolve(ctx, id)
				if err != nil || !ok || current != connection || next != binding {
					cancel()
					stream.Close()
					return
				}
			}
		}
	}()
	if opts.Stdin == nil {
		if err = stream.CloseWrite(); err != nil {
			return result, err
		}
	} else {
		go func() {
			if _, err := io.Copy(stream, opts.Stdin); err != nil {
				cancel()
				stream.Close()
				return
			}
			_ = stream.CloseWrite()
		}()
	}
	var stdout, stderr workerCapture
	out, errout := opts.Stdout, opts.Stderr
	if !streaming {
		out = &stdout
		errout = &stderr
		if opts.Stdout != nil {
			out = io.MultiWriter(&stdout, opts.Stdout)
		}
		if opts.Stderr != nil {
			errout = io.MultiWriter(&stderr, opts.Stderr)
		}
	}
	result.ExitCode, err = workerprotocol.ReadOutput(stream, out, errout)
	result.FinishedAt = time.Now().UTC()
	result.Stdout = stdout.String()
	result.Stderr = stderr.String()
	if err != nil {
		return result, fmt.Errorf("direct worker execution interrupted: %w", err)
	}
	return result, nil
}

// Preserve provider-owned lifecycle and bootstrap capability checks. Agent
// observations never stand in for fresh volume ownership/attachment evidence.
func (p *directWorkerProvider) AttachedStorage(ctx context.Context, id string) (*provider.Storage, error) {
	v, ok := p.Provider.(provider.AttachedStorageProvider)
	if !ok {
		return nil, provider.ErrUnsupported
	}
	return v.AttachedStorage(ctx, id)
}
func (p *directWorkerProvider) DetachStorage(ctx context.Context, id string, storage provider.Storage) error {
	v, ok := p.Provider.(provider.DetachableStorageProvider)
	if !ok {
		return provider.ErrUnsupported
	}
	return v.DetachStorage(ctx, id, storage)
}
func (p *directWorkerProvider) SanitizeSlot(ctx context.Context, id string) error {
	v, ok := p.Provider.(provider.DetachableStorageProvider)
	if !ok {
		return provider.ErrUnsupported
	}
	return v.SanitizeSlot(ctx, id)
}
func (p *directWorkerProvider) Bootstrap(ctx context.Context, id string, request provider.BootstrapRequest) error {
	_, binding, enrolled, err := p.resolve(ctx, id)
	if err != nil {
		return err
	}
	if enrolled {
		// An enrolled worker already has the workload user and sudo installed.
		// Keep the binding fixed across every bootstrap step and carry assets in
		// stdin. Losing the agent must never restart bootstrap through Railway.
		return providerbootstrap.Install(ctx, request, func(ctx context.Context, argv []string, stdin io.Reader) (provider.ExecResult, error) {
			return p.execute(ctx, id, binding, append([]string{"sudo", "-n", "--"}, argv...), provider.ExecOptions{Stdin: stdin}, false)
		})
	}
	v, ok := p.Provider.(provider.Bootstrapper)
	if !ok {
		return provider.ErrUnsupported
	}
	return v.Bootstrap(ctx, id, request)
}
func (p *directWorkerProvider) Regions(ctx context.Context) ([]provider.Region, error) {
	v, ok := p.Provider.(provider.RegionProvider)
	if !ok {
		return nil, provider.ErrUnsupported
	}
	return v.Regions(ctx)
}

// Connection assignment validation is independent of provider deployment IDs for
// enrolled workers. Legacy connections keep their existing deployment fence.
func connectionMatchesAssignment(conn provider.Connection, a fleetAssignment) bool {
	if conn.Transport == "shared-worker" {
		return conn.Endpoint == a.Slot.ServiceID && conn.Metadata["accountId"] == a.Box.AccountID && conn.Metadata["boxId"] == a.Box.Name && conn.Metadata["workspaceId"] == a.Box.VolumeID && conn.Metadata["deploymentInstanceId"] != "" && conn.Metadata["deploymentInstanceId"] == a.Slot.DeploymentInstanceID
	}
	if conn.Transport == directWorkerTransport {
		return conn.Metadata["accountId"] == a.Box.AccountID && conn.Endpoint == a.Slot.ServiceID && conn.Metadata["boxId"] == a.Box.ID && conn.Metadata["slotId"] == a.Slot.ID && conn.Metadata["assignment"] == nativeFence(a) && conn.Metadata["connectionRevision"] != ""
	}
	return conn.Metadata["deploymentInstanceId"] != "" && conn.Metadata["deploymentInstanceId"] == a.Slot.DeploymentInstanceID
}

func clientWorkerConnection(conn provider.Connection, a fleetAssignment) provider.Connection {
	if conn.Transport != "shared-worker" {
		return conn
	}
	return provider.Connection{Transport: directWorkerTransport, Endpoint: a.Slot.ServiceID, Metadata: map[string]string{
		"accountId": a.Box.AccountID, "boxId": a.Box.ID, "slotId": a.Slot.ID, "assignment": nativeFence(a), "connectionRevision": conn.Metadata["deploymentInstanceId"],
	}}
}

// Agent-mode handoff uses already stored metadata, never a management read for
// cosmetic details. Infrastructure lifecycle callers continue using Inspect.
func connectionDisplayInfo(ctx context.Context, prov provider.Provider, conn provider.Connection, a fleetAssignment) (provider.Box, error) {
	if conn.Transport == directWorkerTransport {
		return provider.Box{ID: a.Slot.ServiceID, Region: a.Slot.Region, Image: a.Slot.Image}, nil
	}
	return prov.Inspect(ctx, a.Slot.ServiceID)
}

func (p *directWorkerProvider) ResourceLimits(ctx context.Context, id string) (provider.Resources, error) {
	v, ok := p.Provider.(provider.ResourceLimitsProvider)
	if !ok {
		return provider.Resources{}, provider.ErrUnsupported
	}
	return v.ResourceLimits(ctx, id)
}
func (p *directWorkerProvider) SetResourceLimits(ctx context.Context, id string, resources provider.Resources) error {
	v, ok := p.Provider.(provider.ResourceLimitsProvider)
	if !ok {
		return provider.ErrUnsupported
	}
	return v.SetResourceLimits(ctx, id, resources)
}

func (p *directWorkerProvider) ObserveInventory(ctx context.Context) (provider.InventoryObservation, error) {
	observer, ok := p.Provider.(provider.InventoryObserver)
	if !ok {
		return provider.InventoryObservation{}, provider.ErrUnsupported
	}
	return observer.ObserveInventory(ctx)
}
