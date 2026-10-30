package hub

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/0xikarus/vmbox-service/internal/instance"
	"github.com/0xikarus/vmbox-service/internal/procexec"
	"github.com/0xikarus/vmbox-service/internal/provider"
	dockerprovider "github.com/0xikarus/vmbox-service/internal/provider/docker"
	"golang.org/x/sys/unix"
)

type Observation struct {
	Exists, Running bool
	Port            int
}
type Gateway struct {
	Port         int
	Capabilities map[string]string
}
type Engine interface {
	Host(context.Context) (Host, error)
	Provision(context.Context, Box, [][]byte) error
	Inspect(context.Context, Box) (Observation, error)
	Start(context.Context, Box) error
	Stop(context.Context, Box) error
	Delete(context.Context, Box) error
	Gateway(context.Context, Box) (Gateway, error)
}
type DockerEngine struct {
	mu                sync.Mutex
	HubID, Image, Dir string
	Runner            procexec.Runner
	diskPath          string
}

func NewDockerEngine(id, image, dir string) *DockerEngine {
	return &DockerEngine{HubID: id, Image: image, Dir: dir, Runner: procexec.OSRunner{}}
}
func (e *DockerEngine) manager(b Box) *instance.Manager {
	return instance.New(dockerprovider.Config{CreateStopped: true, NoRestart: true, BoundedLogs: true, PublishGateway: true, GatewayAddress: "127.0.0.1", MemorySwapMiB: &b.Spec.MemoryMiB}, e.Runner)
}
func (e *DockerEngine) owner(b Box) provider.Owner {
	return provider.Owner{AccountID: e.HubID, BoxID: b.ID, Lease: b.ID}
}
func (e *DockerEngine) Host(ctx context.Context) (Host, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var h Host
	h.CPUs = float64(runtime.NumCPU())
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return h, err
	}
	for _, line := range strings.Split(string(data), "\n") {
		v := strings.Fields(line)
		if len(v) < 2 {
			continue
		}
		n, _ := strconv.ParseInt(v[1], 10, 64)
		switch v[0] {
		case "MemTotal:":
			h.MemoryMiB = n / 1024
		case "MemAvailable:":
			h.AvailableMiB = n / 1024
		}
	}
	if e.diskPath == "" {
		r, err := e.Runner.Run(ctx, []string{"docker", "info", "--format", "{{.DockerRootDir}}"}, nil, nil, nil)
		if err != nil || r.ExitCode != 0 {
			return h, fmt.Errorf("Docker host unavailable")
		}
		e.diskPath = strings.TrimSpace(string(r.Stdout))
	}
	var fs unix.Statfs_t
	if err = unix.Statfs(e.diskPath, &fs); err != nil {
		return h, fmt.Errorf("cannot inspect Docker storage filesystem")
	}
	h.DiskFreeMiB = int64(fs.Bavail) * int64(fs.Bsize) / (1 << 20)
	return h, nil
}
func (e *DockerEngine) Provision(ctx context.Context, b Box, archives [][]byte) error {
	pw, err := instance.PasswordArchive(b.Password)
	if err != nil {
		return err
	}
	defer clear(pw)
	env := map[string]string{"VMBOX_AGENT": b.Spec.Agent, "VMBOX_MODEL_URL": b.Spec.ModelURL, "VMBOX_MODEL": b.Spec.Model, "VMBOX_MODEL_VISION": strconv.FormatBool(b.Spec.Vision != nil && *b.Spec.Vision), "VMBOX_TOOLS": strings.Join(b.Spec.Tools, ","), "VMBOX_WEB_BASE_PATH": "/boxes/" + b.ID, "VMBOX_HUB_MANAGED": "true"}
	env["VMBOX_BOX_NAME"] = b.Spec.Name
	if b.Spec.AgentModel != "" {
		env["VMBOX_AGENT_MODEL"] = b.Spec.AgentModel
	}
	if b.Spec.AgentReasoningEffort != "" {
		env["VMBOX_AGENT_REASONING_EFFORT"] = b.Spec.AgentReasoningEffort
	}
	if b.Spec.Agent != "opencode" || b.Spec.ModelSource == "profile" {
		delete(env, "VMBOX_MODEL_URL")
		delete(env, "VMBOX_MODEL")
		delete(env, "VMBOX_MODEL_VISION")
	}
	req := provider.CreateRequest{Name: b.ID, Image: e.Image, Owner: e.owner(b), Resources: provider.Resources{CPU: b.Spec.CPU, MemoryMiB: b.Spec.MemoryMiB, PIDs: 512}, Env: env, Command: []string{"vmbox-standalone"}}
	dir := filepath.Join(e.Dir, "instances", b.ID)
	if err = os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(dir, "owner"), []byte(b.ID), 0600); err != nil {
		return err
	}
	_, err = e.manager(b).Provision(ctx, req, append(archives, pw)...)
	return err
}
func (e *DockerEngine) Inspect(ctx context.Context, b Box) (Observation, error) {
	r, err := e.Runner.Run(ctx, []string{"docker", "inspect", "vmbox-" + b.ID}, nil, nil, nil)
	if err != nil {
		return Observation{}, err
	}
	if r.ExitCode != 0 {
		if strings.Contains(strings.ToLower(string(r.Stderr)), "no such") {
			return Observation{}, nil
		}
		return Observation{}, fmt.Errorf("cannot inspect worker")
	}
	var a []struct {
		Config          struct{ Labels map[string]string }
		State           struct{ Running bool }
		NetworkSettings struct {
			Ports map[string][]struct{ HostIP, HostPort string }
		}
	}
	if json.Unmarshal(r.Stdout, &a) != nil || len(a) != 1 {
		return Observation{}, fmt.Errorf("invalid worker metadata")
	}
	v := a[0]
	if v.Config.Labels["dev.vmbox.account"] != e.HubID || v.Config.Labels["dev.vmbox.box"] != b.ID || v.Config.Labels["dev.vmbox.lease"] != b.ID {
		return Observation{}, fmt.Errorf("worker ownership mismatch")
	}
	o := Observation{Exists: true, Running: v.State.Running}
	for _, p := range v.NetworkSettings.Ports["8080/tcp"] {
		if p.HostIP == "127.0.0.1" {
			o.Port, _ = strconv.Atoi(p.HostPort)
		}
	}
	if o.Running && o.Port < 1 {
		return Observation{}, fmt.Errorf("worker private port unavailable")
	}
	return o, nil
}
func (e *DockerEngine) Start(ctx context.Context, b Box) error {
	_, err := e.manager(b).Start(ctx, b.ID)
	return err
}
func (e *DockerEngine) Stop(ctx context.Context, b Box) error {
	_, err := e.manager(b).Stop(ctx, b.ID)
	return err
}
func (e *DockerEngine) Delete(ctx context.Context, b Box) error {
	if err := e.manager(b).Delete(ctx, b.ID, e.owner(b)); err != nil {
		return err
	}
	dir := filepath.Join(e.Dir, "instances", b.ID)
	v, err := os.ReadFile(filepath.Join(dir, "owner"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil || string(v) != b.ID {
		return fmt.Errorf("instance directory ownership mismatch")
	}
	return os.RemoveAll(dir)
}
func (e *DockerEngine) Gateway(ctx context.Context, b Box) (Gateway, error) {
	o, err := e.Inspect(ctx, b)
	if err != nil || !o.Running || o.Port == 0 {
		return Gateway{}, fmt.Errorf("worker gateway unavailable")
	}
	r, err := e.Runner.Run(ctx, []string{"docker", "exec", "--user", "vmbox", "vmbox-" + b.ID, "cat", "/data/standalone/config.json"}, nil, nil, nil)
	if err != nil || r.ExitCode != 0 {
		return Gateway{}, fmt.Errorf("worker gateway initializing")
	}
	var g Gateway
	if json.Unmarshal(r.Stdout, &g) != nil {
		return g, fmt.Errorf("invalid gateway metadata")
	}
	g.Port = o.Port
	for _, scope := range []string{"view", "desktop", "chat"} {
		if len(g.Capabilities[scope]) != 64 {
			return Gateway{}, fmt.Errorf("incomplete gateway capabilities")
		}
	}
	return g, nil
}

var operationTimeout = 2 * time.Minute
