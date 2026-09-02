package railway

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/0xikarus/vmbox-service/internal/procexec"
	"github.com/0xikarus/vmbox-service/internal/provider"
	providerbootstrap "github.com/0xikarus/vmbox-service/internal/provider/bootstrap"
)

type Config struct {
	ProjectID         string
	EnvironmentID     string
	Token             string
	TokenEnvironment  string
	DefaultImage      string
	SSHKnownHostsFile string
	PollInterval      time.Duration
	ReadyTimeout      time.Duration
}

type Provider struct {
	cfg    Config
	runner procexec.Runner
}

func New(cfg Config, runner procexec.Runner) *Provider {
	if runner == nil {
		runner = procexec.OSRunner{}
	}
	if cfg.TokenEnvironment == "" {
		cfg.TokenEnvironment = "RAILWAY_API_TOKEN"
	}
	if cfg.DefaultImage == "" {
		cfg.DefaultImage = "node:22-bookworm-slim"
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 2 * time.Second
	}
	if cfg.ReadyTimeout <= 0 {
		cfg.ReadyTimeout = 15 * time.Minute
	}
	if cfg.Token != "" && (cfg.TokenEnvironment == "RAILWAY_API_TOKEN" || cfg.TokenEnvironment == "RAILWAY_TOKEN") {
		other := "RAILWAY_API_TOKEN"
		if cfg.TokenEnvironment == other {
			other = "RAILWAY_TOKEN"
		}
		switch value := runner.(type) {
		case procexec.OSRunner:
			env := make(map[string]string, len(value.Env)+1)
			for key, item := range value.Env {
				env[key] = item
			}
			env[cfg.TokenEnvironment] = cfg.Token
			value.Env = env
			value.Unset = append(value.Unset, other)
			runner = value
		case *procexec.OSRunner:
			env := make(map[string]string, len(value.Env)+1)
			for key, item := range value.Env {
				env[key] = item
			}
			env[cfg.TokenEnvironment] = cfg.Token
			unset := append([]string(nil), value.Unset...)
			unset = append(unset, other)
			runner = &procexec.OSRunner{Env: env, Unset: unset}
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
	return p.runInput(ctx, nil, args...)
}
func (p *Provider) runInput(ctx context.Context, stdin io.Reader, args ...string) (procexec.Result, error) {
	return p.runner.Run(ctx, p.command(args...), stdin, nil, nil)
}

func (p *Provider) Validate(ctx context.Context) (provider.Capabilities, error) {
	cap := provider.Capabilities{Provider: p.Name(), Architectures: []string{"linux/amd64"}, Interactive: true, Detached: true, ExactArgv: true, PersistentStorage: true, AutomatedStorage: true, Resize: true, Metrics: true, Cost: true, ControllerCompatible: true}
	if p.cfg.TokenEnvironment != "RAILWAY_API_TOKEN" && p.cfg.TokenEnvironment != "RAILWAY_TOKEN" {
		return cap, fmt.Errorf("Railway token environment must be RAILWAY_API_TOKEN or RAILWAY_TOKEN")
	}
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
	var err error
	existing, resolveErr := p.resolve(ctx, req.Name)
	if resolveErr == nil {
		box, inspectErr := p.inspectService(ctx, existing)
		if inspectErr != nil {
			return provider.Box{}, inspectErr
		}
		if box.Owner.BoxID == "" && box.Owner.AccountID == req.Owner.AccountID {
			// A prior creation may have been interrupted after writing the account
			// marker but before the remaining metadata and source. Reconcile that
			// exact vmbox-prefixed service instead of trying to start it incomplete.
		} else {
			if err := provider.VerifyOwner(box.Owner, req.Owner); err != nil {
				return provider.Box{}, err
			}
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
		result, createErr = p.createService(ctx, serviceName(req.Name))
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
	if req.Region != "" {
		metadata["VMBOX_REGION"] = req.Region
	}
	if req.Resources.CPU > 0 {
		metadata["VMBOX_CPU"] = strconv.FormatFloat(req.Resources.CPU, 'f', -1, 64)
	}
	if req.Resources.MemoryMiB > 0 {
		metadata["VMBOX_MEMORY_MIB"] = strconv.FormatInt(req.Resources.MemoryMiB, 10)
	}
	if req.Resources.DiskGiB > 0 {
		metadata["VMBOX_DISK_GIB"] = strconv.FormatInt(req.Resources.DiskGiB, 10)
	}
	if len(req.Components) > 0 {
		metadata["VMBOX_COMPONENTS"] = strings.Join(req.Components, ",")
	}
	for key, value := range req.Env {
		metadata[key] = value
	}
	keys := make([]string, 0, len(metadata))
	for key, value := range metadata {
		if value != "" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	for _, key := range keys {
		value := metadata[key]
		result, err = p.runInput(ctx, strings.NewReader(value), "variable", "set", key, "--stdin", "--service", service.Name, "--skip-deploys")
		if err == nil && result.ExitCode == 0 {
			continue
		}
		values, reconcileErr := p.variables(ctx, service.Name)
		if reconcileErr != nil || values[key] != value {
			return provider.Box{}, railwayError("set variable "+key, result, err)
		}
	}
	if _, err := p.CreateStorage(ctx, service.ID, req.Resources); err != nil {
		return provider.Box{}, err
	}
	image := req.Image
	if image == "" {
		image = p.cfg.DefaultImage
	}
	if err := p.connectImage(ctx, service.Name, image); err != nil {
		return provider.Box{}, err
	}
	if err := p.setResources(ctx, service.ID, req.Resources); err != nil {
		return provider.Box{}, err
	}
	if err := p.setRegion(ctx, service.ID, req.Region); err != nil {
		return provider.Box{}, err
	}
	if err := p.setStartCommand(ctx, service.ID, "sleep infinity"); err != nil {
		return provider.Box{}, err
	}
	if err := p.submitAndWaitDeployment(ctx, service.Name); err != nil {
		return provider.Box{}, err
	}
	return p.Inspect(ctx, service.ID)
}

func (p *Provider) Bootstrap(ctx context.Context, id string, request provider.BootstrapRequest) error {
	service, err := p.resolve(ctx, id)
	if err != nil {
		return err
	}
	exec := func(ctx context.Context, argv []string, stdin io.Reader) (provider.ExecResult, error) {
		sshArgs := append([]string{"railway", "ssh"}, p.target()...)
		sshArgs = append(sshArgs, "--service", service.Name)
		sshArgs = append(sshArgs, argv...)
		started := time.Now().UTC()
		result, err := p.runSSH(ctx, sshArgs, stdin, nil, nil)
		if err != nil {
			return provider.ExecResult{}, err
		}
		return provider.ExecResult{ExitCode: result.ExitCode, Stdout: string(result.Stdout), Stderr: string(result.Stderr), StartedAt: started, FinishedAt: time.Now().UTC()}, nil
	}
	return providerbootstrap.Install(ctx, request, exec)
}

func (p *Provider) runSSH(ctx context.Context, argv []string, stdin io.Reader, stdout, stderr io.Writer) (procexec.Result, error) {
	result, err := p.runner.Run(ctx, argv, stdin, stdout, stderr)
	if p.cfg.SSHKnownHostsFile == "" || !strings.Contains(string(result.Stderr), "REMOTE HOST IDENTIFICATION HAS CHANGED") {
		return result, err
	}
	removed, removeErr := p.runner.Run(ctx, []string{"ssh-keygen", "-f", p.cfg.SSHKnownHostsFile, "-R", "ssh.railway.com"}, nil, nil, nil)
	if removeErr != nil || removed.ExitCode != 0 {
		return result, err
	}
	return p.runner.Run(ctx, argv, stdin, stdout, stderr)
}

func state(status string) provider.State {
	switch strings.ToUpper(status) {
	case "SUCCESS":
		return provider.StateRunning
	case "FAILED", "CRASHED":
		return provider.StateFailed
	case "", "REMOVED", "NO_DEPLOYMENT":
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
	if region == "" {
		region = values["VMBOX_REGION"]
	}
	cpu, _ := strconv.ParseFloat(values["VMBOX_CPU"], 64)
	memory, _ := strconv.ParseInt(values["VMBOX_MEMORY_MIB"], 10, 64)
	disk, _ := strconv.ParseInt(values["VMBOX_DISK_GIB"], 10, 64)
	box := provider.Box{ID: service.ID, Name: strings.TrimPrefix(service.Name, "vmbox-"), Provider: p.Name(), State: state(service.Status), ProviderState: service.Status, Region: region, Image: values["VMBOX_IMAGE"], Resources: provider.Resources{CPU: cpu, MemoryMiB: memory, DiskGiB: disk}, Owner: provider.Owner{AccountID: values["VMBOX_ACCOUNT_ID"], BoxID: values["VMBOX_BOX_ID"], RunID: values["VMBOX_RUN_ID"], Lease: values["VMBOX_LEASE"]}, CreatedAt: service.CreatedAt, UpdatedAt: service.UpdatedAt, Connection: provider.Connection{Transport: "railway-ssh", Endpoint: service.Name}, Storage: &provider.Storage{Name: service.Name + "-data", MountPath: "/data", SizeGiB: disk}}
	if actual, resourceErr := p.resources(ctx, service.ID); resourceErr == nil {
		if actual.CPU > 0 {
			box.Resources.CPU = actual.CPU
		}
		if actual.MemoryMiB > 0 {
			box.Resources.MemoryMiB = actual.MemoryMiB
		}
	}
	return box, nil
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
	if err := p.setResources(ctx, service.ID, resources); err != nil {
		return provider.Box{}, err
	}
	deadline := time.NewTimer(p.cfg.ReadyTimeout)
	defer deadline.Stop()
	var actual provider.Box
	for {
		actual, err = p.Inspect(ctx, service.ID)
		if err != nil {
			return provider.Box{}, err
		}
		if resourcesMatch(actual.Resources, resources) {
			return actual, nil
		}
		select {
		case <-ctx.Done():
			return provider.Box{}, ctx.Err()
		case <-deadline.C:
			return provider.Box{}, fmt.Errorf("Railway resize for %s did not become effective: requested %+v, observed %+v", service.Name, resources, actual.Resources)
		case <-time.After(p.cfg.PollInterval):
		}
	}
}

func resourcesMatch(actual, requested provider.Resources) bool {
	return (requested.CPU <= 0 || math.Abs(actual.CPU-requested.CPU) < 0.000001) &&
		(requested.MemoryMiB <= 0 || actual.MemoryMiB == requested.MemoryMiB)
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
	for _, volumeID := range volumeIDs {
		if err := p.deleteVolume(ctx, volumeID); err != nil {
			return err
		}
	}
	result, err := p.run(ctx, "service", "delete", "--service", box.ID, "--yes", "--json")
	if err != nil || result.ExitCode != 0 {
		return railwayError("delete service", result, err)
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
	result, err := p.runVolume(ctx, "", nil, "list", "--json")
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
		result, createErr = p.runVolume(ctx, service.ID, nil, "add", "--mount-path", "/data", "--json")
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
		if err := p.connectImage(ctx, service.Name, image); err != nil {
			return provider.Box{}, err
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
	result, submitErr := p.run(ctx, "redeploy", "--service", service, "--yes", "--json", "--from-source")
	id := deploymentID(result.Stdout)
	visibilityTimeout := 30 * time.Second
	if p.cfg.ReadyTimeout < visibilityTimeout {
		visibilityTimeout = p.cfg.ReadyTimeout
	}
	visibilityDeadline := time.NewTimer(visibilityTimeout)
	defer visibilityDeadline.Stop()
	for id == "" {
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
		if id != "" {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-visibilityDeadline.C:
			if submitErr != nil || result.ExitCode != 0 {
				return railwayError("submit deployment", result, submitErr)
			}
			return fmt.Errorf("Railway accepted the deployment submission, but no deployment record became visible before timeout")
		case <-time.After(p.cfg.PollInterval):
		}
	}
	visibilityDeadline.Stop()
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
	result, err := p.runner.Run(ctx, []string{"railway", "usage", "projects", "--project", p.cfg.ProjectID, "--period", "current", "--json"}, nil, nil, nil)
	if err != nil || result.ExitCode != 0 {
		detail := "Railway billing usage is unavailable for this credential"
		if strings.Contains(strings.ToLower(string(result.Stderr)), "unauthorized") {
			detail = "Railway project tokens cannot read workspace billing; use an account/workspace token for accrued cost"
		} else if err != nil {
			detail = "Railway billing usage unavailable: " + err.Error()
		}
		return provider.Usage{ObservedAt: time.Now().UTC(), Cost: provider.Cost{Available: false, Currency: "USD", Detail: detail}}, nil
	}
	return provider.Usage{ObservedAt: time.Now().UTC(), Cost: decodeRailwayCost(result.Stdout, service.Name)}, nil
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
	remote := provider.AsWorkloadUser([]string{"vmbox-runtime", "exec-json", base64.RawURLEncoding.EncodeToString(encoded)})
	if opts.Detach {
		remote = provider.AsWorkloadUser(append([]string{"vmbox-runtime", "run", "--detach", "--"}, argv...))
	}
	started := time.Now().UTC()
	sshArgs := append([]string{"railway", "ssh"}, p.target()...)
	sshArgs = append(sshArgs, "--service", service.Name)
	sshArgs = append(sshArgs, remote...)
	result, err := p.runSSH(ctx, sshArgs, opts.Stdin, opts.Stdout, opts.Stderr)
	if err != nil {
		return provider.ExecResult{}, err
	}
	return provider.ExecResult{ExitCode: result.ExitCode, Stdout: string(result.Stdout), Stderr: string(result.Stderr), StartedAt: started, FinishedAt: time.Now().UTC()}, nil
}

func (p *Provider) AttachSession(ctx context.Context, id, session string, command []string, opts provider.ExecOptions) (provider.ExecResult, error) {
	if session == "" {
		return provider.ExecResult{}, fmt.Errorf("tmux session name cannot be empty")
	}
	if len(command) == 0 {
		return provider.ExecResult{}, fmt.Errorf("tmux session command cannot be empty")
	}
	service, err := p.resolve(ctx, id)
	if err != nil {
		return provider.ExecResult{}, err
	}
	if err := p.ensureSession(ctx, service.Name, session, command, opts.Stderr); err != nil {
		return provider.ExecResult{}, err
	}
	started := time.Now().UTC()
	sshArgs := append([]string{"railway", "ssh"}, p.target()...)
	sshArgs = append(sshArgs, "--service", service.Name, "--session", session)
	var result procexec.Result
	if attached, ok := p.runner.(procexec.AttachedRunner); ok {
		result, err = attached.RunAttached(ctx, sshArgs, opts.Stdin, opts.Stdout, opts.Stderr)
	} else {
		result, err = p.runner.Run(ctx, sshArgs, opts.Stdin, opts.Stdout, opts.Stderr)
	}
	if err != nil {
		return provider.ExecResult{}, err
	}
	return provider.ExecResult{ExitCode: result.ExitCode, StartedAt: started, FinishedAt: time.Now().UTC()}, nil
}

func (p *Provider) ensureSession(ctx context.Context, service, session string, command []string, stderr io.Writer) error {
	run := func(argv ...string) (procexec.Result, error) {
		sshArgs := append([]string{"railway", "ssh"}, p.target()...)
		sshArgs = append(sshArgs, "--service", service)
		sshArgs = append(sshArgs, argv...)
		return p.runSSH(ctx, sshArgs, nil, nil, nil)
	}
	check, err := run("tmux", "has-session", "-t", session)
	if err != nil {
		return fmt.Errorf("check tmux session: %w", err)
	}
	if check.ExitCode == 0 {
		marker, markerErr := run("tmux", "show-environment", "-t", session, "VMBOX_SESSION_USER")
		if markerErr == nil && marker.ExitCode == 0 && strings.TrimSpace(string(marker.Stdout)) == "VMBOX_SESSION_USER="+provider.WorkloadUser {
			return nil
		}
		panes, panesErr := run("tmux", "list-panes", "-t", session, "-F", "#{pane_current_command}")
		if panesErr == nil && panes.ExitCode == 0 && idleSession(string(panes.Stdout)) {
			killed, killErr := run("tmux", "kill-session", "-t", session)
			if killErr != nil || killed.ExitCode != 0 {
				return fmt.Errorf("replace legacy root tmux session")
			}
		} else {
			if stderr != nil {
				fmt.Fprintln(stderr, "vmbox: existing active tmux session predates the non-root migration; it will be preserved until the box is stopped")
			}
			return nil
		}
	}
	encoded, _ := json.Marshal(command)
	pane := provider.AsWorkloadUser([]string{"vmbox-runtime", "direct-json", base64.RawURLEncoding.EncodeToString(encoded)})
	create := []string{"tmux", "new-session", "-d", "-s", session, "-c", "/data/workspace", "--"}
	create = append(create, pane...)
	created, err := run(create...)
	if err != nil || created.ExitCode != 0 {
		return fmt.Errorf("create tmux session exited with status %d", created.ExitCode)
	}
	marked, err := run("tmux", "set-environment", "-t", session, "VMBOX_SESSION_USER", provider.WorkloadUser)
	if err != nil || marked.ExitCode != 0 {
		return fmt.Errorf("mark tmux session user")
	}
	return nil
}

func idleSession(output string) bool {
	values := strings.Fields(strings.ToLower(output))
	if len(values) == 0 {
		return false
	}
	for _, value := range values {
		switch value {
		case "bash", "sh", "zsh", "fish", "dash":
		default:
			return false
		}
	}
	return true
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
