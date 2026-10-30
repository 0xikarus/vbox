package hub

import (
	"context"
	"fmt"
	"github.com/0xikarus/vmbox-service/internal/provider"
	"math"
)

type resizeEngine interface {
	Resize(context.Context, Box, float64, int64) error
}

func (e *DockerEngine) Resize(ctx context.Context, b Box, cpu float64, ram int64) error {
	observed, err := e.manager(b).Resize(ctx, b.ID, provider.Resources{CPU: cpu, MemoryMiB: ram, PIDs: 512})
	if err != nil {
		return err
	}
	if math.Abs(observed.Resources.CPU-cpu) > 0.000001 || observed.Resources.MemoryMiB != ram {
		return fmt.Errorf("Docker did not apply requested limits")
	}
	return nil
}
func (m *Manager) Resize(ctx context.Context, id string, cpu float64, ram int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, err := m.Store.Get(id)
	if err != nil || b.State == "deleted" || b.Desired == "deleted" {
		return fmt.Errorf("box not found")
	}
	if _, busy := m.active[id]; busy {
		return fmt.Errorf("wait for the current lifecycle operation")
	}
	if cpu <= 0 || math.IsNaN(cpu) || math.IsInf(cpu, 0) || cpu > m.Config.Policy.CPUs || ram < 256 || ram > m.Config.Policy.MemoryMiB {
		return fmt.Errorf("invalid box resource limits")
	}
	o, err := m.Engine.Inspect(ctx, b)
	if err != nil {
		return err
	}
	if o.Running && ram < b.Spec.MemoryMiB {
		return fmt.Errorf("stop the box before reducing its RAM")
	}
	boxes, err := m.Store.List()
	if err != nil {
		return err
	}
	totalCPU, totalRAM, _ := allocated(boxes)
	if b.State == "running" || b.State == "starting" || b.State == "stopping" {
		if totalCPU-b.Spec.CPU+cpu > m.Config.Policy.CPUs || totalRAM-b.Spec.MemoryMiB+ram > m.Config.Policy.MemoryMiB {
			return fmt.Errorf("resource edit exceeds the active worker budget")
		}
	}
	if !o.Exists {
		b.Spec.CPU = cpu
		b.Spec.MemoryMiB = ram
		return m.Store.Update(b)
	}
	e, ok := m.Engine.(resizeEngine)
	if !ok {
		return fmt.Errorf("resource editing unavailable")
	}
	// Reserve the larger limits before Docker changes them. Both crash windows
	// therefore retain enough capacity, including mixed increase/decrease edits.
	b.Spec.CPU = max(b.Spec.CPU, cpu)
	b.Spec.MemoryMiB = max(b.Spec.MemoryMiB, ram)
	if err = m.Store.Update(b); err != nil {
		return err
	}
	bounded, cancel := context.WithTimeout(ctx, operationTimeout)
	defer cancel()
	if err = e.Resize(bounded, b, cpu, ram); err != nil {
		b.State = "stopping"
		b.Desired = "stopped"
		b.Reason = "resize verification failed; stop before retry"
		_ = m.Store.Update(b)
		return fmt.Errorf("resize could not be verified; stopping box while retaining files")
	}
	b.Spec.CPU = cpu
	b.Spec.MemoryMiB = ram
	return m.Store.Update(b)
}
