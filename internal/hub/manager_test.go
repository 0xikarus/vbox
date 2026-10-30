package hub

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

type fakeEngine struct {
	mu           sync.Mutex
	host         Host
	workers      map[string]Observation
	fail         bool
	inspectError bool
	gateway      Gateway
}

func newFake() *fakeEngine {
	return &fakeEngine{host: Host{CPUs: 8, MemoryMiB: 8192, AvailableMiB: 8192, DiskFreeMiB: 10000}, workers: map[string]Observation{}}
}
func (f *fakeEngine) Host(context.Context) (Host, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.host, nil
}
func (f *fakeEngine) Provision(_ context.Context, b Box, _ [][]byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail {
		return fmt.Errorf("fixture failure")
	}
	f.workers[b.ID] = Observation{Exists: true, Running: true, Port: 18080}
	return nil
}
func (f *fakeEngine) Inspect(_ context.Context, b Box) (Observation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.inspectError {
		return Observation{}, fmt.Errorf("fixture inspection unavailable")
	}
	return f.workers[b.ID], nil
}
func (f *fakeEngine) Start(_ context.Context, b Box) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	o := f.workers[b.ID]
	o.Running = true
	f.workers[b.ID] = o
	return nil
}
func (f *fakeEngine) Stop(_ context.Context, b Box) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	o := f.workers[b.ID]
	o.Running = false
	f.workers[b.ID] = o
	return nil
}
func (f *fakeEngine) Delete(_ context.Context, b Box) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.workers, b.ID)
	return nil
}
func (f *fakeEngine) Gateway(context.Context, Box) (Gateway, error) { return f.gateway, nil }
func fixture(t *testing.T) (*Manager, *fakeEngine) {
	t.Helper()
	dir := t.TempDir()
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	c := Config{ID: "hub-fixture", Policy: Policy{CPUs: 1, MemoryMiB: 1024, ReserveCPUs: 1, ReserveMemoryMiB: 512, MinDiskFreeMiB: 64, MaxRunning: 2}, Defaults: Spec{Agent: "opencode", CPU: 0.5, MemoryMiB: 512, ModelURL: "http://model/v1", Model: "fixture"}}
	f := newFake()
	m := NewManager(dir, c, s, f)
	m.ready = func(context.Context, Box) bool { return true }
	return m, f
}
func tick(t *testing.T, m *Manager, n int) {
	t.Helper()
	for range n {
		if err := m.Tick(context.Background()); err != nil {
			t.Fatal(err)
		}
		m.wg.Wait()
	}
}
func TestConcurrentAdmissionFIFOAndStopRelease(t *testing.T) {
	m, f := fixture(t)
	var wg sync.WaitGroup
	for i := range 12 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, err := m.Create(Spec{Name: fmt.Sprintf("tiny-%d", i)}); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	tick(t, m, 3)
	boxes, _ := m.Store.List()
	cpu, ram, count := allocated(boxes)
	if count != 2 || cpu > 1 || ram > 1024 {
		t.Fatalf("over-admission: %d %g %d", count, cpu, ram)
	}
	if boxes[0].State != "running" || boxes[1].State != "running" || boxes[2].State != "queued" {
		t.Fatal("queue order changed")
	}
	if err := m.Action(boxes[0].ID, "stop"); err != nil {
		t.Fatal(err)
	}
	tick(t, m, 3)
	b, _ := m.Store.Get(boxes[2].ID)
	if b.State != "running" {
		t.Fatal("queue did not advance")
	}
	f.mu.Lock()
	if f.workers[boxes[0].ID].Running {
		t.Fatal("stopped worker still running")
	}
	f.mu.Unlock()
}
func TestRecoveryReservesExistingWorkersBeforeQueue(t *testing.T) {
	m, f := fixture(t)
	queued, _ := m.Create(Spec{Name: "earlier queued"})
	late, _ := m.Create(Spec{Name: "crash-window worker", CPU: 1, MemoryMiB: 1024})
	f.workers[late.ID] = Observation{Exists: true, Running: true, Port: 18080} // Docker start completed before process died.
	tick(t, m, 2)
	b, _ := m.Store.Get(queued.ID)
	if b.State != "queued" {
		t.Fatal("recovery admitted over an unobserved live worker")
	}
	m2 := NewManager(m.Dir, m.Config, m.Store, f)
	m2.ready = m.ready
	tick(t, m2, 2)
	b, _ = m.Store.Get(late.ID)
	if b.State != "running" {
		t.Fatal("hub restart lost live worker")
	}
}
func TestFailedStartupAndOwnershipUncertainty(t *testing.T) {
	m, f := fixture(t)
	b, _ := m.Create(Spec{Name: "failed"})
	f.fail = true
	tick(t, m, 2)
	got, _ := m.Store.Get(b.ID)
	if got.State != "failed" {
		t.Fatalf("expected failure, got %s", got.State)
	}
	f.fail = false
	next, _ := m.Create(Spec{Name: "next"})
	f.inspectError = true
	tick(t, m, 1)
	got, _ = m.Store.Get(next.ID)
	if got.State != "queued" {
		t.Fatal("admitted with unverifiable worker inventory")
	}
	f.inspectError = false
	tick(t, m, 2)
	got, _ = m.Store.Get(next.ID)
	if got.State != "running" {
		t.Fatal("failure did not release RAM/CPU reservation")
	}
}
func TestHostPressureAndPolicyReduction(t *testing.T) {
	m, f := fixture(t)
	b, _ := m.Create(Spec{Name: "pressure"})
	f.host.AvailableMiB = 600
	tick(t, m, 1)
	got, _ := m.Store.Get(b.ID)
	if got.State != "queued" {
		t.Fatal("host RAM reserve consumed")
	}
	f.host.AvailableMiB = 8192
	tick(t, m, 2)
	p := m.Config.Policy
	p.MemoryMiB = 256
	if err := m.SetPolicy(context.Background(), p); err == nil {
		t.Fatal("lowered budget below live reservations")
	}
	f.host.DiskFreeMiB = 1
	tick(t, m, 2)
	got, _ = m.Store.Get(b.ID)
	if got.Desired != "stopped" {
		t.Fatal("disk pressure left worker writing")
	}
	if _, ok := f.workers[b.ID]; !ok {
		t.Fatal("pressure deleted user data")
	}
}
func TestCrashLoopAndStartupDeadline(t *testing.T) {
	m, f := fixture(t)
	b, _ := m.Create(Spec{Name: "crash loop"})
	tick(t, m, 2)
	for range 3 {
		f.mu.Lock()
		o := f.workers[b.ID]
		o.Running = false
		f.workers[b.ID] = o
		f.mu.Unlock()
		tick(t, m, 3)
	}
	got, _ := m.Store.Get(b.ID)
	if got.State != "failed" {
		t.Fatalf("crash loop not stopped: %s", got.State)
	}
	b2, _ := m.Create(Spec{Name: "never ready"})
	tick(t, m, 2)
	m.ready = func(context.Context, Box) bool { return false }
	got, _ = m.Store.Get(b2.ID)
	got.Booted = time.Now().Add(-20 * time.Minute).Format(time.RFC3339Nano)
	if err := m.Store.Update(got); err != nil {
		t.Fatal(err)
	}
	tick(t, m, 3)
	got, _ = m.Store.Get(b2.ID)
	if got.Desired != "stopped" {
		t.Fatal("startup had no deadline")
	}
}
func TestPersistentStoreLockAndPolicyBounds(t *testing.T) {
	m, _ := fixture(t)
	if second, err := OpenStore(m.Dir); err == nil {
		second.Close()
		t.Fatal("two host managers could allocate the same registry")
	}
	h := Host{CPUs: 8, MemoryMiB: 8192}
	p := m.Config.Policy
	p.ModelMemoryMiB = 8000
	if p.Validate(h) == nil {
		t.Fatal("model reservation ignored")
	}
	b, _ := m.Create(Spec{Name: "persisted"})
	if err := m.Store.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := OpenStore(m.Dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, err := s.Get(b.ID)
	if err != nil || got.Password != b.Password || got.State != "queued" {
		t.Fatal("durable identity or queue lost")
	}
}

func (f *fakeEngine) Resize(_ context.Context, _ Box, _ float64, _ int64) error { return nil }
func TestResourceEditsAndCreateIdempotency(t *testing.T) {
	m, _ := fixture(t)
	b, err := m.Create(Spec{Name: "stable"}, "create-key")
	if err != nil {
		t.Fatal(err)
	}
	same, err := m.Create(Spec{Name: "stable"}, "create-key")
	if err != nil || same.ID != b.ID {
		t.Fatal("creation retry duplicated worker identity")
	}
	if _, err = m.Create(Spec{Name: "different"}, "create-key"); err == nil {
		t.Fatal("conflicting create retry accepted")
	}
	_, err = m.Create(Spec{Name: "second"})
	if err != nil {
		t.Fatal(err)
	}
	tick(t, m, 2)
	if err = m.Resize(context.Background(), b.ID, 0.75, 512); err == nil {
		t.Fatal("increased CPU beyond combined budget")
	}
	if err = m.Resize(context.Background(), b.ID, 0.25, 256); err == nil {
		t.Fatal("live memory shrink accepted")
	}
	if err = m.Resize(context.Background(), b.ID, 0.25, 512); err != nil {
		t.Fatal(err)
	}
	got, _ := m.Store.Get(b.ID)
	if got.Spec.CPU != 0.25 {
		t.Fatal("resource edit not persisted")
	}
}

func TestExplicitTextOnlyModelOverridesVisionDefault(t *testing.T) {
	m, _ := fixture(t)
	yes, no := true, false
	m.Config.Defaults.Vision = &yes
	inherited, err := m.Create(Spec{Name: "inherited"})
	if err != nil || inherited.Spec.Vision == nil || !*inherited.Spec.Vision {
		t.Fatal("lost hub vision default")
	}
	explicit, err := m.Create(Spec{Name: "text-only", Vision: &no})
	if err != nil || explicit.Spec.Vision == nil || *explicit.Spec.Vision {
		t.Fatal("explicit text-only model inherited vision")
	}
}
