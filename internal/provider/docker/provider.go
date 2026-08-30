package docker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/0xikarus/vmbox-service/internal/procexec"
	"github.com/0xikarus/vmbox-service/internal/provider"
)

const (
	labelManaged = "dev.vmbox.managed"
	labelAccount = "dev.vmbox.account"
	labelBox     = "dev.vmbox.box"
	labelRun     = "dev.vmbox.run"
	labelLease   = "dev.vmbox.lease"
)

type Config struct {
	Context          string
	Host             string
	TLSVerify        bool
	CertPath         string
	DefaultImage     string
	LocalCostPerHour float64
}

type Provider struct {
	cfg    Config
	runner procexec.Runner
}

func New(cfg Config, runner procexec.Runner) *Provider {
	if runner == nil {
		runner = procexec.OSRunner{}
	}
	if cfg.DefaultImage == "" {
		cfg.DefaultImage = "ghcr.io/0xikarus/vmbox-service:latest"
	}
	return &Provider{cfg: cfg, runner: runner}
}

func (p *Provider) Name() string { return "docker" }

func (p *Provider) command(args ...string) []string {
	argv := []string{"docker"}
	if p.cfg.Context != "" {
		argv = append(argv, "--context", p.cfg.Context)
	}
	if p.cfg.Host != "" {
		argv = append(argv, "--host", p.cfg.Host)
	}
	if p.cfg.TLSVerify {
		argv = append(argv, "--tlsverify")
		if p.cfg.CertPath != "" {
			argv = append(argv, "--tlscacert", p.cfg.CertPath+"/ca.pem", "--tlscert", p.cfg.CertPath+"/cert.pem", "--tlskey", p.cfg.CertPath+"/key.pem")
		}
	}
	return append(argv, args...)
}

func (p *Provider) run(ctx context.Context, args ...string) (procexec.Result, error) {
	return p.runner.Run(ctx, p.command(args...), nil, nil, nil)
}

func (p *Provider) Validate(ctx context.Context) (provider.Capabilities, error) {
	cap := provider.Capabilities{Provider: p.Name(), Architectures: []string{"linux/amd64", "linux/arm64"}, Interactive: true, Detached: true, ExactArgv: true, PersistentStorage: true, AutomatedStorage: true, Resize: true, Metrics: true, ControllerCompatible: true}
	if strings.HasPrefix(p.cfg.Host, "tcp://") && !p.cfg.TLSVerify {
		return cap, fmt.Errorf("refusing unauthenticated Docker TCP endpoint; use SSH or mutually authenticated TLS")
	}
	result, err := p.run(ctx, "version", "--format", "{{json .}}")
	if err != nil {
		return cap, err
	}
	if result.ExitCode != 0 {
		return cap, fmt.Errorf("docker version failed: %s", strings.TrimSpace(string(result.Stderr)))
	}
	if p.cfg.LocalCostPerHour > 0 {
		cap.Cost = true
	}
	return cap, nil
}

func names(name string) (container, volume, network string) {
	base := "vmbox-" + name
	return base, base + "-data", base + "-net"
}

func labels(owner provider.Owner) []string {
	values := map[string]string{labelManaged: "true", labelAccount: owner.AccountID, labelBox: owner.BoxID, labelRun: owner.RunID, labelLease: owner.Lease}
	result := make([]string, 0, len(values)*2)
	for _, key := range []string{labelManaged, labelAccount, labelBox, labelRun, labelLease} {
		if values[key] != "" {
			result = append(result, "--label", key+"="+values[key])
		}
	}
	return result
}

func (p *Provider) Create(ctx context.Context, req provider.CreateRequest) (provider.Box, error) {
	if err := provider.ValidateName(req.Name); err != nil {
		return provider.Box{}, err
	}
	if req.Owner.BoxID == "" {
		req.Owner.BoxID = req.Name
	}
	if req.Image == "" {
		req.Image = p.cfg.DefaultImage
	}
	container, volume, network := names(req.Name)
	if existing, err := p.Inspect(ctx, req.Name); err == nil {
		if err := provider.VerifyOwner(existing.Owner, req.Owner); err != nil {
			return provider.Box{}, err
		}
		return existing, nil
	} else if !errors.Is(err, provider.ErrNotFound) {
		return provider.Box{}, err
	}
	localImage, localErr := p.run(ctx, "image", "inspect", req.Image)
	if localErr != nil || localImage.ExitCode != 0 {
		if result, err := p.run(ctx, "image", "pull", req.Image); err != nil || result.ExitCode != 0 {
			return provider.Box{}, commandError("pull image", result, err)
		}
	}
	labelArgs := labels(req.Owner)
	if result, err := p.run(ctx, append([]string{"volume", "create"}, append(labelArgs, volume)...)...); err != nil || result.ExitCode != 0 {
		return provider.Box{}, commandError("create volume", result, err)
	}
	// A dedicated bridge isolates boxes from one another while retaining the
	// outbound connectivity development workloads need for Git and package APIs.
	if result, err := p.run(ctx, append([]string{"network", "create", "--driver", "bridge"}, append(labelArgs, network)...)...); err != nil || (result.ExitCode != 0 && !strings.Contains(string(result.Stderr), "already exists")) {
		return provider.Box{}, commandError("create network", result, err)
	}
	if err := p.verifyLabels(ctx, "volume", volume, req.Owner); err != nil {
		return provider.Box{}, err
	}
	if err := p.verifyLabels(ctx, "network", network, req.Owner); err != nil {
		return provider.Box{}, err
	}
	args := []string{"container", "create", "--name", container, "--network", network, "--mount", "type=volume,src=" + volume + ",dst=/data", "--restart", "unless-stopped"}
	args = append(args, labelArgs...)
	if req.Resources.CPU > 0 {
		args = append(args, "--cpus", strconv.FormatFloat(req.Resources.CPU, 'f', -1, 64))
	}
	if req.Resources.MemoryMiB > 0 {
		args = append(args, "--memory", fmt.Sprintf("%dm", req.Resources.MemoryMiB))
	}
	if req.Resources.PIDs > 0 {
		args = append(args, "--pids-limit", strconv.FormatInt(req.Resources.PIDs, 10))
	}
	for key, value := range req.Env {
		args = append(args, "--env", key+"="+value)
	}
	args = append(args, req.Image)
	if len(req.Command) > 0 {
		args = append(args, req.Command...)
	}
	if result, err := p.run(ctx, args...); err != nil || result.ExitCode != 0 {
		return provider.Box{}, commandError("create container", result, err)
	}
	if result, err := p.run(ctx, "container", "start", container); err != nil || result.ExitCode != 0 {
		return provider.Box{}, commandError("start container", result, err)
	}
	return p.Inspect(ctx, req.Name)
}

type inspectData struct {
	ID      string `json:"Id"`
	Name    string `json:"Name"`
	Created string `json:"Created"`
	Config  struct {
		Image  string            `json:"Image"`
		Cmd    []string          `json:"Cmd"`
		Labels map[string]string `json:"Labels"`
	} `json:"Config"`
	State struct {
		Status  string `json:"Status"`
		Running bool   `json:"Running"`
	} `json:"State"`
	HostConfig struct {
		NanoCPUs  int64  `json:"NanoCpus"`
		Memory    int64  `json:"Memory"`
		PidsLimit *int64 `json:"PidsLimit"`
	} `json:"HostConfig"`
	Image string `json:"Image"`
}

func (p *Provider) Inspect(ctx context.Context, id string) (provider.Box, error) {
	container, volume, _ := names(id)
	result, err := p.run(ctx, "container", "inspect", container)
	if err != nil {
		return provider.Box{}, err
	}
	if result.ExitCode != 0 {
		if strings.Contains(string(result.Stderr), "No such") {
			return provider.Box{}, provider.ErrNotFound
		}
		return provider.Box{}, commandError("inspect container", result, nil)
	}
	var rows []inspectData
	if err := json.Unmarshal(result.Stdout, &rows); err != nil || len(rows) != 1 {
		return provider.Box{}, fmt.Errorf("decode docker inspect: %w", err)
	}
	r := rows[0]
	state := provider.StateStopped
	if r.State.Running {
		state = provider.StateRunning
	} else if r.State.Status == "dead" || r.State.Status == "exited" {
		state = provider.StateFailed
	}
	created, _ := time.Parse(time.RFC3339Nano, r.Created)
	pids := int64(0)
	if r.HostConfig.PidsLimit != nil {
		pids = *r.HostConfig.PidsLimit
	}
	owner := provider.Owner{AccountID: r.Config.Labels[labelAccount], BoxID: r.Config.Labels[labelBox], RunID: r.Config.Labels[labelRun], Lease: r.Config.Labels[labelLease]}
	return provider.Box{ID: r.ID, Name: id, Provider: p.Name(), State: state, ProviderState: r.State.Status, Image: r.Config.Image, ImageDigest: r.Image, Owner: owner, Labels: r.Config.Labels, CreatedAt: created, UpdatedAt: time.Now().UTC(), Resources: provider.Resources{CPU: float64(r.HostConfig.NanoCPUs) / 1e9, MemoryMiB: r.HostConfig.Memory / (1024 * 1024), PIDs: pids}, Storage: &provider.Storage{Name: volume, MountPath: "/data"}, Connection: provider.Connection{Transport: "docker-exec", Endpoint: p.cfg.Context}}, nil
}

func (p *Provider) List(ctx context.Context) ([]provider.Box, error) {
	result, err := p.run(ctx, "container", "ls", "--all", "--filter", "label="+labelManaged+"=true", "--format", "{{.Names}}")
	if err != nil || result.ExitCode != 0 {
		return nil, commandError("list containers", result, err)
	}
	var boxes []provider.Box
	for _, container := range strings.Fields(string(result.Stdout)) {
		name := strings.TrimPrefix(container, "vmbox-")
		box, err := p.Inspect(ctx, name)
		if err != nil {
			return nil, err
		}
		boxes = append(boxes, box)
	}
	return boxes, nil
}

func (p *Provider) Start(ctx context.Context, id string) (provider.Box, error) {
	container, _, _ := names(id)
	result, err := p.run(ctx, "container", "start", container)
	if err != nil || result.ExitCode != 0 {
		return provider.Box{}, commandError("start container", result, err)
	}
	return p.Inspect(ctx, id)
}

func (p *Provider) Stop(ctx context.Context, id string) (provider.Box, error) {
	container, _, _ := names(id)
	result, err := p.run(ctx, "container", "stop", container)
	if err != nil || result.ExitCode != 0 {
		return provider.Box{}, commandError("stop container", result, err)
	}
	return p.Inspect(ctx, id)
}

func (p *Provider) Resize(ctx context.Context, id string, resources provider.Resources) (provider.Box, error) {
	container, _, _ := names(id)
	args := []string{"container", "update"}
	if resources.CPU > 0 {
		args = append(args, "--cpus", strconv.FormatFloat(resources.CPU, 'f', -1, 64))
	}
	if resources.MemoryMiB > 0 {
		args = append(args, "--memory", fmt.Sprintf("%dm", resources.MemoryMiB), "--memory-swap", fmt.Sprintf("%dm", resources.MemoryMiB))
	}
	if resources.PIDs > 0 {
		args = append(args, "--pids-limit", strconv.FormatInt(resources.PIDs, 10))
	}
	args = append(args, container)
	result, err := p.run(ctx, args...)
	if err != nil || result.ExitCode != 0 {
		return provider.Box{}, commandError("resize container", result, err)
	}
	return p.Inspect(ctx, id)
}

func (p *Provider) Delete(ctx context.Context, id string, owner provider.Owner) error {
	box, err := p.Inspect(ctx, id)
	if errors.Is(err, provider.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := provider.VerifyOwner(box.Owner, owner); err != nil {
		return err
	}
	container, volume, network := names(id)
	if err := p.verifyLabels(ctx, "network", network, owner); err != nil {
		return err
	}
	if err := p.verifyLabels(ctx, "volume", volume, owner); err != nil {
		return err
	}
	for _, args := range [][]string{{"container", "rm", "--force", container}, {"network", "rm", network}, {"volume", "rm", volume}} {
		result, err := p.run(ctx, args...)
		if err != nil || (result.ExitCode != 0 && !strings.Contains(string(result.Stderr), "No such")) {
			return commandError("delete owned Docker resource", result, err)
		}
	}
	return nil
}

func (p *Provider) verifyLabels(ctx context.Context, kind, name string, owner provider.Owner) error {
	result, err := p.run(ctx, kind, "inspect", name, "--format", "{{json .Labels}}")
	if err != nil || result.ExitCode != 0 {
		return commandError("inspect owned "+kind, result, err)
	}
	var values map[string]string
	if err := json.Unmarshal(bytes.TrimSpace(result.Stdout), &values); err != nil {
		return fmt.Errorf("decode %s ownership labels: %w", kind, err)
	}
	actual := provider.Owner{AccountID: values[labelAccount], BoxID: values[labelBox], RunID: values[labelRun], Lease: values[labelLease]}
	return provider.VerifyOwner(actual, owner)
}

func (p *Provider) CreateStorage(ctx context.Context, id string, resources provider.Resources) (provider.Storage, error) {
	box, err := p.Inspect(ctx, id)
	if err != nil {
		return provider.Storage{}, err
	}
	_, volume, _ := names(id)
	result, err := p.run(ctx, append([]string{"volume", "create"}, append(labels(box.Owner), volume)...)...)
	if err != nil || result.ExitCode != 0 {
		return provider.Storage{}, commandError("create volume", result, err)
	}
	return provider.Storage{Name: volume, MountPath: "/data", SizeGiB: resources.DiskGiB}, nil
}

func (p *Provider) AttachStorage(context.Context, string, provider.Storage) error { return nil }

func (p *Provider) DeleteStorage(ctx context.Context, storage provider.Storage, owner provider.Owner) error {
	result, err := p.run(ctx, "volume", "inspect", storage.Name, "--format", "{{json .Labels}}")
	if err != nil || result.ExitCode != 0 {
		if strings.Contains(string(result.Stderr), "No such") {
			return nil
		}
		return commandError("inspect volume", result, err)
	}
	var values map[string]string
	if err := json.Unmarshal(bytes.TrimSpace(result.Stdout), &values); err != nil {
		return err
	}
	if err := provider.VerifyOwner(provider.Owner{AccountID: values[labelAccount], BoxID: values[labelBox], Lease: values[labelLease]}, owner); err != nil {
		return err
	}
	result, err = p.run(ctx, "volume", "rm", storage.Name)
	return commandError("delete volume", result, err)
}

func (p *Provider) Deploy(ctx context.Context, id, image string) (provider.Box, error) {
	box, err := p.Inspect(ctx, id)
	if err != nil {
		return provider.Box{}, err
	}
	if image == "" {
		image = box.Image
	}
	container, _, _ := names(id)
	details, err := p.run(ctx, "container", "inspect", container)
	if err != nil || details.ExitCode != 0 {
		return provider.Box{}, commandError("inspect before deploy", details, err)
	}
	var rows []inspectData
	if err := json.Unmarshal(details.Stdout, &rows); err != nil || len(rows) != 1 {
		return provider.Box{}, fmt.Errorf("decode container command: %w", err)
	}
	result, err := p.run(ctx, "container", "rm", "--force", container)
	if err != nil || result.ExitCode != 0 {
		return provider.Box{}, commandError("remove old container", result, err)
	}
	return p.Create(ctx, provider.CreateRequest{Name: id, Image: image, Resources: box.Resources, Owner: box.Owner, Command: rows[0].Config.Cmd})
}

func (p *Provider) Connection(_ context.Context, id string) (provider.Connection, error) {
	container, _, _ := names(id)
	return provider.Connection{Transport: "docker-exec", Endpoint: container, Metadata: map[string]string{"context": p.cfg.Context}}, nil
}

func (p *Provider) Logs(ctx context.Context, id string, opts provider.LogOptions, dst io.Writer) error {
	container, _, _ := names(id)
	args := []string{"container", "logs"}
	if opts.Follow {
		args = append(args, "--follow")
	}
	if opts.Tail > 0 {
		args = append(args, "--tail", strconv.Itoa(opts.Tail))
	}
	if !opts.Since.IsZero() {
		args = append(args, "--since", opts.Since.Format(time.RFC3339))
	}
	args = append(args, container)
	result, err := p.runner.Run(ctx, p.command(args...), nil, dst, dst)
	if err != nil || result.ExitCode != 0 {
		return commandError("docker logs", result, err)
	}
	return nil
}

func (p *Provider) Usage(ctx context.Context, id string) (provider.Usage, error) {
	container, _, _ := names(id)
	result, err := p.run(ctx, "container", "stats", "--no-stream", "--format", "{{json .}}", container)
	if err != nil || result.ExitCode != 0 {
		return provider.Usage{}, commandError("docker stats", result, err)
	}
	var row struct{ CPUPerc, MemUsage string }
	_ = json.Unmarshal(bytes.TrimSpace(result.Stdout), &row)
	cpu, _ := strconv.ParseFloat(strings.TrimSuffix(row.CPUPerc, "%"), 64)
	usage := provider.Usage{CPUPercent: cpu, ObservedAt: time.Now().UTC(), Cost: provider.Cost{Available: p.cfg.LocalCostPerHour > 0, Currency: "USD", Estimated: true, Detail: "locally configured hourly rate"}}
	return usage, nil
}

func (p *Provider) Exec(ctx context.Context, id string, argv []string, opts provider.ExecOptions) (provider.ExecResult, error) {
	if len(argv) == 0 {
		return provider.ExecResult{}, fmt.Errorf("command argv cannot be empty")
	}
	container, _, _ := names(id)
	args := []string{"container", "exec"}
	if opts.Interactive {
		args = append(args, "--interactive", "--tty")
	} else if opts.Stdin != nil {
		args = append(args, "--interactive")
	}
	args = append(args, container)
	if opts.Detach {
		args = append(args, "vmbox-runtime", "run", "--detach", "--")
	}
	args = append(args, argv...)
	started := time.Now().UTC()
	result, err := p.runner.Run(ctx, p.command(args...), opts.Stdin, opts.Stdout, opts.Stderr)
	if err != nil {
		return provider.ExecResult{}, err
	}
	return provider.ExecResult{ExitCode: result.ExitCode, Stdout: string(result.Stdout), Stderr: string(result.Stderr), StartedAt: started, FinishedAt: time.Now().UTC()}, nil
}

func (p *Provider) Reconcile(ctx context.Context, desired provider.Box) (provider.Box, error) {
	actual, err := p.Inspect(ctx, desired.Name)
	if err != nil {
		return provider.Box{}, err
	}
	if desired.State == provider.StateRunning && actual.State == provider.StateStopped {
		return p.Start(ctx, desired.Name)
	}
	if desired.State == provider.StateStopped && actual.State == provider.StateRunning {
		return p.Stop(ctx, desired.Name)
	}
	return actual, nil
}

func commandError(action string, result procexec.Result, err error) error {
	if err != nil {
		return fmt.Errorf("%s: %w", action, err)
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("%s (exit %d): %s", action, result.ExitCode, strings.TrimSpace(string(result.Stderr)))
	}
	return nil
}
