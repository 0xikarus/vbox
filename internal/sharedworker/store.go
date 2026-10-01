package sharedworker

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"sync"
	"syscall"
	"time"

	"github.com/0xikarus/vmbox-service/internal/provider"
)

type Workspace struct {
	ID        string         `json:"id"`
	Owner     provider.Owner `json:"owner"`
	UID       int            `json:"uid"`
	Display   int            `json:"display"`
	SizeGiB   int64          `json:"sizeGiB"`
	MemoryGiB int64          `json:"memoryGiB,omitempty"`
	SwapGiB   *int64         `json:"swapGiB,omitempty"`
	// CPU is the container CPU limit; zero means the original fixed 1 CPU.
	CPU float64 `json:"cpu,omitempty"`
}

type Slot struct {
	Identity    string         `json:"identity"`
	Name        string         `json:"name"`
	Owner       provider.Owner `json:"owner"`
	WorkspaceID string         `json:"workspaceId,omitempty"`
	Revision    uint64         `json:"revision"`
	State       provider.State `json:"state"`
}

type State struct {
	HostID     string               `json:"hostId"`
	AccountID  string               `json:"accountId"`
	NextUID    int                  `json:"nextUid"`
	Slots      map[string]Slot      `json:"slots"`
	Workspaces map[string]Workspace `json:"workspaces"`
	// Settings are owner-tunable through the controller. They are seeded once
	// from the startup capacity and are authoritative afterwards.
	Settings *provider.WorkerSettings `json:"settings,omitempty"`
}

type Runtime interface {
	Prepare(context.Context, Workspace) error
	Stop(context.Context, Workspace) error
	Delete(context.Context, Workspace) error
}

type Store struct {
	mu          sync.Mutex
	diskMu      sync.Mutex
	diskCache   map[string]diskObservation
	Root        string
	Incarnation string
	Runtime     Runtime
	// Specs reads the machine's specs; tests replace it.
	Specs   func() (provider.WorkerSpecs, error)
	state   State
	lock    *os.File
	failure error
}

type diskObservation struct {
	used              *int64
	observedAt        time.Time
	startedAt         time.Time
	running           bool
	unavailableReason string
}

func NewID() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(value[:])
}

// Open opens the data root. capacity seeds the slot count the first time the
// worker starts (zero selects one slot); persisted settings win afterwards.
func Open(root, account string, capacity int, runtime Runtime) (*Store, error) {
	if !filepath.IsAbs(root) || filepath.Clean(root) != root || root == "/" || account == "" || capacity < 0 || capacity > maxWorkerSlots || runtime == nil {
		return nil, fmt.Errorf("shared worker requires an absolute data root, account, runtime and at most %d slots", maxWorkerSlots)
	}
	store := &Store{Root: root, Runtime: runtime, Incarnation: NewID()}
	store.Specs = func() (provider.WorkerSpecs, error) { return MachineSpecs(root) }
	if err := os.MkdirAll(filepath.Join(root, ".shared-worker"), 0700); err != nil {
		return nil, err
	}
	info, err := os.Lstat(filepath.Join(root, ".shared-worker"))
	if err != nil {
		return nil, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.IsDir() || info.Mode().Perm() != 0700 || stat.Uid != uint32(os.Geteuid()) {
		return nil, errors.New("shared worker metadata directory must be private and supervisor-owned")
	}
	lock, err := os.OpenFile(filepath.Join(root, ".shared-worker", "lock"), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lock.Close()
		return nil, errors.New("shared worker data root is already in use")
	}
	store.lock = lock
	opened := false
	defer func() {
		if !opened {
			store.Close()
		}
	}()
	data, err := os.ReadFile(store.statePath())
	if errors.Is(err, os.ErrNotExist) {
		store.state = State{HostID: NewID(), AccountID: account, NextUID: 30000, Slots: map[string]Slot{}, Workspaces: map[string]Workspace{}}
		store.state.Settings = seedSettings(capacity, 0)
		if err := store.save(); err != nil {
			return nil, err
		}
		opened = true
		return store, nil
	}
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(data, &store.state); err != nil {
		return nil, errors.New("invalid shared worker state")
	}
	if store.state.Settings == nil {
		if capacity != 0 && len(store.state.Slots) > capacity {
			return nil, errors.New("shared worker identity or capacity does not match retained state")
		}
		store.state.Settings = seedSettings(capacity, len(store.state.Slots))
		if err := store.save(); err != nil {
			return nil, err
		}
	}
	if store.state.AccountID != account || store.state.HostID == "" || store.state.NextUID < 30000 || store.state.Slots == nil || store.state.Workspaces == nil || len(store.state.Slots) > store.state.Settings.Slots {
		return nil, errors.New("shared worker identity or capacity does not match retained state")
	}
	identities := map[int]bool{}
	for id, workspace := range store.state.Workspaces {
		if err := validateWorkspace(workspace); err != nil {
			return nil, err
		}
		if id != workspace.ID || workspace.Owner.AccountID != account || workspace.Owner.BoxID == "" || workspace.UID >= store.state.NextUID || identities[workspace.UID] {
			return nil, errors.New("invalid retained workspace")
		}
		identities[workspace.UID] = true
	}
	attachments := map[string]bool{}
	for name, slot := range store.state.Slots {
		if err := provider.ValidateName(name); err != nil {
			return nil, err
		}
		if slot.Identity == "" || slot.Name != name || slot.Owner.AccountID != account || slot.Owner.BoxID == "" || slot.Revision == 0 {
			return nil, errors.New("invalid retained slot")
		}
		if slot.WorkspaceID != "" {
			if _, exists := store.state.Workspaces[slot.WorkspaceID]; !exists || attachments[slot.WorkspaceID] {
				return nil, errors.New("invalid retained attachment")
			}
			attachments[slot.WorkspaceID] = true
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	for _, workspace := range store.state.Workspaces {
		if linux, ok := store.Runtime.(*LinuxRuntime); ok && linux.Container != nil {
			active := false
			for _, slot := range store.state.Slots {
				if slot.WorkspaceID == workspace.ID && slot.State == provider.StateRunning {
					active = true
				}
			}
			if !active {
				if err := linux.Container.Stop(ctx, workspace); err != nil {
					return nil, err
				}
				continue
			}
		}
		if err := store.Runtime.Prepare(ctx, workspace); err != nil {
			return nil, fmt.Errorf("recover workspace %s: %w", workspace.ID, err)
		}
	}
	opened = true
	return store, nil
}

func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failure = errors.New("shared worker store is closed")
	if s.lock == nil {
		return nil
	}
	err := s.lock.Close()
	s.lock = nil
	return err
}

func (s *Store) statePath() string { return filepath.Join(s.Root, ".shared-worker", "state.json") }

func (s *Store) save() (err error) {
	if s.failure != nil {
		return s.failure
	}
	defer func() {
		if err != nil {
			s.failure = errors.New("shared worker state persistence failed; restart required")
		}
	}()
	file, err := os.CreateTemp(filepath.Dir(s.statePath()), ".state-")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if err = json.NewEncoder(file).Encode(s.state); err != nil {
		return err
	}
	if err = file.Sync(); err != nil {
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	if err = os.Rename(file.Name(), s.statePath()); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(s.statePath()))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

// isolationStatus reports the tier actually in force. Runtimes that cannot
// report a status are treated as the legacy uid tier, never as isolated.
func (s *Store) isolationStatus() IsolationStatus {
	if reporter, ok := s.Runtime.(interface{ IsolationStatus() IsolationStatus }); ok {
		return reporter.IsolationStatus()
	}
	return SelectIsolation(IsolationModeUID, Capabilities{})
}

func (s *Store) connection(slot Slot) provider.Connection {
	workspace := s.state.Workspaces[slot.WorkspaceID]
	metadata := map[string]string{
		"deploymentInstanceId": fmt.Sprintf("%s:%s:%s:%d", s.state.HostID, s.Incarnation, slot.Identity, slot.Revision),
		"hostId":               s.state.HostID, "workspaceId": slot.WorkspaceID,
		"accountId": s.state.AccountID, "boxId": workspace.Owner.BoxID,
	}
	for key, value := range s.isolationStatus().Metadata() {
		metadata[key] = value
	}
	if s.isolationStatus().Tier == IsolationTierContainer && slot.WorkspaceID != "" {
		memoryGiB, swapGiB, _ := containerMemoryLimits(workspace)
		metadata["resource.memoryBytes"] = fmt.Sprintf("%d", memoryGiB<<30)
		metadata["resource.swapBytes"] = fmt.Sprintf("%d", swapGiB<<30)
	}
	return provider.Connection{Transport: "shared-worker", Endpoint: slot.Name, Metadata: metadata}
}

func (s *Store) box(slot Slot) provider.Box {
	box := provider.Box{ID: slot.Name, Name: slot.Name, Provider: "shared-worker", State: slot.State, Owner: slot.Owner, Connection: s.connection(slot), Region: "shared", Image: "shared-worker"}
	status := s.isolationStatus()
	box.Labels = map[string]string{"isolation.tier": string(status.Tier), "isolation.mode": string(status.Mode)}
	if workspace, exists := s.state.Workspaces[slot.WorkspaceID]; exists {
		storage := storageFor(workspace)
		box.Storage = &storage
	}
	return box
}

func storageFor(workspace Workspace) provider.Storage {
	return provider.Storage{ID: workspace.ID, Name: workspace.ID, MountPath: "/data", SizeGiB: workspace.SizeGiB}
}

func (s *Store) ResourceLimits(ctx context.Context, id string) (provider.Resources, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	linux, ok := s.Runtime.(*LinuxRuntime)
	if !ok || linux.Container == nil {
		return provider.Resources{}, provider.ErrUnsupported
	}
	slot, exists := s.state.Slots[id]
	if !exists || slot.WorkspaceID == "" {
		return provider.Resources{}, provider.ErrNotFound
	}
	w := s.state.Workspaces[slot.WorkspaceID]
	memoryGiB, swapGiB, err := containerMemoryLimits(w)
	if err != nil {
		return provider.Resources{}, err
	}
	resources := provider.Resources{CPU: containerCPU(w), MemoryMiB: memoryGiB * 1024, SwapMiB: swapGiB * 1024, DiskGiB: w.SizeGiB}
	current, err := linux.Container.inspect(ctx, w)
	if err != nil {
		return provider.Resources{}, err
	}
	if current == nil || !current.State.Running || current.HostConfig.Memory <= 0 || current.HostConfig.MemorySwap < current.HostConfig.Memory {
		return provider.Resources{}, errors.New("live container resource limits unavailable")
	}
	resources.MemoryMiB = current.HostConfig.Memory >> 20
	resources.SwapMiB = (current.HostConfig.MemorySwap - current.HostConfig.Memory) >> 20
	if current.HostConfig.NanoCpus > 0 {
		resources.CPU = float64(current.HostConfig.NanoCpus) / 1e9
	}
	return resources, nil
}

func (s *Store) SetResourceLimits(ctx context.Context, id string, resources provider.Resources) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	linux, ok := s.Runtime.(*LinuxRuntime)
	if !ok || linux.Container == nil {
		return provider.ErrUnsupported
	}
	limits, err := s.limits()
	if err != nil {
		return err
	}
	if err := checkBoxLimits(provider.BoxLimits{CPU: resources.CPU, MemoryMiB: resources.MemoryMiB, SwapMiB: resources.SwapMiB}, limits); err != nil {
		return err
	}
	slot, exists := s.state.Slots[id]
	if !exists || slot.WorkspaceID == "" || slot.State != provider.StateRunning {
		return provider.ErrNotFound
	}
	w := s.state.Workspaces[slot.WorkspaceID]
	old := w
	w.CPU, w.MemoryGiB = resources.CPU, resources.MemoryMiB/1024
	swap := resources.SwapMiB / 1024
	w.SwapGiB = &swap
	if err := linux.Container.UpdateLimits(ctx, w); err != nil {
		return err
	}
	s.state.Workspaces[w.ID] = w
	if err := s.save(); err != nil {
		s.state.Workspaces[w.ID] = old
		return fmt.Errorf("live limits changed but could not be saved; retry after checking: %w", err)
	}
	return nil
}

func (s *Store) Create(req provider.CreateRequest) (provider.Box, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failure != nil {
		return provider.Box{}, s.failure
	}
	if err := provider.ValidateName(req.Name); err != nil {
		return provider.Box{}, err
	}
	if req.Owner.AccountID != s.state.AccountID || req.Owner.BoxID == "" {
		return provider.Box{}, errors.New("worker account mismatch")
	}
	if slot, exists := s.state.Slots[req.Name]; exists {
		if err := provider.VerifyOwner(slot.Owner, req.Owner); err != nil {
			return provider.Box{}, err
		}
		return s.box(slot), nil
	}
	if len(s.state.Slots) >= s.state.Settings.Slots {
		return provider.Box{}, errors.New("shared worker slot capacity reached")
	}
	slot := Slot{Identity: NewID(), Name: req.Name, Owner: req.Owner, State: provider.StateRunning, Revision: 1}
	s.state.Slots[slot.Name] = slot
	if err := s.save(); err != nil {
		delete(s.state.Slots, slot.Name)
		return provider.Box{}, err
	}
	return s.box(slot), nil
}

func (s *Store) Inspect(id string) (provider.Box, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failure != nil {
		return provider.Box{}, s.failure
	}
	slot, exists := s.state.Slots[id]
	if !exists {
		return provider.Box{}, provider.ErrNotFound
	}
	return s.box(slot), nil
}

func (s *Store) Health() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.failure
}

func (s *Store) List() ([]provider.Box, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failure != nil {
		return nil, s.failure
	}
	boxes := make([]provider.Box, 0, len(s.state.Slots))
	for _, slot := range s.state.Slots {
		boxes = append(boxes, s.box(slot))
	}
	sort.Slice(boxes, func(left, right int) bool { return boxes[left].ID < boxes[right].ID })
	return boxes, nil
}

func (s *Store) CreateStorage(ctx context.Context, id string, owner provider.Owner, resources provider.Resources) (provider.Storage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failure != nil {
		return provider.Storage{}, s.failure
	}
	slot, exists := s.state.Slots[id]
	if !exists {
		return provider.Storage{}, provider.ErrNotFound
	}
	if owner.AccountID != s.state.AccountID || owner.BoxID == "" {
		return provider.Storage{}, errors.New("workspace owner required")
	}
	if existing, exists := s.state.Workspaces[slot.WorkspaceID]; exists {
		if err := provider.VerifyOwner(existing.Owner, owner); err != nil {
			return provider.Storage{}, err
		}
		if err := s.Runtime.Prepare(ctx, existing); err != nil {
			return provider.Storage{}, err
		}
		return storageFor(existing), nil
	}
	for _, existing := range s.state.Workspaces {
		if existing.Owner.BoxID == owner.BoxID {
			return provider.Storage{}, errors.New("workspace already exists; attach its retained storage")
		}
	}
	if s.state.NextUID >= 59000 {
		return provider.Storage{}, errors.New("shared workspace identity capacity reached")
	}
	workspace := Workspace{ID: NewID(), Owner: owner, UID: s.state.NextUID, Display: s.state.NextUID - 29000, SizeGiB: resources.DiskGiB}
	if linux, ok := s.Runtime.(*LinuxRuntime); ok && linux.Container != nil {
		limits, err := s.newBoxLimits(resources)
		if err != nil {
			return provider.Storage{}, err
		}
		workspace.CPU, workspace.MemoryGiB = limits.CPU, limits.MemoryMiB/1024
		swap := limits.SwapMiB / 1024
		workspace.SwapGiB = &swap
	} else if resources.MemoryMiB != 0 || resources.SwapMiB != 0 {
		return provider.Storage{}, errors.New("per-box memory and swap limits require container isolation")
	}
	s.state.NextUID++
	s.state.Workspaces[workspace.ID] = workspace
	slot.WorkspaceID, slot.Revision, slot.State = workspace.ID, slot.Revision+1, provider.StateRunning
	s.state.Slots[id] = slot
	if err := s.save(); err != nil {
		return provider.Storage{}, err
	}
	if err := s.Runtime.Prepare(ctx, workspace); err != nil {
		return provider.Storage{}, err
	}
	return storageFor(workspace), nil
}

func (s *Store) Attach(ctx context.Context, id string, storage provider.Storage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failure != nil {
		return s.failure
	}
	slot, exists := s.state.Slots[id]
	if !exists {
		return provider.ErrNotFound
	}
	workspace, exists := s.state.Workspaces[storage.ID]
	if !exists {
		return errors.New("workspace does not belong to this physical worker")
	}
	if slot.WorkspaceID != "" && slot.WorkspaceID != workspace.ID {
		return errors.New("slot contains another workspace")
	}
	for _, other := range s.state.Slots {
		if other.Name != id && other.WorkspaceID == workspace.ID {
			return errors.New("workspace is attached to another slot")
		}
	}
	if err := s.Runtime.Prepare(ctx, workspace); err != nil {
		return err
	}
	if slot.WorkspaceID == workspace.ID && slot.State == provider.StateRunning {
		return nil
	}
	slot.WorkspaceID, slot.Revision, slot.State = workspace.ID, slot.Revision+1, provider.StateRunning
	s.state.Slots[id] = slot
	return s.save()
}

func (s *Store) Release(ctx context.Context, id string, storage *provider.Storage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failure != nil {
		return s.failure
	}
	slot, exists := s.state.Slots[id]
	if !exists {
		return provider.ErrNotFound
	}
	if storage != nil && slot.WorkspaceID != "" && slot.WorkspaceID != storage.ID {
		return errors.New("workspace attachment changed")
	}
	slot.State, slot.Revision = provider.StateStopped, slot.Revision+1
	s.state.Slots[id] = slot
	if err := s.save(); err != nil {
		return err
	}
	if workspace, exists := s.state.Workspaces[slot.WorkspaceID]; exists {
		if err := s.Runtime.Stop(ctx, workspace); err != nil {
			return err
		}
	}
	if storage != nil {
		slot.WorkspaceID = ""
		s.state.Slots[id] = slot
	}
	return s.save()
}

func (s *Store) Start(ctx context.Context, id string) (provider.Box, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failure != nil {
		return provider.Box{}, s.failure
	}
	slot, exists := s.state.Slots[id]
	if !exists {
		return provider.Box{}, provider.ErrNotFound
	}
	if workspace, exists := s.state.Workspaces[slot.WorkspaceID]; exists {
		if err := s.Runtime.Prepare(ctx, workspace); err != nil {
			return provider.Box{}, err
		}
	}
	slot.State = provider.StateRunning
	s.state.Slots[id] = slot
	if err := s.save(); err != nil {
		return provider.Box{}, err
	}
	return s.box(slot), nil
}

func (s *Store) DeleteSlot(id string, owner provider.Owner) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failure != nil {
		return s.failure
	}
	slot, exists := s.state.Slots[id]
	if !exists {
		return nil
	}
	if err := provider.VerifyOwner(slot.Owner, owner); err != nil {
		return err
	}
	if slot.WorkspaceID != "" {
		return errors.New("cannot delete a slot with attached storage")
	}
	delete(s.state.Slots, id)
	return s.save()
}

func (s *Store) DeleteStorage(ctx context.Context, storage provider.Storage, owner provider.Owner) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failure != nil {
		return s.failure
	}
	workspace, exists := s.state.Workspaces[storage.ID]
	if !exists {
		return nil
	}
	if err := provider.VerifyOwner(workspace.Owner, owner); err != nil {
		return err
	}
	for _, slot := range s.state.Slots {
		if slot.WorkspaceID == workspace.ID {
			return errors.New("cannot delete attached workspace")
		}
	}
	if err := s.Runtime.Stop(ctx, workspace); err != nil {
		return err
	}
	if err := s.Runtime.Delete(ctx, workspace); err != nil {
		return err
	}
	delete(s.state.Workspaces, workspace.ID)
	return s.save()
}

func (s *Store) WorkspaceFor(conn provider.Connection) (Workspace, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.workspaceFor(conn)
}

func (s *Store) StartCommand(conn provider.Connection, command *exec.Cmd) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.workspaceFor(conn); err != nil {
		return err
	}
	return command.Start()
}

func (s *Store) workspaceFor(conn provider.Connection) (Workspace, error) {
	if s.failure != nil {
		return Workspace{}, s.failure
	}
	slot, exists := s.state.Slots[conn.Endpoint]
	if !exists || slot.State != provider.StateRunning || s.connection(slot).Metadata["deploymentInstanceId"] != conn.Metadata["deploymentInstanceId"] || slot.WorkspaceID == "" || slot.WorkspaceID != conn.Metadata["workspaceId"] {
		return Workspace{}, errors.New("shared slot assignment changed or stopped")
	}
	workspace, exists := s.state.Workspaces[slot.WorkspaceID]
	if !exists {
		return Workspace{}, errors.New("workspace unavailable")
	}
	return workspace, nil
}
