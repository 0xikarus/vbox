package hub

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type Usage struct {
	CPUPercent float64 `json:"cpuPercent"`
	MemoryMiB  float64 `json:"memoryMiB"`
	Observed   string  `json:"observed"`
}
type usageEngine interface {
	Usage(context.Context, []Box) (map[string]Usage, error)
}

func (e *DockerEngine) Usage(ctx context.Context, boxes []Box) (map[string]Usage, error) {
	argv := []string{"docker", "stats", "--no-stream", "--format", "{{json .}}"}
	for _, b := range boxes {
		if b.State == "running" || b.State == "starting" {
			argv = append(argv, "vmbox-"+b.ID)
		}
	}
	out := map[string]Usage{}
	if len(argv) == 5 {
		return out, nil
	}
	r, err := e.Runner.Run(ctx, argv, nil, nil, nil)
	if err != nil || r.ExitCode != 0 {
		return nil, fmt.Errorf("usage unavailable")
	}
	for _, line := range strings.Split(strings.TrimSpace(string(r.Stdout)), "\n") {
		var v struct{ Name, CPUPerc, MemUsage string }
		if json.Unmarshal([]byte(line), &v) != nil {
			continue
		}
		cpu, _ := strconv.ParseFloat(strings.TrimSuffix(v.CPUPerc, "%"), 64)
		mem, _, _ := strings.Cut(v.MemUsage, "/")
		mem = strings.TrimSpace(mem)
		used := 0.0
		for _, u := range []struct {
			suffix string
			scale  float64
		}{{"GiB", 1024}, {"MiB", 1}, {"KiB", 1.0 / 1024}, {"GB", 1e9 / (1 << 20)}, {"MB", 1e6 / (1 << 20)}, {"kB", 1e3 / (1 << 20)}, {"B", 1.0 / (1 << 20)}} {
			if strings.HasSuffix(mem, u.suffix) {
				n, _ := strconv.ParseFloat(strings.TrimSpace(strings.TrimSuffix(mem, u.suffix)), 64)
				used = n * u.scale
				break
			}
		}
		out[strings.TrimPrefix(v.Name, "vmbox-")] = Usage{cpu, used, time.Now().UTC().Format(time.RFC3339Nano)}
	}
	return out, nil
}
func (m *Manager) collectUsage(ctx context.Context) {
	e, ok := m.Engine.(usageEngine)
	if !ok {
		return
	}
	t := time.NewTicker(10 * time.Second)
	defer t.Stop()
	for {
		boxes, err := m.Store.List()
		if err == nil {
			bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
			u, e := e.Usage(bounded, boxes)
			cancel()
			if e == nil {
				m.usageMu.Lock()
				m.usage = u
				m.usageMu.Unlock()
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
func (m *Manager) publicBox(b Box) Box {
	m.usageMu.Lock()
	defer m.usageMu.Unlock()
	if u, ok := m.usage[b.ID]; ok {
		b.Usage = &u
	}
	return b
}
