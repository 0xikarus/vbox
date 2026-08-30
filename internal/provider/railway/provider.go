package railway

import (
	"context"
	"encoding/base64"
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
	ProjectID     string
	EnvironmentID string
	Token         string
	DefaultImage  string
	PollInterval  time.Duration
	ReadyTimeout  time.Duration
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
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 2 * time.Second
	}
	if cfg.ReadyTimeout <= 0 {
		cfg.ReadyTimeout = 15 * time.Minute
	}
	if cfg.Token != "" {
		switch value := runner.(type) {
		case procexec.OSRunner:
			env := make(map[string]string, len(value.Env)+1)
			for key, item := range value.Env {
				env[key] = item
			}
			env["RAILWAY_API_TOKEN"] = cfg.Token
			value.Env = env
			runner = value
		case *procexec.OSRunner:
			env := make(map[string]string, len(value.Env)+1)
			for key, item := range value.Env {
				env[key] = item
			}
			env["RAILWAY_API_TOKEN"] = cfg.Token
			runner = &procexec.OSRunner{Env: env}
		}
	}
	return &Provider{cfg: cfg, runner: runner}
}

func (p *Provider) Name() string { return "railway" }

func (p *Provider) target() []string {
	return []string{"--project", p.cfg.ProjectID, "--environment", p.cfg.EnvironmentID}
}
func (p *Provider) command(args ...string) []string {
	return append(append([]string{"railway"}, args...), p.target()...)
}
func (p *Provider) run(ctx context.Context, args ...string) (procexec.Result, error) {
	return p.runner.Run(ctx, p.command(args...), nil, nil, nil)
}

func (p *Provider) Validate(ctx context.Context) (provider.Capabilities, error) {
	cap := provider.Capabilities{Provider: p.Name(), Architectures: []string{"linux/amd64"}, Interactive: true, Detached: true, ExactArgv: true, PersistentStorage: true, AutomatedStorage: true, Resize: true, Metrics: true, Cost: true, ControllerCompatible: true}
	if p.cfg.ProjectID == "" || p.cfg.EnvironmentID == "" {
		return cap, fmt.Errorf("Railway project and environment IDs are required")
	}
	result, err := p.run(ctx, "service", "list", "--json")
	if err != nil || result.ExitCode != 0 {
		return cap, railwayError("validate", result, err)
	}
	return cap, nil
}

type service struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
	Regions   []struct {
		Name string `json:"name"`
	} `json:"regions"`
}

func (p *Provider) services(ctx context.Context) ([]service, error) {
	result, err := p.run(ctx, "service", "list", "--json")
	if err != nil || result.ExitCode != 0 {
		return nil, railwayError("list services", result, err)
	}
	var services []service
	if err := json.Unmarshal(result.Stdout, &services); err != nil {
		return nil, fmt.Errorf("decode Railway services: %w", err)
	}
	return services, nil
}

func serviceName(name string) string { return "vmbox-" + name }

func (p *Provider) resolve(ctx context.Context, id string) (service, error) {
	services, err := p.services(ctx)
	if err != nil {
		return service{}, err
	}
	for _, item := range services {
		if item.ID == id || item.Name == id || item.Name == serviceName(id) {
			return item, nil
		}
	}
	return service{}, provider.ErrNotFound
}

func (p *Provider) Create(ctx context.Context, req provider.CreateRequest) (provider.Box, error) {
	if err := provider.ValidateName(req.Name); err != nil {
		return provider.Box{}, err
	}
	if req.Owner.BoxID == "" {
		req.Owner.BoxID = req.Name
	}
	var service service
	var result procexec.Result
	existing, resolveErr := p.resolve(ctx, req.Name)
	if resolveErr == nil {
		box, inspectErr := p.inspectService(ctx, existing)
		if inspectErr != nil {
			return provider.Box{}, inspectErr
		}
		if err := provider.VerifyOwner(box.Owner, req.Owner); err != nil {
			return provider.Box{}, err
		}
		if status := strings.ToUpper(existing.Status); status != "" && status != "NO_DEPLOYMENT" {
			return box, nil
		}
		service = existing
	} else {
		if !errors.Is(resolveErr, provider.ErrNotFound) {
			return provider.Box{}, resolveErr
		}
		var createErr error
		result, createErr = p.run(ctx, "service", "create", "--name", serviceName(req.Name), "--json")
		if createErr != nil || result.ExitCode != 0 {
			if _, reconcileErr := p.resolve(ctx, req.Name); reconcileErr != nil {
				return provider.Box{}, railwayError("create service", result, createErr)
			}
		}
		var err error
		service, err = p.resolve(ctx, req.Name)
		if err != nil {
			return provider.Box{}, err
		}
	}
	metadata := map[string]string{"VMBOX_MANAGED": "true", "VMBOX_ACCOUNT_ID": req.Owner.AccountID, "VMBOX_BOX_ID": req.Owner.BoxID, "VMBOX_RUN_ID": req.Owner.RunID, "VMBOX_LEASE": req.Owner.Lease, "VMBOX_IMAGE": req.Image}
	if len(req.Components) > 0 {
		metadata["VMBOX_COMPONENTS"] = strings.Join(req.Components, ",")
	}
	for key, value := range req.Env {
		metadata[key] = value
	}
	args := []string{"variables", "set", "--service", service.Name, "--skip-deploys"}
	for key, value := range metadata {
		if value != "" {
			args = append(args, key+"="+value)
		}
	}
	result, err := p.run(ctx, args...)
	if err != nil || result.ExitCode != 0 {
		values, reconcileErr := p.variables(ctx, service.Name)
		if reconcileErr != nil || values["VMBOX_ACCOUNT_ID"] != req.Owner.AccountID || values["VMBOX_BOX_ID"] != req.Owner.BoxID || (req.Owner.Lease != "" && values["VMBOX_LEASE"] != req.Owner.Lease) {
			return provider.Box{}, railwayError("set ownership variables", result, err)
		}
	}
	if _, err := p.CreateStorage(ctx, service.ID, req.Resources); err != nil {
		return provider.Box{}, err
	}
	// Deploy the shared OCI runtime. Railway accepts image sources directly.
	image := req.Image
	if image == "" {
		image = p.cfg.DefaultImage
	}
	update := []string{"service", "update", "--service", service.Name, "--image", image}
	if req.Region != "" {
		update = append(update, "--region", req.Region)
	}
	if req.Resources.CPU > 0 {
		update = append(update, "--cpu", strconv.FormatFloat(req.Resources.CPU, 'f', -1, 64))
	}
	if req.Resources.MemoryMiB > 0 {
		update = append(update, "--memory", strconv.FormatInt(req.Resources.MemoryMiB, 10))
	}
	update = append(update, "--json")
	result, err = p.run(ctx, update...)
	if err != nil || result.ExitCode != 0 {
		return provider.Box{}, railwayError("configure image", result, err)
	}
	if err := p.submitAndWaitDeployment(ctx, service.Name); err != nil {
		return provider.Box{}, err
	}
	return p.Inspect(ctx, service.ID)
}

func state(status string) provider.State {
	switch strings.ToUpper(status) {
	case "SUCCESS":
		return provider.StateRunning
	case "FAILED", "CRASHED":
		return provider.StateFailed
	case "REMOVED", "NO_DEPLOYMENT":
		return provider.StateStopped
	default:
		return provider.StateProvisioning
	}
}

func (p *Provider) variables(ctx context.Context, service string) (map[string]string, error) {
	result, err := p.run(ctx, "variables", "--service", service, "--json")
	if err != nil || result.ExitCode != 0 {
		return nil, railwayError("read variables", result, err)
	}
	values := make(map[string]string)
	if err := json.Unmarshal(result.Stdout, &values); err != nil {
		return nil, err
	}
	return values, nil
}

func (p *Provider) inspectService(ctx context.Context, service service) (provider.Box, error) {
	values, err := p.variables(ctx, service.Name)
	if err != nil {
		return provider.Box{}, err
	}
	region := ""
	if len(service.Regions) > 0 {
		region = service.Regions[0].Name
	}
	return provider.Box{ID: service.ID, Name: strings.TrimPrefix(service.Name, "vmbox-"), Provider: p.Name(), State: state(service.Status), ProviderState: service.Status, Region: region, Image: values["VMBOX_IMAGE"], Owner: provider.Owner{AccountID: values["VMBOX_ACCOUNT_ID"], BoxID: values["VMBOX_BOX_ID"], RunID: values["VMBOX_RUN_ID"], Lease: values["VMBOX_LEASE"]}, CreatedAt: service.CreatedAt, UpdatedAt: service.UpdatedAt, Connection: provider.Connection{Transport: "railway-ssh", Endpoint: service.Name}, Storage: &provider.Storage{Name: service.Name + "-data", MountPath: "/data"}}, nil
}

func (p *Provider) Inspect(ctx context.Context, id string) (provider.Box, error) {
	service, err := p.resolve(ctx, id)
	if err != nil {
		return provider.Box{}, err
	}
	return p.inspectService(ctx, service)
}
func (p *Provider) List(ctx context.Context) ([]provider.Box, error) {
	services, err := p.services(ctx)
	if err != nil {
		return nil, err
	}
	boxes := make([]provider.Box, 0)
	for _, item := range services {
		if !strings.HasPrefix(item.Name, "vmbox-") {
			continue
		}
		box, err := p.inspectService(ctx, item)
		if err == nil && box.Owner.BoxID != "" {
			boxes = append(boxes, box)
		}
	}
	return boxes, nil
}

func (p *Provider) Start(ctx context.Context, id string) (provider.Box, error) {
	return p.Deploy(ctx, id, "")
}
func (p *Provider) Stop(ctx context.Context, id string) (provider.Box, error) {
	service, err := p.resolve(ctx, id)
	if err != nil {
		return provider.Box{}, err
	}
	result, err := p.run(ctx, "down", "--service", service.Name, "--yes")
	if err != nil || result.ExitCode != 0 {
		return provider.Box{}, railwayError("stop", result, err)
	}
	return p.Inspect(ctx, service.ID)
}

func (p *Provider) Resize(ctx context.Context, id string, resources provider.Resources) (provider.Box, error) {
	service, err := p.resolve(ctx, id)
	if err != nil {
		return provider.Box{}, err
	}
	args := []string{"service", "update", "--service", service.Name}
	if resources.CPU > 0 {
		args = append(args, "--cpu", strconv.FormatFloat(resources.CPU, 'f', -1, 64))
	}
	if resources.MemoryMiB > 0 {
		args = append(args, "--memory", strconv.FormatInt(resources.MemoryMiB, 10))
	}
	result, err := p.run(ctx, args...)
	if err != nil || result.ExitCode != 0 {
		return provider.Box{}, railwayError("resize", result, err)
	}
	return p.Inspect(ctx, service.ID)
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
	volumeIDs, err := p.volumeIDs(ctx, serviceName(box.Name))
	if err != nil {
		return err
	}
	result, err := p.run(ctx, "service", "delete", "--service", box.ID, "--yes", "--json")
	if err != nil || result.ExitCode != 0 {
		return railwayError("delete service", result, err)
	}
	for _, volumeID := range volumeIDs {
		result, err = p.run(ctx, "volume", "delete", "--volume", volumeID, "--yes", "--json")
		if err != nil || result.ExitCode != 0 {
			return railwayError("delete owned volume", result, err)
		}
	}
	return nil
}

type railwayVolume struct {
	ID          string `json:"id"`
	ServiceName string `json:"serviceName"`
	MountPath   string `json:"mountPath"`
	Status      string `json:"status"`
}

func (p *Provider) volumes(ctx context.Context) ([]railwayVolume, error) {
	result, err := p.run(ctx, "volume", "list", "--json")
	if err != nil || result.ExitCode != 0 {
		return nil, railwayError("list volumes", result, err)
	}
	var payload struct {
		Volumes []railwayVolume `json:"volumes"`
	}
	if err := json.Unmarshal(result.Stdout, &payload); err != nil || payload.Volumes == nil {
		var direct []railwayVolume
		if directErr := json.Unmarshal(result.Stdout, &direct); directErr != nil {
			if err != nil {
				return nil, err
			}
			return nil, directErr
		}
		payload.Volumes = direct
	}
	return payload.Volumes, nil
}

func (p *Provider) volumeIDs(ctx context.Context, service string) ([]string, error) {
	volumes, err := p.volumes(ctx)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, volume := range volumes {
		if volume.ServiceName == service && volume.MountPath == "/data" {
			ids = append(ids, volume.ID)
		}
	}
	return ids, nil
}

func (p *Provider) CreateStorage(ctx context.Context, id string, resources provider.Resources) (provider.Storage, error) {
	service, err := p.resolve(ctx, id)
	if err != nil {
		return provider.Storage{}, err
	}
	find := func(values []railwayVolume) (railwayVolume, bool) {
		for _, volume := range values {
			if volume.ServiceName == service.Name && volume.MountPath == "/data" {
				return volume, true
			}
		}
		return railwayVolume{}, false
	}
	values, listErr := p.volumes(ctx)
	_, exists := find(values)
	var result procexec.Result
	var createErr error
	if listErr != nil {
		return provider.Storage{}, listErr
	}
	if !exists {
		result, createErr = p.run(ctx, "volume", "add", "--service", service.Name, "--mount-path", "/data", "--json")
		if createErr != nil || result.ExitCode != 0 {
			values, listErr = p.volumes(ctx)
			if _, reconciled := find(values); listErr != nil || !reconciled {
				return provider.Storage{}, railwayError("create volume", result, createErr)
			}
		}
	}
	deadline := time.NewTimer(p.cfg.ReadyTimeout)
	defer deadline.Stop()
	for {
		values, err = p.volumes(ctx)
		if err != nil {
			return provider.Storage{}, err
		}
		if volume, ok := find(values); ok {
			switch strings.ToUpper(volume.Status) {
			case "", "READY", "SUCCESS", "ACTIVE", "AVAILABLE":
				return provider.Storage{ID: volume.ID, Name: service.Name + "-data", MountPath: "/data", SizeGiB: resources.DiskGiB}, nil
			case "FAILED", "ERROR", "DELETED":
				return provider.Storage{}, fmt.Errorf("Railway volume %s ended with %s", volume.ID, volume.Status)
			}
		}
		select {
		case <-ctx.Done():
			return provider.Storage{}, ctx.Err()
		case <-deadline.C:
			return provider.Storage{}, fmt.Errorf("Railway volume for %s did not become ready", service.Name)
		case <-time.After(p.cfg.PollInterval):
		}
	}
}
func (p *Provider) AttachStorage(context.Context, string, provider.Storage) error { return nil }
func (p *Provider) DeleteStorage(context.Context, provider.Storage, provider.Owner) error {
	return fmt.Errorf("Railway service deletion owns volume cleanup: %w", provider.ErrUnsupported)
}

func (p *Provider) Deploy(ctx context.Context, id, image string) (provider.Box, error) {
	service, err := p.resolve(ctx, id)
	if err != nil {
		return provider.Box{}, err
	}
	if image != "" {
		result, err := p.run(ctx, "service", "update", "--service", service.Name, "--image", image, "--json")
		if err != nil || result.ExitCode != 0 {
			return provider.Box{}, railwayError("update image", result, err)
		}
	}
	if err := p.submitAndWaitDeployment(ctx, service.Name); err != nil {
		return provider.Box{}, err
	}
	return p.Inspect(ctx, service.ID)
}

type railwayDeployment struct {
	ID        string    `json:"id"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"createdAt"`
}

func (p *Provider) deployments(ctx context.Context, service string) ([]railwayDeployment, error) {
	result, err := p.run(ctx, "deployment", "list", "--service", service, "--limit", "100", "--json")
	if err != nil || result.ExitCode != 0 {
		return nil, railwayError("list deployments", result, err)
	}
	var direct []railwayDeployment
	if err := json.Unmarshal(result.Stdout, &direct); err == nil {
		return direct, nil
	}
	var wrapped struct {
		Deployments []railwayDeployment `json:"deployments"`
	}
	if err := json.Unmarshal(result.Stdout, &wrapped); err != nil {
		return nil, fmt.Errorf("decode Railway deployments: %w", err)
	}
	return wrapped.Deployments, nil
}

func deploymentID(data []byte) string {
	var value any
	if json.Unmarshal(data, &value) != nil {
		return ""
	}
	var visit func(any) string
	visit = func(current any) string {
		switch typed := current.(type) {
		case map[string]any:
			for _, key := range []string{"deploymentId", "deployment_id"} {
				if text, ok := typed[key].(string); ok && text != "" {
					return text
				}
			}
			if deployment, ok := typed["deployment"]; ok {
				if found := visit(deployment); found != "" {
					return found
				}
			}
			if text, ok := typed["id"].(string); ok && text != "" {
				return text
			}
			for key, child := range typed {
				if key == "service" || key == "project" || key == "environment" {
					continue
				}
				if found := visit(child); found != "" {
					return found
				}
			}
		case []any:
			for _, child := range typed {
				if found := visit(child); found != "" {
					return found
				}
			}
		}
		return ""
	}
	return visit(value)
}

func (p *Provider) submitAndWaitDeployment(ctx context.Context, service string) error {
	before, err := p.deployments(ctx, service)
	if err != nil {
		return err
	}
	known := make(map[string]bool, len(before))
	for _, item := range before {
		known[item.ID] = true
	}
	result, submitErr := p.run(ctx, "redeploy", "--service", service, "--yes", "--json")
	id := deploymentID(result.Stdout)
	if id == "" {
		after, reconcileErr := p.deployments(ctx, service)
		if reconcileErr == nil {
			var candidates []string
			for _, item := range after {
				if item.ID != "" && !known[item.ID] {
					candidates = append(candidates, item.ID)
				}
			}
			if len(candidates) == 1 {
				id = candidates[0]
			} else if len(candidates) > 1 {
				return fmt.Errorf("Railway deployment submission was interrupted and reconciled to multiple new deployments; refusing to guess")
			}
		}
	}
	if id == "" {
		if submitErr != nil || result.ExitCode != 0 {
			return railwayError("submit deployment", result, submitErr)
		}
		return fmt.Errorf("Railway deployment submission returned no deployment ID and could not be reconciled")
	}
	deadline := time.NewTimer(p.cfg.ReadyTimeout)
	defer deadline.Stop()
	for {
		items, listErr := p.deployments(ctx, service)
		if listErr != nil {
			return listErr
		}
		for _, item := range items {
			if item.ID != id {
				continue
			}
			switch strings.ToUpper(item.Status) {
			case "SUCCESS", "READY":
				return nil
			case "FAILED", "CRASHED", "CANCELLED", "REMOVED", "SKIPPED":
				return fmt.Errorf("Railway deployment %s ended with %s", id, item.Status)
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("Railway deployment %s did not reach terminal readiness", id)
		case <-time.After(p.cfg.PollInterval):
		}
	}
}

func (p *Provider) Connection(ctx context.Context, id string) (provider.Connection, error) {
	service, err := p.resolve(ctx, id)
	if err != nil {
		return provider.Connection{}, err
	}
	return provider.Connection{Transport: "railway-ssh", Endpoint: service.Name}, nil
}
func (p *Provider) Logs(ctx context.Context, id string, opts provider.LogOptions, dst io.Writer) error {
	service, err := p.resolve(ctx, id)
	if err != nil {
		return err
	}
	args := []string{"logs", "--service", service.Name}
	if opts.Tail > 0 {
		args = append(args, "--lines", strconv.Itoa(opts.Tail))
	}
	if opts.Follow {
		args = append(args, "--follow")
	}
	result, err := p.runner.Run(ctx, p.command(args...), nil, dst, dst)
	if err != nil || result.ExitCode != 0 {
		return railwayError("logs", result, err)
	}
	return nil
}
func (p *Provider) Usage(ctx context.Context, id string) (provider.Usage, error) {
	service, err := p.resolve(ctx, id)
	if err != nil {
		return provider.Usage{}, err
	}
	result, err := p.run(ctx, "usage", "--service", service.Name, "--json")
	if err != nil || result.ExitCode != 0 {
		return provider.Usage{}, railwayError("usage", result, err)
	}
	return provider.Usage{ObservedAt: time.Now().UTC(), Cost: provider.Cost{Available: true, Currency: "USD", Detail: strings.TrimSpace(string(result.Stdout))}}, nil
}

func (p *Provider) Exec(ctx context.Context, id string, argv []string, opts provider.ExecOptions) (provider.ExecResult, error) {
	if len(argv) == 0 {
		return provider.ExecResult{}, fmt.Errorf("command argv cannot be empty")
	}
	service, err := p.resolve(ctx, id)
	if err != nil {
		return provider.ExecResult{}, err
	}
	encoded, _ := json.Marshal(argv)
	remote := []string{"vmbox-runtime", "exec-json", base64.RawURLEncoding.EncodeToString(encoded)}
	if opts.Detach {
		remote = append([]string{"vmbox-runtime", "run", "--detach", "--"}, argv...)
	}
	started := time.Now().UTC()
	sshArgs := append([]string{"railway", "ssh"}, p.target()...)
	sshArgs = append(sshArgs, "--service", service.Name)
	sshArgs = append(sshArgs, remote...)
	result, err := p.runner.Run(ctx, sshArgs, opts.Stdin, opts.Stdout, opts.Stderr)
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

func railwayError(action string, result procexec.Result, err error) error {
	if err != nil {
		return fmt.Errorf("Railway %s: %w", action, err)
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("Railway %s (exit %d): %s", action, result.ExitCode, strings.TrimSpace(string(result.Stderr)))
	}
	return nil
}
