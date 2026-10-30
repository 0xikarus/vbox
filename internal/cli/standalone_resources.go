package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
)

type standaloneMemory int64

func (m *standaloneMemory) String() string { return strconv.FormatInt(int64(*m), 10) }
func (m *standaloneMemory) Set(text string) error {
	value := strings.ToLower(strings.TrimSpace(text))
	factor := float64(1)
	for _, suffix := range []struct {
		unit   string
		factor float64
	}{{"gib", 1024}, {"gb", 1000000000.0 / 1048576}, {"mib", 1}, {"mb", 1000000.0 / 1048576}, {"g", 1024}, {"m", 1}} {
		if strings.HasSuffix(value, suffix.unit) {
			value = strings.TrimSuffix(value, suffix.unit)
			factor = suffix.factor
			break
		}
	}
	n, err := strconv.ParseFloat(value, 64)
	if err != nil || math.IsNaN(n) || math.IsInf(n, 0) || n <= 0 || n*factor > float64(math.MaxInt64) {
		return fmt.Errorf("memory must be positive MiB or a size such as 6g")
	}
	*m = standaloneMemory(math.Ceil(n * factor))
	return nil
}

func (a *App) standaloneCapacity(ctx context.Context, host string) string {
	argv := []string{"docker"}
	if host != "" {
		argv = append(argv, "--host", "ssh://"+host)
	}
	argv = append(argv, "info", "--format", `{"cpus":{{.NCPU}},"memory":{{.MemTotal}},"arch":"{{.Architecture}}"}`)
	result, err := a.Runner.Run(ctx, argv, nil, nil, nil)
	if err != nil || result.ExitCode != 0 {
		return "Docker host capacity unavailable"
	}
	var info struct {
		CPUs   int    `json:"cpus"`
		Memory int64  `json:"memory"`
		Arch   string `json:"arch"`
	}
	if json.Unmarshal(result.Stdout, &info) != nil {
		return "Docker host capacity unavailable"
	}
	return fmt.Sprintf("%s · %d CPUs · %.1f GiB total Docker memory", info.Arch, info.CPUs, float64(info.Memory)/(1<<30))
}

func (a *App) standaloneLinuxResources(ctx context.Context, host string) string {
	script := `import os,json,platform
m={}
with open('/proc/meminfo') as f:
 for line in f:
  key,value=line.split(':',1);m[key]=int(value.strip().split()[0])
s=os.statvfs(os.path.expanduser('~'))
print(json.dumps({'available':m.get('MemAvailable',0),'load':os.getloadavg()[0],'disk':s.f_bavail*s.f_frsize,'kvm':os.access('/dev/kvm',os.R_OK|os.W_OK)}))`
	argv := []string{"python3", "-c", script}
	if host != "" {
		argv = []string{"ssh", "-o", "BatchMode=yes", "-o", "ClearAllForwardings=yes", "-o", "ConnectTimeout=10", host, "python3 -c " + shellQuote(script)}
	}
	result, err := a.Runner.Run(ctx, argv, nil, nil, nil)
	if err != nil || result.ExitCode != 0 {
		return "Host RAM availability / disk / KVM unavailable"
	}
	var p struct {
		Available int64   `json:"available"`
		Load      float64 `json:"load"`
		Disk      int64   `json:"disk"`
		KVM       bool    `json:"kvm"`
	}
	if json.NewDecoder(bytes.NewReader(result.Stdout)).Decode(&p) != nil {
		return "Host resource probe unavailable"
	}
	return fmt.Sprintf("%.1f GiB RAM available · load %.2f · %.1f GiB free on home filesystem · KVM access %t", float64(p.Available)/(1<<20), p.Load, float64(p.Disk)/(1<<30), p.KVM)
}

// Allocate the port on the target host before creating the container so Docker
// stores an explicit binding which remains stable across restart.
func (a *App) standaloneFreePort(ctx context.Context, host, address string) (int, error) {
	return a.standaloneTargetPort(ctx, host, address, 0)
}

func (a *App) standaloneTargetPort(ctx context.Context, host, address string, requested int) (int, error) {
	script := `import socket,sys,json
a=json.load(sys.stdin)
saddr=a['address']
s=socket.socket(socket.AF_INET6 if ':' in saddr else socket.AF_INET,socket.SOCK_STREAM)
s.bind((saddr,a['port']))
print(s.getsockname()[1])
s.close()`
	data, _ := json.Marshal(map[string]any{"address": address, "port": requested})
	argv := []string{"python3", "-c", script}
	if host != "" {
		argv = []string{"ssh", "-o", "BatchMode=yes", "-o", "ClearAllForwardings=yes", host, "python3 -c " + shellQuote(script)}
	}
	result, err := a.Runner.Run(ctx, argv, bytes.NewReader(data), nil, nil)
	if err != nil || result.ExitCode != 0 {
		return 0, fmt.Errorf("could not allocate a gateway port on the target host")
	}
	port, err := strconv.Atoi(strings.TrimSpace(string(result.Stdout)))
	if err != nil || port < 1 || port > 65535 || requested != 0 && requested != port {
		return 0, fmt.Errorf("invalid target gateway port")
	}
	return port, nil
}
