package incus

import (
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

type Config struct {
	Remote       string
	Project      string
	DefaultImage string
	VM           bool
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
		cfg.DefaultImage = "images:ubuntu/24.04"
	}
	return &Provider{cfg: cfg, runner: runner}
}
func (p *Provider) Name() string { return "incus" }
func (p *Provider) target(name string) string {
	if p.cfg.Remote == "" {
		return name
	}
	return strings.TrimSuffix(p.cfg.Remote, ":") + ":" + name
}
func (p *Provider) command(args ...string) []string {
	argv := []string{"incus"}
	if p.cfg.Project != "" {
		argv = append(argv, "--project", p.cfg.Project)
	}
	return append(argv, args...)
}
func (p *Provider) run(ctx context.Context, args ...string) (procexec.Result, error) {
	return p.runner.Run(ctx, p.command(args...), nil, nil, nil)
}

func (p *Provider) Validate(ctx context.Context) (provider.Capabilities, error) {
	cap := provider.Capabilities{Provider: p.Name(), Architectures: []string{"linux/amd64", "linux/arm64"}, Interactive: true, Detached: true, ExactArgv: true, PersistentStorage: true, AutomatedStorage: true, Resize: true, Metrics: true, ControllerCompatible: true}
	result, err := p.run(ctx, "version")
	if err != nil || result.ExitCode != 0 {
		return cap, incusError("validate", result, err)
	}
	return cap, nil
}

type instance struct {
	Name         string            `json:"name"`
	Status       string            `json:"status"`
	Type         string            `json:"type"`
	Architecture string            `json:"architecture"`
	CreatedAt    time.Time         `json:"created_at"`
	LastUsedAt   time.Time         `json:"last_used_at"`
	Config       map[string]string `json:"config"`
}

func instanceName(name string) string { return "vmbox-" + name }
func (p *Provider) instances(ctx context.Context) ([]instance, error) {
	result, err := p.run(ctx, "list", "--format", "json")
	if err != nil || result.ExitCode != 0 {
		return nil, incusError("list", result, err)
	}
	var rows []instance
	if err := json.Unmarshal(result.Stdout, &rows); err != nil {
		return nil, err
	}
	return rows, nil
}
func (p *Provider) resolve(ctx context.Context, id string) (instance, error) {
	rows, err := p.instances(ctx)
	if err != nil {
		return instance{}, err
	}
	for _, row := range rows {
		if row.Name == id || row.Name == instanceName(id) {
			return row, nil
		}
	}
	return instance{}, provider.ErrNotFound
}

func (p *Provider) Create(ctx context.Context, req provider.CreateRequest) (provider.Box, error) {
	if err := provider.ValidateName(req.Name); err != nil {
		return provider.Box{}, err
	}
	if req.Owner.BoxID == "" {
		req.Owner.BoxID = req.Name
	}
	if existing, err := p.resolve(ctx, req.Name); err == nil {
		box := p.toBox(existing)
		if err := provider.VerifyOwner(box.Owner, req.Owner); err != nil {
			return provider.Box{}, err
		}
		return box, nil
	} else if !errors.Is(err, provider.ErrNotFound) {
		return provider.Box{}, err
	}
	image := req.Image
	if image == "" {
		image = p.cfg.DefaultImage
	}
	name := p.target(instanceName(req.Name))
	args := []string{"launch", image, name, "--config", "security.privileged=false", "--config", "user.vmbox.managed=true", "--config", "user.vmbox.account=" + req.Owner.AccountID, "--config", "user.vmbox.box=" + req.Owner.BoxID, "--config", "user.vmbox.run=" + req.Owner.RunID, "--config", "user.vmbox.lease=" + req.Owner.Lease}
	for key, value := range req.Env {
		args = append(args, "--config", "environment."+key+"="+value)
	}
	if p.cfg.VM {
		args = append(args, "--vm")
	}
	if req.Resources.CPU > 0 {
		args = append(args, "--config", "limits.cpu="+strconv.FormatFloat(req.Resources.CPU, 'f', -1, 64))
	}
	if req.Resources.MemoryMiB > 0 {
		args = append(args, "--config", fmt.Sprintf("limits.memory=%dMiB", req.Resources.MemoryMiB))
	}
	result, err := p.run(ctx, args...)
	if err != nil || result.ExitCode != 0 {
		return provider.Box{}, incusError("launch", result, err)
	}
	storage, err := p.CreateStorage(ctx, req.Name, req.Resources)
	if err != nil {
		return provider.Box{}, err
	}
	if err := p.AttachStorage(ctx, req.Name, storage); err != nil {
		return provider.Box{}, err
	}
	return p.Inspect(ctx, req.Name)
}

func (p *Provider) toBox(row instance) provider.Box {
	state := provider.StateStopped
	if row.Status == "Running" {
		state = provider.StateRunning
	} else if row.Status == "Error" {
		state = provider.StateFailed
	}
	cpu, _ := strconv.ParseFloat(row.Config["limits.cpu"], 64)
	memory := parseMiB(row.Config["limits.memory"])
	name := strings.TrimPrefix(row.Name, "vmbox-")
	return provider.Box{ID: row.Name, Name: name, Provider: p.Name(), State: state, ProviderState: row.Status, Owner: provider.Owner{AccountID: row.Config["user.vmbox.account"], BoxID: row.Config["user.vmbox.box"], RunID: row.Config["user.vmbox.run"], Lease: row.Config["user.vmbox.lease"]}, Resources: provider.Resources{CPU: cpu, MemoryMiB: memory}, CreatedAt: row.CreatedAt, UpdatedAt: row.LastUsedAt, Connection: provider.Connection{Transport: "incus-exec", Endpoint: p.target(row.Name)}, Storage: &provider.Storage{Name: instanceName(name) + "-data", MountPath: "/data"}}
}
func parseMiB(value string) int64 {
	value = strings.TrimSuffix(value, "MiB")
	n, _ := strconv.ParseInt(value, 10, 64)
	return n
}
func (p *Provider) Inspect(ctx context.Context, id string) (provider.Box, error) {
	row, err := p.resolve(ctx, id)
	if err != nil {
		return provider.Box{}, err
	}
	return p.toBox(row), nil
}
func (p *Provider) List(ctx context.Context) ([]provider.Box, error) {
	rows, err := p.instances(ctx)
	if err != nil {
		return nil, err
	}
	boxes := []provider.Box{}
	for _, row := range rows {
		if row.Config["user.vmbox.managed"] == "true" {
			boxes = append(boxes, p.toBox(row))
		}
	}
	return boxes, nil
}

func (p *Provider) Start(ctx context.Context, id string) (provider.Box, error) {
	row, err := p.resolve(ctx, id)
	if err != nil {
		return provider.Box{}, err
	}
	result, err := p.run(ctx, "start", p.target(row.Name))
	if err != nil || result.ExitCode != 0 {
		return provider.Box{}, incusError("start", result, err)
	}
	return p.Inspect(ctx, id)
}
func (p *Provider) Stop(ctx context.Context, id string) (provider.Box, error) {
	row, err := p.resolve(ctx, id)
	if err != nil {
		return provider.Box{}, err
	}
	result, err := p.run(ctx, "stop", p.target(row.Name), "--timeout", "30")
	if err != nil || result.ExitCode != 0 {
		return provider.Box{}, incusError("stop", result, err)
	}
	return p.Inspect(ctx, id)
}
func (p *Provider) Resize(ctx context.Context, id string, resources provider.Resources) (provider.Box, error) {
	row, err := p.resolve(ctx, id)
	if err != nil {
		return provider.Box{}, err
	}
	if resources.CPU > 0 {
		result, err := p.run(ctx, "config", "set", p.target(row.Name), "limits.cpu", strconv.FormatFloat(resources.CPU, 'f', -1, 64))
		if err != nil || result.ExitCode != 0 {
			return provider.Box{}, incusError("resize CPU", result, err)
		}
	}
	if resources.MemoryMiB > 0 {
		result, err := p.run(ctx, "config", "set", p.target(row.Name), "limits.memory", fmt.Sprintf("%dMiB", resources.MemoryMiB))
		if err != nil || result.ExitCode != 0 {
			return provider.Box{}, incusError("resize memory", result, err)
		}
	}
	return p.Inspect(ctx, id)
}
func (p *Provider) Delete(ctx context.Context, id string, requested provider.Owner) error {
	box, err := p.Inspect(ctx, id)
	if errors.Is(err, provider.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := provider.VerifyOwner(box.Owner, requested); err != nil {
		return err
	}
	result, err := p.run(ctx, "delete", p.target(box.ID), "--force")
	if err != nil || result.ExitCode != 0 {
		return incusError("delete instance", result, err)
	}
	if box.Storage != nil {
		return p.DeleteStorage(ctx, *box.Storage, requested)
	}
	return nil
}

func (p *Provider) CreateStorage(ctx context.Context, id string, resources provider.Resources) (provider.Storage, error) {
	row, err := p.resolve(ctx, id)
	if err != nil {
		return provider.Storage{}, err
	}
	box := p.toBox(row)
	name := instanceName(box.Name) + "-data"
	args := []string{"storage", "volume", "create", p.target("default"), name, "--type", "filesystem", "user.vmbox.account=" + box.Owner.AccountID, "user.vmbox.box=" + box.Owner.BoxID, "user.vmbox.lease=" + box.Owner.Lease}
	if resources.DiskGiB > 0 {
		args = append(args, "size="+strconv.FormatInt(resources.DiskGiB, 10)+"GiB")
	}
	result, err := p.run(ctx, args...)
	if err != nil || (result.ExitCode != 0 && !strings.Contains(string(result.Stderr), "already exists")) {
		return provider.Storage{}, incusError("create storage", result, err)
	}
	return provider.Storage{Name: name, MountPath: "/data", SizeGiB: resources.DiskGiB}, nil
}
func (p *Provider) AttachStorage(ctx context.Context, id string, storage provider.Storage) error {
	row, err := p.resolve(ctx, id)
	if err != nil {
		return err
	}
	result, err := p.run(ctx, "config", "device", "add", p.target(row.Name), "data", "disk", "pool=default", "source="+storage.Name, "path=/data")
	if err != nil || (result.ExitCode != 0 && !strings.Contains(string(result.Stderr), "already exists")) {
		return incusError("attach storage", result, err)
	}
	return nil
}
func (p *Provider) DeleteStorage(ctx context.Context, storage provider.Storage, owner provider.Owner) error {
	actual := provider.Owner{}
	for key, dst := range map[string]*string{"user.vmbox.account": &actual.AccountID, "user.vmbox.box": &actual.BoxID, "user.vmbox.lease": &actual.Lease} {
		result, err := p.run(ctx, "storage", "volume", "get", p.target("default"), storage.Name, key)
		if err != nil || result.ExitCode != 0 {
			return incusError("inspect storage ownership", result, err)
		}
		*dst = strings.TrimSpace(string(result.Stdout))
	}
	if err := provider.VerifyOwner(actual, owner); err != nil {
		return err
	}
	result, err := p.run(ctx, "storage", "volume", "delete", p.target("default"), storage.Name)
	if err != nil || (result.ExitCode != 0 && !strings.Contains(string(result.Stderr), "not found")) {
		return incusError("delete storage", result, err)
	}
	return nil
}
func (p *Provider) Deploy(ctx context.Context, id, image string) (provider.Box, error) {
	row, err := p.resolve(ctx, id)
	if err != nil {
		return provider.Box{}, err
	}
	if image == "" {
		return p.Start(ctx, id)
	}
	result, err := p.run(ctx, "rebuild", image, p.target(row.Name), "--force")
	if err != nil || result.ExitCode != 0 {
		return provider.Box{}, incusError("rebuild image", result, err)
	}
	return p.Inspect(ctx, id)
}
func (p *Provider) Connection(ctx context.Context, id string) (provider.Connection, error) {
	row, err := p.resolve(ctx, id)
	if err != nil {
		return provider.Connection{}, err
	}
	return provider.Connection{Transport: "incus-exec", Endpoint: p.target(row.Name)}, nil
}
func (p *Provider) Logs(ctx context.Context, id string, opts provider.LogOptions, dst io.Writer) error {
	row, err := p.resolve(ctx, id)
	if err != nil {
		return err
	}
	current, err := p.run(ctx, "exec", p.target(row.Name), "--", "cat", "/data/.vmbox/current-run")
	if err != nil || current.ExitCode != 0 {
		return incusError("read current run", current, err)
	}
	runID := strings.TrimSpace(string(current.Stdout))
	if runID == "" {
		return fmt.Errorf("box has no runtime event log")
	}
	result, err := p.runner.Run(ctx, p.command("exec", p.target(row.Name), "--", "cat", "/data/.vmbox/runs/"+runID+"/events.ndjson"), nil, dst, dst)
	if err != nil || result.ExitCode != 0 {
		return incusError("read runtime logs", result, err)
	}
	if opts.Follow {
		return fmt.Errorf("Incus log follow requires controller event streaming: %w", provider.ErrUnsupported)
	}
	return nil
}
func (p *Provider) Usage(ctx context.Context, id string) (provider.Usage, error) {
	_, err := p.resolve(ctx, id)
	if err != nil {
		return provider.Usage{}, err
	}
	return provider.Usage{ObservedAt: time.Now().UTC(), Cost: provider.Cost{Available: false, Detail: "host-local provider does not expose billing"}}, nil
}
func (p *Provider) Exec(ctx context.Context, id string, argv []string, opts provider.ExecOptions) (provider.ExecResult, error) {
	if len(argv) == 0 {
		return provider.ExecResult{}, fmt.Errorf("command argv cannot be empty")
	}
	row, err := p.resolve(ctx, id)
	if err != nil {
		return provider.ExecResult{}, err
	}
	args := []string{"exec", p.target(row.Name)}
	if opts.Interactive {
		args = append(args, "--mode", "interactive")
	}
	args = append(args, "--")
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
	actual, err := p.Inspect(ctx, desired.ID)
	if err != nil {
		return provider.Box{}, err
	}
	if desired.State == provider.StateRunning && actual.State == provider.StateStopped {
		return p.Start(ctx, actual.ID)
	}
	if desired.State == provider.StateStopped && actual.State == provider.StateRunning {
		return p.Stop(ctx, actual.ID)
	}
	return actual, nil
}
func incusError(action string, result procexec.Result, err error) error {
	if err != nil {
		return fmt.Errorf("Incus %s: %w", action, err)
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("Incus %s (exit %d): %s", action, result.ExitCode, strings.TrimSpace(string(result.Stderr)))
	}
	return nil
}
