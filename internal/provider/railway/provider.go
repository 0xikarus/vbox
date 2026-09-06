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
	"sync"
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
	SSHIdentityFile   string
	SSHBinary         string
	SSHControlDir     string
	PollInterval      time.Duration
	ReadyTimeout      time.Duration
}

type Provider struct {
	cfg                 Config
	runner              procexec.Runner
	cacheMu             sync.RWMutex
	servicesByKey       map[string]service
	deploymentByService map[string]string
	masterByTarget      map[string]bool
	sshMu               sync.Mutex
}

func New(cfg Config, runner procexec.Runner) *Provider {
	if runner == nil {
		runner = procexec.OSRunner{}
	}
	if cfg.TokenEnvironment == "" {
		cfg.TokenEnvironment = "RAILWAY_API_TOKEN"
	}
	if cfg.SSHBinary == "" {
		cfg.SSHBinary = "ssh"
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
	return &Provider{
		cfg: cfg, runner: runner,
		servicesByKey:       make(map[string]service),
		deploymentByService: make(map[string]string),
		masterByTarget:      make(map[string]bool),
	}
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
	Replicas  *struct {
		Configured int `json:"configured"`
		Running    int `json:"running"`
		Crashed    int `json:"crashed"`
		Exited     int `json:"exited"`
		Total      int `json:"total"`
	} `json:"replicas,omitempty"`
	Regions []struct {
		Name string `json:"name"`
	} `json:"regions"`
	Source struct {
		Image string `json:"image"`
	} `json:"source"`
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
	p.cacheMu.Lock()
	for _, item := range services {
		p.servicesByKey[item.ID] = item
		p.servicesByKey[item.Name] = item
		p.servicesByKey[strings.TrimPrefix(item.Name, "vmbox-")] = item
	}
	p.cacheMu.Unlock()
	return services, nil
}

func serviceName(name string) string { return "vmbox-" + name }
func railwayStartCommand(detached bool) string {
	if detached {
		return "/usr/local/bin/vmbox-entrypoint vmbox-runtime idle"
	}
	return "sleep infinity"
}

func (p *Provider) resolve(ctx context.Context, id string) (service, error) {
	p.cacheMu.RLock()
	cached, ok := p.servicesByKey[id]
	if !ok {
		cached, ok = p.servicesByKey[serviceName(id)]
	}
	p.cacheMu.RUnlock()
	if ok {
		return cached, nil
	}
	return p.resolveFresh(ctx, id)
}

// resolveFresh is used when deployment state matters. Cached resolution is
// reserved for repeated commands that only need the stable service identity.
func (p *Provider) resolveFresh(ctx context.Context, id string) (service, error) {
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
	existing, resolveErr := p.resolveFresh(ctx, req.Name)
	if resolveErr == nil {
		box, inspectErr := p.inspectService(ctx, existing, true)
		if inspectErr != nil {
			return provider.Box{}, inspectErr
		}
		if box.Owner.BoxID == "" && box.Owner.AccountID == req.Owner.AccountID {
			// A prior creation may have been interrupted after writing the account
			// marker but before the remaining metadata and source. Reconcile that
			// exact vmbox-prefixed service instead of trying to start it incomplete.
		} else {
			if err := verifyRailwayOwner(box, req.Owner); err != nil {
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
	if req.Detached {
		metadata["VMBOX_COMPUTE_SLOT"] = "true"
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
	if !req.Detached {
		if _, err := p.CreateStorage(ctx, service.ID, req.Resources); err != nil {
			return provider.Box{}, err
		}
	}
	image := req.Image
	if image == "" {
		image = p.cfg.DefaultImage
	}
	if err := p.connectImage(ctx, service.ID, image); err != nil {
		return provider.Box{}, err
	}
	if err := p.setResources(ctx, service.ID, req.Resources); err != nil {
		return provider.Box{}, err
	}
	if err := p.setRegion(ctx, service.ID, req.Region); err != nil {
		return provider.Box{}, err
	}
	startCommand := railwayStartCommand(req.Detached)
	if err := p.setStartCommand(ctx, service.ID, startCommand); err != nil {
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
		started := time.Now().UTC()
		result, err := p.directSSH(ctx, service, argv, false, stdin, nil, nil)
		if err != nil {
			return provider.ExecResult{}, err
		}
		return provider.ExecResult{ExitCode: result.ExitCode, Stdout: string(result.Stdout), Stderr: string(result.Stderr), StartedAt: started, FinishedAt: time.Now().UTC()}, nil
	}
	return providerbootstrap.Install(ctx, request, exec)
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

// Railway keeps the last successful deployment status after its only replica
// has exited. Service.Status alone therefore says SUCCESS for a container that
// direct SSH cannot reach. Prefer live replica counts when the CLI supplies
// them, while retaining the status-only fallback for older CLI responses.
func serviceState(value service) provider.State {
	result := state(value.Status)
	if result != provider.StateRunning || value.Replicas == nil {
		return result
	}
	if value.Replicas.Running > 0 {
		return provider.StateRunning
	}
	if value.Replicas.Crashed > 0 {
		return provider.StateFailed
	}
	return provider.StateStopped
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

func (p *Provider) inspectService(ctx context.Context, service service, includeResources bool) (provider.Box, error) {
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
	image := strings.TrimSpace(service.Source.Image)
	if image == "" {
		image = values["VMBOX_IMAGE"]
	}
	box := provider.Box{ID: service.ID, Name: strings.TrimPrefix(service.Name, "vmbox-"), Provider: p.Name(), State: serviceState(service), ProviderState: service.Status, Region: region, Image: image, Resources: provider.Resources{CPU: cpu, MemoryMiB: memory, DiskGiB: disk}, Owner: provider.Owner{AccountID: values["VMBOX_ACCOUNT_ID"], BoxID: values["VMBOX_BOX_ID"], RunID: values["VMBOX_RUN_ID"], Lease: values["VMBOX_LEASE"]}, CreatedAt: service.CreatedAt, UpdatedAt: service.UpdatedAt, Connection: provider.Connection{Transport: "railway-ssh", Endpoint: service.Name}, Storage: &provider.Storage{Name: service.Name + "-data", MountPath: "/data", SizeGiB: disk}}
	if includeResources {
		if actual, resourceErr := p.resources(ctx, service.ID); resourceErr == nil {
			if actual.CPU > 0 {
				box.Resources.CPU = actual.CPU
			}
			if actual.MemoryMiB > 0 {
				box.Resources.MemoryMiB = actual.MemoryMiB
			}
		}
	}
	return box, nil
}

func (p *Provider) Inspect(ctx context.Context, id string) (provider.Box, error) {
	service, err := p.resolveFresh(ctx, id)
	if err != nil {
		return provider.Box{}, err
	}
	return p.inspectService(ctx, service, true)
}
func (p *Provider) List(ctx context.Context) ([]provider.Box, error) {
	services, err := p.services(ctx)
	if err != nil {
		return nil, err
	}
	type inspected struct {
		box provider.Box
		err error
	}
	var candidates []service
	for _, item := range services {
		if !strings.HasPrefix(item.Name, "vmbox-") {
			continue
		}
		candidates = append(candidates, item)
	}
	results := make(chan inspected, len(candidates))
	for _, item := range candidates {
		go func(item service) {
			box, inspectErr := p.inspectService(ctx, item, false)
			results <- inspected{box: box, err: inspectErr}
		}(item)
	}
	boxes := make([]provider.Box, 0, len(candidates))
	for range candidates {
		result := <-results
		if result.err == nil && result.box.Owner.BoxID != "" {
			boxes = append(boxes, result.box)
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
	p.invalidateServiceSSH(service)
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

// Railway may seal service variables so even an account owner receives null
// values when reading them back. Fleet services retain an independent ownership
// proof in their controller-derived name: the normalized account prefix plus a
// bounded ordinal. The controller also supplies the immutable service ID from
// its slot record. This fallback is deliberately limited to compute slots with
// completely unreadable ownership metadata; partial or conflicting markers
// remain a hard failure.
func verifyRailwayOwner(box provider.Box, requested provider.Owner) error {
	if err := provider.VerifyOwner(box.Owner, requested); err == nil {
		return nil
	}
	if box.Owner.AccountID != "" || box.Owner.BoxID != "" || box.Owner.RunID != "" || box.Owner.Lease != "" ||
		requested.AccountID == "" || !strings.HasPrefix(requested.BoxID, "compute-slot:") || requested.RunID != "" || requested.Lease != "" {
		return fmt.Errorf("ownership mismatch: refusing destructive operation")
	}
	prefix := strings.ReplaceAll(requested.AccountID, "-", "")
	if len(prefix) > 10 {
		prefix = prefix[:10]
	}
	if prefix == "" {
		prefix = "default"
	}
	namePrefix := "slot-" + prefix + "-"
	ordinal := strings.TrimPrefix(box.Name, namePrefix)
	value, err := strconv.Atoi(ordinal)
	if !strings.HasPrefix(box.Name, namePrefix) || len(ordinal) != 2 || err != nil || value < 1 || value > 32 {
		return fmt.Errorf("ownership mismatch: refusing destructive operation")
	}
	return nil
}

func (p *Provider) Delete(ctx context.Context, id string, requested provider.Owner) error {
	box, err := p.Inspect(ctx, id)
	if errors.Is(err, provider.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := verifyRailwayOwner(box, requested); err != nil {
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
	Name              string `json:"name"`
	ID                string `json:"id"`
	ServiceName       string `json:"serviceName"`
	MountPath         string `json:"mountPath"`
	Status            string `json:"status"`
	IsPendingDeletion bool   `json:"isPendingDeletion"`
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

func (p *Provider) AttachedStorage(ctx context.Context, id string) (*provider.Storage, error) {
	service, err := p.resolveFresh(ctx, id)
	if err != nil {
		return nil, err
	}
	volumes, err := p.volumes(ctx)
	if err != nil {
		return nil, err
	}
	var attached *provider.Storage
	for _, volume := range volumes {
		if volume.ServiceName != service.Name || volume.MountPath != "/data" {
			continue
		}
		if attached != nil {
			return nil, fmt.Errorf("compute service %s has multiple workspace volumes attached", service.Name)
		}
		name := volume.Name
		if name == "" {
			name = service.Name + "-data"
		}
		attached = &provider.Storage{ID: volume.ID, Name: name, MountPath: "/data"}
	}
	return attached, nil
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
func (p *Provider) AttachStorage(ctx context.Context, id string, storage provider.Storage) error {
	if storage.ID == "" {
		return fmt.Errorf("Railway volume ID is required")
	}
	service, err := p.resolveFresh(ctx, id)
	if err != nil {
		return err
	}
	attached := func() (bool, error) {
		volumes, listErr := p.volumes(ctx)
		if listErr != nil {
			return false, listErr
		}
		for _, volume := range volumes {
			if volume.ID == storage.ID {
				if volume.ServiceName == service.Name && volume.MountPath == "/data" {
					return true, nil
				}
				if volume.ServiceName != "" {
					return false, fmt.Errorf("Railway volume %s is already attached to %s", storage.ID, volume.ServiceName)
				}
				continue
			}
			if volume.ServiceName == service.Name && volume.MountPath == "/data" {
				return false, fmt.Errorf("compute slot %s already has Railway volume %s attached", service.Name, volume.ID)
			}
		}
		return false, nil
	}
	if ready, err := attached(); err != nil || ready {
		return err
	}
	result, attachErr := p.runVolume(ctx, service.ID, nil, "attach", "--volume", storage.ID, "--yes", "--json")
	if attachErr != nil || result.ExitCode != 0 {
		if ready, reconcileErr := attached(); reconcileErr != nil || !ready {
			return railwayError("attach volume", result, attachErr)
		}
	}
	deadline := time.NewTimer(p.cfg.ReadyTimeout)
	defer deadline.Stop()
	for {
		ready, checkErr := attached()
		if checkErr != nil {
			return checkErr
		}
		if ready {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("Railway volume %s did not attach to %s", storage.ID, service.Name)
		case <-time.After(p.cfg.PollInterval):
		}
	}
	p.invalidateServiceSSH(service)
	return p.submitAndWaitDeployment(ctx, service.Name)
}

func (p *Provider) DetachStorage(ctx context.Context, id string, storage provider.Storage) error {
	if storage.ID == "" {
		return fmt.Errorf("Railway volume ID is required")
	}
	service, err := p.resolveFresh(ctx, id)
	if err != nil {
		return err
	}
	detached := func() (bool, error) {
		volumes, listErr := p.volumes(ctx)
		if listErr != nil {
			return false, listErr
		}
		for _, volume := range volumes {
			if volume.ID != storage.ID {
				continue
			}
			if volume.ServiceName == "" {
				return true, nil
			}
			if volume.ServiceName != service.Name {
				return false, fmt.Errorf("Railway volume %s is attached to unexpected service %s", storage.ID, volume.ServiceName)
			}
			return false, nil
		}
		return false, fmt.Errorf("Railway volume %s does not exist", storage.ID)
	}
	if ready, err := detached(); err != nil || ready {
		return err
	}
	result, detachErr := p.runVolume(ctx, service.ID, nil, "detach", "--volume", storage.ID, "--yes", "--json")
	if detachErr != nil || result.ExitCode != 0 {
		if ready, reconcileErr := detached(); reconcileErr != nil || !ready {
			return railwayError("detach volume", result, detachErr)
		}
	}
	deadline := time.NewTimer(p.cfg.ReadyTimeout)
	defer deadline.Stop()
	for {
		ready, checkErr := detached()
		if checkErr != nil {
			return checkErr
		}
		if ready {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("Railway volume %s did not detach from %s", storage.ID, service.Name)
		case <-time.After(p.cfg.PollInterval):
		}
	}
	p.invalidateServiceSSH(service)
	return p.submitAndWaitDeployment(ctx, service.Name)
}

func (p *Provider) SanitizeSlot(ctx context.Context, id string) error {
	service, err := p.resolveFresh(ctx, id)
	if err != nil {
		return err
	}
	volumes, err := p.volumes(ctx)
	if err != nil {
		return err
	}
	for _, volume := range volumes {
		if volume.ServiceName == service.Name {
			return fmt.Errorf("refusing to sanitize compute slot %s while volume %s remains attached", service.Name, volume.ID)
		}
	}
	if serviceState(service) == provider.StateStopped {
		return nil
	}
	_, err = p.Stop(ctx, service.ID)
	return err
}

// matchingVolumeName accepts the one legacy naming transition used by the
// controller. Older logical-box records called a slot volume "-data", while
// Railway materialized that same requested volume as "-volume". The immutable
// provider volume ID must still match before this check is reached; unrelated
// names remain a hard stop.
func matchingVolumeName(expected, actual string) bool {
	if expected == "" || actual == "" || expected == actual {
		return true
	}
	if !strings.HasSuffix(expected, "-data") {
		return false
	}
	railwayName := strings.TrimSuffix(expected, "-data") + "-volume"
	return actual == railwayName || strings.HasPrefix(actual, railwayName+"-") && len(actual) > len(railwayName)+1
}

func (p *Provider) DeleteStorage(ctx context.Context, storage provider.Storage, requested provider.Owner) error {
	if storage.ID == "" || requested.AccountID == "" || requested.BoxID == "" {
		return fmt.Errorf("exact volume ID and logical-box ownership are required")
	}
	volumes, err := p.volumes(ctx)
	if err != nil {
		return err
	}
	found := false
	for _, volume := range volumes {
		if volume.ID != storage.ID {
			continue
		}
		found = true
		if volume.ServiceName != "" {
			return fmt.Errorf("refusing to delete attached Railway volume %s from %s", storage.ID, volume.ServiceName)
		}
		if !matchingVolumeName(storage.Name, volume.Name) {
			return fmt.Errorf("Railway volume name mismatch: expected %s, found %s", storage.Name, volume.Name)
		}
		if volume.IsPendingDeletion {
			return nil
		}
	}
	if !found {
		return nil
	}
	if err := p.deleteVolume(ctx, storage.ID); err != nil {
		return err
	}
	deadline := time.NewTimer(p.cfg.ReadyTimeout)
	defer deadline.Stop()
	for {
		volumes, err = p.volumes(ctx)
		if err != nil {
			return err
		}
		present := false
		for _, volume := range volumes {
			present = present || (volume.ID == storage.ID && !volume.IsPendingDeletion)
		}
		if !present {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("Railway volume %s remained visible after deletion", storage.ID)
		case <-time.After(p.cfg.PollInterval):
		}
	}
}

func (p *Provider) Deploy(ctx context.Context, id, image string) (provider.Box, error) {
	service, err := p.resolve(ctx, id)
	if err != nil {
		return provider.Box{}, err
	}
	if image != "" {
		if err := p.connectImage(ctx, service.ID, image); err != nil {
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
	p.invalidateSSHForServiceKey(service)
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
	target, err := p.deploymentTarget(ctx, service)
	if err != nil {
		return provider.Connection{}, err
	}
	instance, _, _ := strings.Cut(target, "@")
	return provider.Connection{Transport: "openssh", Endpoint: target, Metadata: map[string]string{"deploymentInstanceId": instance}}, nil
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
	result, err := p.directSSH(ctx, service, remote, false, opts.Stdin, opts.Stdout, opts.Stderr)
	if err != nil {
		return provider.ExecResult{}, err
	}
	return provider.ExecResult{ExitCode: result.ExitCode, Stdout: string(result.Stdout), Stderr: string(result.Stderr), StartedAt: started, FinishedAt: time.Now().UTC()}, nil
}

func (p *Provider) ExecConnection(ctx context.Context, connection provider.Connection, argv []string, opts provider.ExecOptions) (provider.ExecResult, error) {
	if len(argv) == 0 {
		return provider.ExecResult{}, fmt.Errorf("command argv cannot be empty")
	}
	if opts.Interactive || opts.Detach {
		return provider.ExecResult{}, fmt.Errorf("controller-resolved command execution must be non-interactive and attached")
	}
	target, err := validatedConnectionTarget(connection)
	if err != nil {
		return provider.ExecResult{}, err
	}
	encoded, _ := json.Marshal(argv)
	remote := provider.AsWorkloadUser([]string{"vmbox-runtime", "exec-json", base64.RawURLEncoding.EncodeToString(encoded)})
	started := time.Now().UTC()
	result, err := p.directSSHTarget(ctx, target, remote, false, opts.Stdin, opts.Stdout, opts.Stderr)
	if err != nil {
		return provider.ExecResult{}, err
	}
	return provider.ExecResult{ExitCode: result.ExitCode, Stdout: string(result.Stdout), Stderr: string(result.Stderr), StartedAt: started, FinishedAt: time.Now().UTC()}, nil
}

func (p *Provider) StreamConnection(ctx context.Context, connection provider.Connection, argv []string, opts provider.ExecOptions) (provider.ExecResult, error) {
	if len(argv) == 0 || opts.Interactive || opts.Detach {
		return provider.ExecResult{}, fmt.Errorf("invalid streaming command")
	}
	target, err := validatedConnectionTarget(connection)
	if err != nil {
		return provider.ExecResult{}, err
	}
	encoded, err := json.Marshal(argv)
	if err != nil {
		return provider.ExecResult{}, err
	}
	remote := provider.AsWorkloadUser([]string{"vmbox-runtime", "direct-json", base64.RawURLEncoding.EncodeToString(encoded)})
	result, err := p.directSSHTargetMode(ctx, target, remote, false, true, opts.Stdin, opts.Stdout, opts.Stderr)
	return provider.ExecResult{ExitCode: result.ExitCode}, err
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
	if err := p.ensureSession(ctx, service, session, command, opts.Stderr); err != nil {
		return provider.ExecResult{}, err
	}
	started := time.Now().UTC()
	remote := provider.AsWorkloadUser([]string{"tmux", "attach-session", "-t", session})
	result, err := p.directSSH(ctx, service, remote, true, opts.Stdin, opts.Stdout, opts.Stderr)
	if err != nil {
		return provider.ExecResult{}, err
	}
	return provider.ExecResult{ExitCode: result.ExitCode, StartedAt: started, FinishedAt: time.Now().UTC()}, nil
}
func (p *Provider) AttachConnection(ctx context.Context, connection provider.Connection, session string, command []string, opts provider.ExecOptions) (provider.ExecResult, error) {
	if session == "" {
		return provider.ExecResult{}, fmt.Errorf("tmux session name cannot be empty")
	}
	if len(command) == 0 {
		return provider.ExecResult{}, fmt.Errorf("tmux session command cannot be empty")
	}
	target, err := validatedConnectionTarget(connection)
	if err != nil {
		return provider.ExecResult{}, err
	}
	guide := sessionGuide{
		Box: connection.Metadata["vmboxBoxName"], Slot: connection.Metadata["vmboxComputeSlot"],
		State: connection.Metadata["vmboxAssignmentState"], Health: connection.Metadata["vmboxConnectionHealth"],
	}
	if err := p.ensureSessionTarget(ctx, target, session, command, guide, opts.Stderr); err != nil {
		return provider.ExecResult{}, err
	}
	started := time.Now().UTC()
	remote := provider.AsWorkloadUser([]string{"tmux", "attach-session", "-t", session})
	result, err := p.directSSHTarget(ctx, target, remote, true, opts.Stdin, opts.Stdout, opts.Stderr)
	if err != nil {
		return provider.ExecResult{}, err
	}
	return provider.ExecResult{ExitCode: result.ExitCode, StartedAt: started, FinishedAt: time.Now().UTC()}, nil
}

const ensureSessionScript = `set -eu
session="$1"
workload_user="$2"
box="${3:-unknown}"
slot="${4:-standalone}"
state="${5:-running}"
health="${6:-connected}"
shift 6
set_guide_environment() {
  tmux set-environment -t "$session" VMBOX_NAME "$box"
  tmux set-environment -t "$session" VMBOX_COMPUTE_SLOT "$slot"
  tmux set-environment -t "$session" VMBOX_ASSIGNMENT_STATE "$state"
  tmux set-environment -t "$session" VMBOX_CONNECTION_HEALTH "$health"
}
if tmux has-session -t "$session" 2>/dev/null; then
  if [ -f /etc/vmbox/tmux.conf ]; then tmux source-file /etc/vmbox/tmux.conf; fi
  set_guide_environment
  marker="$(tmux show-environment -t "$session" VMBOX_SESSION_USER 2>/dev/null || true)"
  if [ "$marker" = "VMBOX_SESSION_USER=$workload_user" ]; then
    printf "ready\n"
    exit 0
  fi
  panes="$(tmux list-panes -t "$session" -F "#{pane_current_command}" 2>/dev/null || true)"
  idle=1
  seen=0
  for pane in $panes; do
    seen=1
    case "$pane" in bash|sh|zsh|fish|dash) ;; *) idle=0 ;; esac
  done
  if [ "$seen" -eq 1 ] && [ "$idle" -eq 1 ]; then
    tmux kill-session -t "$session"
  else
    printf "preserved\n"
    exit 0
  fi
fi
tmux new-session -d -s "$session" -c /data/workspace -- "$@"
tmux set-environment -t "$session" VMBOX_SESSION_USER "$workload_user"
if [ -f /etc/vmbox/tmux.conf ]; then tmux source-file /etc/vmbox/tmux.conf; fi
set_guide_environment
printf "created\n"
`

type sessionGuide struct{ Box, Slot, State, Health string }

func (g sessionGuide) values() []string {
	values := []string{g.Box, g.Slot, g.State, g.Health}
	defaults := []string{"unknown", "standalone", "running", "connected"}
	for index := range values {
		if values[index] == "" {
			values[index] = defaults[index]
		}
	}
	return values
}

func (p *Provider) ensureSession(ctx context.Context, service service, session string, command []string, stderr io.Writer) error {
	target, err := p.deploymentTarget(ctx, service)
	if err != nil {
		return err
	}
	guide := sessionGuide{Box: strings.TrimPrefix(service.Name, "vmbox-"), Slot: "standalone", State: "running", Health: "connected"}
	return p.ensureSessionTarget(ctx, target, session, command, guide, stderr)
}

func (p *Provider) ensureSessionTarget(ctx context.Context, target, session string, command []string, guide sessionGuide, stderr io.Writer) error {
	encoded, _ := json.Marshal(command)
	pane := []string{"vmbox-runtime", "direct-json", base64.RawURLEncoding.EncodeToString(encoded)}
	script := []string{"sh", "-c", ensureSessionScript, "vmbox-session", session, provider.WorkloadUser}
	script = append(script, guide.values()...)
	script = append(script, pane...)
	remote := provider.AsWorkloadUser(script)
	prepared, err := p.directSSHTarget(ctx, target, remote, false, nil, nil, nil)
	if err != nil {
		return fmt.Errorf("prepare tmux session: %w", err)
	}
	if prepared.ExitCode != 0 {
		return fmt.Errorf("prepare tmux session exited with status %d", prepared.ExitCode)
	}
	if strings.TrimSpace(string(prepared.Stdout)) == "preserved" && stderr != nil {
		fmt.Fprintln(stderr, "vmbox: existing active tmux session predates the non-root migration; it will be preserved until the box is stopped")
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
