package hub

import (
	"context"
	"fmt"
	"math"
	"net/url"
	"strings"
	"sync"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
)

type Manager struct {
	usageMu sync.Mutex
	usage   map[string]Usage
	Store   *Store
	Engine  Engine
	Dir     string
	mu      sync.Mutex
	Config  Config
	active  map[string]context.CancelFunc
	wg      sync.WaitGroup
	ready   func(context.Context, Box) bool
	goal    func(context.Context, Box) error
}

func NewManager(dir string, c Config, s *Store, e Engine) *Manager {
	return &Manager{Dir: dir, Config: c, Store: s, Engine: e, active: map[string]context.CancelFunc{}}
}
func (m *Manager) config() Config { m.mu.Lock(); defer m.mu.Unlock(); return m.Config }
func (m *Manager) Create(spec Spec, keys ...string) (Box, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d := m.Config.Defaults
	if spec.Agent == "" {
		spec.Agent = d.Agent
	}
	if spec.CPU == 0 {
		spec.CPU = d.CPU
	}
	if spec.MemoryMiB == 0 {
		spec.MemoryMiB = d.MemoryMiB
	}
	if spec.Agent != "opencode" {
		spec.ModelSource = ""
		spec.ModelURL = ""
		spec.Model = ""
		spec.Vision = nil
	} else {
		if spec.ModelSource == "" {
			if spec.ModelURL != "" {
				spec.ModelSource = "custom"
			} else {
				profiles, e := m.Store.Profiles()
				if e != nil {
					return Box{}, e
				}
				for _, p := range profiles {
					for _, id := range spec.Profiles {
						if id == p.ID && p.Agent == "opencode" {
							spec.ModelSource = "profile"
						}
					}
				}
				if spec.ModelSource == "" {
					spec.ModelSource = d.ModelSource
				}
				if spec.ModelSource == "" && d.ModelURL != "" {
					spec.ModelSource = "custom"
				}
				if spec.ModelSource == "" {
					spec.ModelSource = "profile"
				}
			}
		}
		switch spec.ModelSource {
		case "custom":
			if spec.ModelURL == "" {
				spec.ModelURL = d.ModelURL
			}
			if spec.Model == "" {
				spec.Model = d.Model
			}
			if spec.Vision == nil {
				spec.Vision = d.Vision
			}
		case "profile":
			spec.ModelURL = ""
			spec.Model = ""
			spec.Vision = nil
			profiles, e := m.Store.Profiles()
			if e != nil {
				return Box{}, e
			}
			found := false
			for _, p := range profiles {
				for _, id := range spec.Profiles {
					if id == p.ID && p.Agent == "opencode" {
						found = true
					}
				}
			}
			if !found {
				return Box{}, fmt.Errorf("select or upload an OpenCode profile, or choose Custom endpoint")
			}
		default:
			return Box{}, fmt.Errorf("modelSource must be profile or custom")
		}
	}

	if spec.Name == "" || len(spec.Name) > 100 || len(spec.Goal) > 100000 || len(spec.Profiles) > 3 || len(spec.Tools) > 20 {
		return Box{}, fmt.Errorf("invalid box settings")
	}
	if spec.Agent != "claude" && spec.Agent != "opencode" && spec.Agent != "codex" {
		return Box{}, fmt.Errorf("agent must be opencode, claude or codex")
	}
	if spec.CPU <= 0 || math.IsNaN(spec.CPU) || math.IsInf(spec.CPU, 0) || spec.CPU > m.Config.Policy.CPUs || spec.MemoryMiB < 256 || spec.MemoryMiB > m.Config.Policy.MemoryMiB {
		return Box{}, fmt.Errorf("box exceeds the hub worker budget")
	}
	if spec.Agent == "opencode" && spec.ModelSource == "custom" {
		u, err := url.Parse(spec.ModelURL)
		if spec.Model == "" || err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "http" && u.Scheme != "https") {
			return Box{}, fmt.Errorf("OpenCode requires a model and HTTP(S) endpoint without credentials")
		}
	}
	if err := v1.ValidateTools(spec.Tools); err != nil {
		return Box{}, err
	}
	if _, err := m.Store.Archive(spec.Profiles); err != nil {
		return Box{}, err
	}
	key := ""
	if len(keys) > 0 {
		key = keys[0]
	}
	if len(key) > 128 {
		return Box{}, fmt.Errorf("idempotency key too long")
	}
	return m.Store.CreateWithKey(spec, key)
}
func (m *Manager) Action(id, action string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, err := m.Store.Get(id)
	if err != nil || b.State == "deleted" {
		return fmt.Errorf("box not found")
	}
	if b.Desired == "deleted" {
		return fmt.Errorf("box deletion is already in progress")
	}
	switch action {
	case "start":
		b.Desired = "running"
		if b.State == "failed" {
			b.State = "queued"
			b.Attempts = 0
		}
	case "stop":
		b.Desired = "stopped"
	case "delete":
		b.Desired = "deleted"
	default:
		return fmt.Errorf("unknown action")
	}
	return m.Store.Update(b)
}
func (m *Manager) SetPolicy(ctx context.Context, p Policy) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	h, err := m.Engine.Host(ctx)
	if err != nil {
		return err
	}
	if err = p.Validate(h); err != nil {
		return err
	}
	boxes, err := m.Store.List()
	if err != nil {
		return err
	}
	cpu, mem, count := allocated(boxes)
	if p.CPUs < cpu || p.MemoryMiB < mem || p.MaxRunning < count {
		return fmt.Errorf("stop boxes before reducing their reserved capacity")
	}
	c := m.Config
	c.Policy = p
	if err = SaveConfig(m.Dir, c); err == nil {
		m.Config = c
	}
	return err
}
func allocated(boxes []Box) (cpu float64, mem int64, count int) {
	for _, b := range boxes {
		if b.State == "starting" || b.State == "running" || b.State == "stopping" || (b.State == "deleting" && b.Port > 0) {
			cpu += b.Spec.CPU
			mem += b.Spec.MemoryMiB
			count++
		}
	}
	return
}
func (m *Manager) launch(parent context.Context, b Box, operation string) {
	if len(m.active) >= 4 {
		return
	}
	ctx, cancel := context.WithTimeout(parent, operationTimeout)
	m.active[b.ID] = cancel
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		defer cancel()
		var err error
		switch operation {
		case "provision":
			var archives [][]byte
			archives, err = m.Store.Archive(b.Spec.Profiles)
			if err == nil {
				err = m.Engine.Provision(ctx, b, archives)
			}
		case "start":
			err = m.Engine.Start(ctx, b)
		case "stop":
			err = m.Engine.Stop(ctx, b)
		case "delete":
			err = m.Engine.Delete(ctx, b)
		}
		m.mu.Lock()
		defer m.mu.Unlock()
		delete(m.active, b.ID)
		current, e := m.Store.Get(b.ID)
		if e != nil {
			return
		}
		if err != nil && parent.Err() != nil {
			return
		}
		if err != nil {
			current.Reason = operation + " failed"
			if operation == "provision" || operation == "start" {
				current.State = "stopping"
				current.Desired = "stopped"
			} // Failed cleanup retains its reservation for retry.
		} else {
			switch operation {
			case "stop":
				current.State = "stopped"
				current.Port = 0
			case "delete":
				current.State = "deleted"
				current.Password = ""
				current.Port = 0
			}
		}
		_ = m.Store.Update(current)
	}()
}
func (m *Manager) Tick(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	boxes, err := m.Store.List()
	if err != nil {
		return err
	}
	host, hostErr := m.Engine.Host(ctx)
	observations := map[string]Observation{}
	uncertain := false
	// Inventory all durable identities before admitting anything. This closes the
	// crash window between Docker start and the registry's observed running state.
	for i, b := range boxes {
		if _, busy := m.active[b.ID]; busy {
			continue
		}
		probe, cancel := context.WithTimeout(ctx, 5*time.Second)
		o, e := m.Engine.Inspect(probe, b)
		cancel()
		if e != nil {
			uncertain = true
			continue
		}
		if !o.Running {
			o.Port = 0
		}
		observations[b.ID] = o
		if b.Port != o.Port {
			b.Port = o.Port
			if err = m.Store.Update(b); err != nil {
				return err
			}
			boxes[i] = b
		}

		if o.Running && b.State != "running" && b.State != "starting" && b.State != "stopping" && b.State != "deleting" {
			b.State = "starting"
			if err = m.Store.Update(b); err != nil {
				return err
			}
			boxes[i] = b
		}
	}
	blocked := uncertain
	for i, b := range boxes {
		if _, busy := m.active[b.ID]; busy {
			continue
		}
		o, verified := observations[b.ID]
		if !verified {
			continue
		}

		if b.Desired == "deleted" {
			b.State = "deleting"
			_ = m.Store.Update(b)
			boxes[i] = b
			m.launch(ctx, b, "delete")
			continue
		}
		if b.Desired == "stopped" {
			if o.Running {
				b.State = "stopping"
				_ = m.Store.Update(b)
				boxes[i] = b
				m.launch(ctx, b, "stop")
			} else {
				b.State = "stopped"
				if b.State == "failed" || strings.Contains(b.Reason, "failed") || strings.Contains(b.Reason, "timed out") || strings.Contains(b.Reason, "repeatedly exited") {
					b.State = "failed"
				}
				_ = m.Store.Update(b)
				boxes[i] = b
			}
			continue
		}
		if hostErr != nil {
			continue
		}
		if host.DiskFreeMiB < m.Config.Policy.MinDiskFreeMiB || host.AvailableMiB < m.Config.Policy.ReserveMemoryMiB/2 {
			if o.Running {
				b.Desired = "stopped"
				b.State = "stopping"
				b.Reason = "host RAM/disk pressure; files retained"
				_ = m.Store.Update(b)
				boxes[i] = b
				m.launch(ctx, b, "stop")
			}
			continue
		}
		if o.Running {
			b.Port = o.Port
			if m.ready != nil && m.ready(ctx, b) {
				b.State = "running"
				b.Reason = ""
			} else {
				b.State = "starting"
				b.Reason = "waiting for agent"
			}
			if b.State == "starting" && b.Booted != "" {
				if started, e := time.Parse(time.RFC3339Nano, b.Booted); e == nil && time.Since(started) > 15*time.Minute {
					b.Desired = "stopped"
					b.Reason = "startup timed out; files retained"
				}
			}
			if b.State == "running" && b.Spec.Goal != "" && !b.GoalSent && m.goal != nil {
				if m.goal(ctx, b) == nil {
					b.GoalSent = true
				}
			}
			_ = m.Store.Update(b)
			boxes[i] = b
			continue
		}
		if b.State == "running" || b.State == "starting" {
			b.Attempts++
			if b.Attempts >= 3 {
				b.State = "failed"
				b.Desired = "stopped"
				b.Reason = "worker repeatedly exited; retry explicitly"
			} else {
				b.State = "queued"
				b.Reason = "recovering worker"
			}
			_ = m.Store.Update(b)
			boxes[i] = b
			continue
		}
		if b.State == "failed" {
			continue
		}
		if b.Spec.CPU > m.Config.Policy.CPUs || b.Spec.MemoryMiB > m.Config.Policy.MemoryMiB {
			b.State = "failed"
			b.Desired = "stopped"
			b.Reason = "box exceeds updated hub budget"
			if err = m.Store.Update(b); err != nil {
				return err
			}
			boxes[i] = b
			continue
		}
		b.State = "queued"
		if blocked {
			b.Reason = "waiting for earlier box or host verification"
			if err = m.Store.Update(b); err != nil {
				return err
			}
			boxes[i] = b
			continue
		}
		cpu, mem, count := allocated(boxes)
		p := m.Config.Policy
		switch {
		case len(m.active) >= 4:
			b.Reason = "waiting for lifecycle operation"
		case count >= p.MaxRunning:
			b.Reason = "maximum running boxes reached"
		case cpu+b.Spec.CPU > p.CPUs+0.000001:
			b.Reason = "waiting for CPU budget"
		case mem+b.Spec.MemoryMiB > p.MemoryMiB:
			b.Reason = "waiting for RAM budget"
		case host.AvailableMiB-mem-b.Spec.MemoryMiB < p.ReserveMemoryMiB:
			b.Reason = "waiting for host RAM headroom"
		default:
			b.State = "starting"
			b.Reason = "starting worker"
			b.Booted = time.Now().UTC().Format(time.RFC3339Nano)
			if err = m.Store.Update(b); err != nil {
				return err
			}
			boxes[i] = b
			if o.Exists {
				m.launch(ctx, b, "start")
			} else {
				m.launch(ctx, b, "provision")
			}
			continue
		}
		blocked = true
		_ = m.Store.Update(b)
		boxes[i] = b
	}
	return hostErr
}
func (m *Manager) Run(ctx context.Context) {
	usageDone := make(chan struct{})
	go func() { defer close(usageDone); m.collectUsage(ctx) }()
	defer func() { <-usageDone }()
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		_ = m.Tick(ctx)
		select {
		case <-ctx.Done():
			m.mu.Lock()
			for _, cancel := range m.active {
				cancel()
			}
			m.mu.Unlock()
			m.wg.Wait()
			return
		case <-t.C:
		}
	}
}
func validID(s string) bool {
	return strings.HasPrefix(s, "hb-") && len(s) == 27 && strings.Trim(s[3:], "0123456789abcdef") == ""
}
