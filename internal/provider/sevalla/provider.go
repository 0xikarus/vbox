package sevalla

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/0xikarus/vmbox-service/internal/provider"
	providerbootstrap "github.com/0xikarus/vmbox-service/internal/provider/bootstrap"
	"github.com/coder/websocket"
)

const defaultAPIURL = "https://api.sevalla.com/v3"

type Config struct {
	Token                      string
	APIURL                     string
	CompanyID                  string
	ProjectID                  string
	ClusterID                  string
	ResourceTypeID             string
	DefaultImage               string
	DockerRegistryCredentialID string
	PreAttachedDisk            string
	HTTPClient                 *http.Client
}

type Provider struct {
	cfg    Config
	client *http.Client
}

func New(cfg Config) *Provider {
	if cfg.APIURL == "" {
		cfg.APIURL = defaultAPIURL
	}
	if cfg.DefaultImage == "" {
		cfg.DefaultImage = "node:22-bookworm-slim"
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 75 * time.Second}
	}
	return &Provider{cfg: cfg, client: client}
}

func (p *Provider) Name() string { return "sevalla" }

type apiError struct {
	Status  int
	Message string
}

func (e *apiError) Error() string { return fmt.Sprintf("Sevalla API %d: %s", e.Status, e.Message) }

func (p *Provider) request(ctx context.Context, method, path string, input, output any) error {
	if p.cfg.Token == "" {
		return fmt.Errorf("SEVALLA_API_TOKEN is required")
	}
	var body []byte
	var err error
	if input != nil {
		body, err = json.Marshal(input)
		if err != nil {
			return err
		}
	}
	for attempt := 0; attempt < 5; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(p.cfg.APIURL, "/")+path, bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+p.cfg.Token)
		req.Header.Set("Accept", "application/json")
		if input != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := p.client.Do(req)
		if err != nil {
			return err
		}
		data, readErr := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		_ = resp.Body.Close()
		if readErr != nil {
			return readErr
		}
		if resp.StatusCode == http.StatusTooManyRequests && attempt < 4 {
			delay := retryDelay(resp.Header, attempt)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delay):
			}
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			var message struct {
				Message string `json:"message"`
				Data    struct {
					Message string `json:"message"`
				} `json:"data"`
			}
			_ = json.Unmarshal(data, &message)
			if message.Message == "" {
				message.Message = strings.TrimSpace(string(data))
			}
			if message.Data.Message != "" {
				message.Message += ": " + message.Data.Message
			}
			return &apiError{Status: resp.StatusCode, Message: message.Message}
		}
		if output == nil || len(bytes.TrimSpace(data)) == 0 {
			return nil
		}
		if err := json.Unmarshal(data, output); err != nil {
			return fmt.Errorf("decode Sevalla response: %w", err)
		}
		return nil
	}
	return fmt.Errorf("Sevalla API retry limit reached")
}

func retryDelay(header http.Header, attempt int) time.Duration {
	if value := header.Get("Retry-After"); value != "" {
		if seconds, err := strconv.Atoi(value); err == nil && seconds >= 0 && seconds <= 300 {
			return time.Duration(seconds) * time.Second
		}
	}
	if value := header.Get("RateLimit-Reset"); value != "" {
		if epoch, err := strconv.ParseInt(value, 10, 64); err == nil {
			d := time.Until(time.Unix(epoch, 0))
			if d > 0 && d <= 5*time.Minute {
				return d
			}
		}
	}
	return time.Duration(1<<attempt) * time.Second
}

type application struct {
	ID          string    `json:"id"`
	ProjectID   string    `json:"project_id"`
	Name        string    `json:"name"`
	DisplayName string    `json:"display_name"`
	Status      string    `json:"status"`
	Suspended   bool      `json:"is_suspended"`
	Image       string    `json:"docker_image"`
	ClusterID   string    `json:"cluster_id"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type process struct {
	ID             string `json:"id"`
	AppID          string `json:"app_id"`
	Key            string `json:"key"`
	Type           string `json:"type"`
	ResourceTypeID string `json:"resource_type_id"`
	CPU            int64  `json:"cpu_limit"`
	Memory         int64  `json:"memory_limit"`
}

type page[T any] struct {
	Data                 []T `json:"data"`
	Total, Offset, Limit int
}

type listResponse[T any] struct{ Data []T }

func (r *listResponse[T]) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) > 0 && trimmed[0] == '[' {
		return json.Unmarshal(trimmed, &r.Data)
	}
	var wrapped page[T]
	if err := json.Unmarshal(trimmed, &wrapped); err != nil {
		return err
	}
	r.Data = wrapped.Data
	return nil
}

func (p *Provider) Validate(ctx context.Context) (provider.Capabilities, error) {
	cap := provider.Capabilities{Provider: p.Name(), Architectures: []string{"linux/amd64"}, Interactive: true, Detached: true, ExactArgv: true, PersistentStorage: p.cfg.PreAttachedDisk != "", AutomatedStorage: false, Resize: true, Metrics: true, Cost: true, ControllerCompatible: true, Warnings: []string{"Sevalla has no documented application-disk mutation API; /data must be manually pre-attached and is capability-gated"}}
	var clusters listResponse[json.RawMessage]
	if err := p.request(ctx, http.MethodGet, "/resources/clusters?limit=1", nil, &clusters); err != nil {
		return cap, err
	}
	return cap, nil
}

func (p *Provider) Clusters(ctx context.Context) ([]map[string]any, error) {
	var result listResponse[map[string]any]
	err := p.request(ctx, http.MethodGet, "/resources/clusters?limit=100", nil, &result)
	return result.Data, err
}

func (p *Provider) ResourceTypes(ctx context.Context) ([]map[string]any, error) {
	var result listResponse[map[string]any]
	err := p.request(ctx, http.MethodGet, "/resources/process-resource-types?limit=100", nil, &result)
	return result.Data, err
}

type deployment struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

func (p *Provider) deploy(ctx context.Context, appID, image string) error {
	payload := map[string]any{}
	if image != "" {
		payload["docker_image"] = image
	}
	var current deployment
	if err := p.request(ctx, http.MethodPost, "/applications/"+url.PathEscape(appID)+"/deployments", payload, &current); err != nil {
		return err
	}
	if current.ID == "" {
		return fmt.Errorf("Sevalla returned no deployment ID")
	}
	wait, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	for {
		switch strings.ToLower(current.Status) {
		case "success":
			return nil
		case "failed", "cancelled", "skipped":
			return fmt.Errorf("Sevalla deployment %s ended with %s", current.ID, current.Status)
		}
		select {
		case <-wait.Done():
			return fmt.Errorf("wait for Sevalla deployment %s: %w", current.ID, wait.Err())
		case <-time.After(2 * time.Second):
		}
		if err := p.request(wait, http.MethodGet, "/applications/"+url.PathEscape(appID)+"/deployments/"+url.PathEscape(current.ID), nil, &current); err != nil {
			return err
		}
	}
}

func (p *Provider) Create(ctx context.Context, req provider.CreateRequest) (provider.Box, error) {
	if err := provider.ValidateName(req.Name); err != nil {
		return provider.Box{}, err
	}
	if req.Image == "" {
		req.Image = p.cfg.DefaultImage
	}
	if req.Owner.BoxID == "" {
		req.Owner.BoxID = req.Name
	}
	cluster := req.Region
	if cluster == "" {
		cluster = p.cfg.ClusterID
	}
	if cluster == "" {
		return provider.Box{}, fmt.Errorf("Sevalla cluster ID is required; discover it with provider validate")
	}
	if existing, err := p.resolve(ctx, req.Name); err == nil {
		actual, ownerErr := p.owner(ctx, existing.ID)
		if ownerErr != nil {
			return provider.Box{}, fmt.Errorf("verify existing Sevalla application ownership: %w", ownerErr)
		}
		if ownerErr := provider.VerifyOwner(actual, req.Owner); ownerErr != nil {
			return provider.Box{}, ownerErr
		}
		return p.Inspect(ctx, existing.ID)
	} else if !errors.Is(err, provider.ErrNotFound) {
		return provider.Box{}, err
	}
	payload := map[string]any{"display_name": "vmbox-" + req.Name, "cluster_id": cluster, "source": "dockerImage", "docker_image": req.Image}
	if p.cfg.ProjectID != "" {
		payload["project_id"] = p.cfg.ProjectID
	}
	var app application
	if p.cfg.DockerRegistryCredentialID != "" {
		payload["docker_registry_credential_id"] = p.cfg.DockerRegistryCredentialID
	}
	if err := p.request(ctx, http.MethodPost, "/applications", payload, &app); err != nil {
		return provider.Box{}, err
	}
	// Ownership metadata is stored as ordinary application variables; credentials
	// remain in the controller/laptop and are never included.
	metadata := map[string]string{"VMBOX_MANAGED": "true", "VMBOX_ACCOUNT_ID": req.Owner.AccountID, "VMBOX_BOX_ID": req.Owner.BoxID, "VMBOX_RUN_ID": req.Owner.RunID, "VMBOX_LEASE": req.Owner.Lease}
	for key, value := range req.Env {
		metadata[key] = value
	}
	for key, value := range metadata {
		if value == "" {
			continue
		}
		input := map[string]any{"key": key, "value": value, "is_runtime": true, "is_buildtime": false}
		if err := p.request(ctx, http.MethodPost, "/applications/"+url.PathEscape(app.ID)+"/env-vars", input, nil); err != nil {
			return provider.Box{}, fmt.Errorf("set ownership metadata: %w", err)
		}
	}
	processes, err := p.processes(ctx, app.ID)
	if err != nil {
		return provider.Box{}, err
	}
	if len(processes) > 0 {
		input := map[string]any{"entrypoint": "sleep infinity", "scaling_strategy": map[string]any{"type": "manual", "config": map[string]any{"instanceCount": 1}}}
		if p.cfg.ResourceTypeID != "" {
			input["resource_type_id"] = p.cfg.ResourceTypeID
		}
		if err := p.request(ctx, http.MethodPatch, "/applications/"+url.PathEscape(app.ID)+"/processes/"+url.PathEscape(processes[0].ID), input, nil); err != nil {
			return provider.Box{}, err
		}
	}
	if err := p.deploy(ctx, app.ID, req.Image); err != nil {
		return provider.Box{}, fmt.Errorf("initial deploy: %w", err)
	}
	return p.Inspect(ctx, app.ID)
}

func (p *Provider) Bootstrap(ctx context.Context, id string, request provider.BootstrapRequest) error {
	exec := func(ctx context.Context, argv []string, stdin io.Reader) (provider.ExecResult, error) {
		return p.Exec(ctx, id, argv, provider.ExecOptions{Stdin: stdin})
	}
	return providerbootstrap.Install(ctx, request, exec)
}

func (p *Provider) resolve(ctx context.Context, id string) (application, error) {
	if strings.Count(id, "-") == 4 {
		var app application
		err := p.request(ctx, http.MethodGet, "/applications/"+url.PathEscape(id), nil, &app)
		var apiErr *apiError
		if errors.As(err, &apiErr) && apiErr.Status == http.StatusNotFound {
			return app, provider.ErrNotFound
		}
		return app, err
	}
	var apps page[application]
	if err := p.request(ctx, http.MethodGet, "/applications?limit=100", nil, &apps); err != nil {
		return application{}, err
	}
	for _, app := range apps.Data {
		if app.Name == id || app.DisplayName == id || app.DisplayName == "vmbox-"+id {
			return app, nil
		}
	}
	return application{}, provider.ErrNotFound
}

func (p *Provider) processes(ctx context.Context, appID string) ([]process, error) {
	var result page[process]
	err := p.request(ctx, http.MethodGet, "/applications/"+url.PathEscape(appID)+"/processes?limit=100", nil, &result)
	return result.Data, err
}

func (p *Provider) toBox(app application, owner provider.Owner) provider.Box {
	state := provider.StateProvisioning
	if app.Suspended {
		state = provider.StateStopped
	} else if app.Status == "deploymentSuccess" {
		state = provider.StateRunning
	} else if strings.Contains(strings.ToLower(app.Status), "fail") {
		state = provider.StateFailed
	}
	box := provider.Box{ID: app.ID, Name: strings.TrimPrefix(app.DisplayName, "vmbox-"), Provider: p.Name(), State: state, ProviderState: app.Status, Image: app.Image, Region: app.ClusterID, Owner: owner, CreatedAt: app.CreatedAt, UpdatedAt: app.UpdatedAt, Connection: provider.Connection{Transport: "sevalla-websocket"}}
	if p.cfg.PreAttachedDisk != "" {
		box.Storage = &provider.Storage{ID: p.cfg.PreAttachedDisk, Name: p.cfg.PreAttachedDisk, MountPath: "/data", Manual: true}
	}
	return box
}

func (p *Provider) owner(ctx context.Context, appID string) (provider.Owner, error) {
	var vars page[struct{ Key, Value string }]
	if err := p.request(ctx, http.MethodGet, "/applications/"+url.PathEscape(appID)+"/env-vars?limit=100", nil, &vars); err != nil {
		return provider.Owner{}, err
	}
	values := make(map[string]string)
	for _, item := range vars.Data {
		values[item.Key] = item.Value
	}
	return provider.Owner{AccountID: values["VMBOX_ACCOUNT_ID"], BoxID: values["VMBOX_BOX_ID"], RunID: values["VMBOX_RUN_ID"], Lease: values["VMBOX_LEASE"]}, nil
}

func (p *Provider) Inspect(ctx context.Context, id string) (provider.Box, error) {
	app, err := p.resolve(ctx, id)
	if err != nil {
		return provider.Box{}, err
	}
	owner, err := p.owner(ctx, app.ID)
	if err != nil {
		return provider.Box{}, err
	}
	box := p.toBox(app, owner)
	processes, _ := p.processes(ctx, app.ID)
	if len(processes) > 0 {
		box.Resources = provider.Resources{CPU: float64(processes[0].CPU) / 1000, MemoryMiB: processes[0].Memory}
	}
	return box, nil
}

func (p *Provider) List(ctx context.Context) ([]provider.Box, error) {
	var apps page[application]
	if err := p.request(ctx, http.MethodGet, "/applications?limit=100", nil, &apps); err != nil {
		return nil, err
	}
	boxes := make([]provider.Box, 0)
	for _, app := range apps.Data {
		if !strings.HasPrefix(app.DisplayName, "vmbox-") {
			continue
		}
		owner, err := p.owner(ctx, app.ID)
		if err != nil || owner.BoxID == "" {
			continue
		}
		boxes = append(boxes, p.toBox(app, owner))
	}
	return boxes, nil
}

func (p *Provider) Start(ctx context.Context, id string) (provider.Box, error) {
	app, err := p.resolve(ctx, id)
	if err != nil {
		return provider.Box{}, err
	}
	if err := p.request(ctx, http.MethodPost, "/applications/"+url.PathEscape(app.ID)+"/activate", nil, nil); err != nil {
		return provider.Box{}, err
	}
	return p.waitApplicationState(ctx, app.ID, false)
}

func (p *Provider) Stop(ctx context.Context, id string) (provider.Box, error) {
	app, err := p.resolve(ctx, id)
	if err != nil {
		return provider.Box{}, err
	}
	if err := p.request(ctx, http.MethodPost, "/applications/"+url.PathEscape(app.ID)+"/suspend", nil, nil); err != nil {
		return provider.Box{}, err
	}
	return p.waitApplicationState(ctx, app.ID, true)
}

func (p *Provider) waitApplicationState(ctx context.Context, appID string, suspended bool) (provider.Box, error) {
	deadline := time.NewTimer(10 * time.Minute)
	defer deadline.Stop()
	for {
		var app application
		if err := p.request(ctx, http.MethodGet, "/applications/"+url.PathEscape(appID), nil, &app); err != nil {
			return provider.Box{}, err
		}
		ready := app.Suspended == suspended
		if !suspended {
			ready = ready && app.Status == "deploymentSuccess"
		}
		if ready {
			return p.Inspect(ctx, appID)
		}
		select {
		case <-ctx.Done():
			return provider.Box{}, ctx.Err()
		case <-deadline.C:
			return provider.Box{}, fmt.Errorf("Sevalla application %s did not reach suspended=%t", appID, suspended)
		case <-time.After(2 * time.Second):
		}
	}
}

func (p *Provider) Resize(ctx context.Context, id string, resources provider.Resources) (provider.Box, error) {
	app, err := p.resolve(ctx, id)
	if err != nil {
		return provider.Box{}, err
	}
	processes, err := p.processes(ctx, app.ID)
	if err != nil || len(processes) == 0 {
		return provider.Box{}, fmt.Errorf("Sevalla process unavailable: %w", err)
	}
	resourceID := p.cfg.ResourceTypeID
	if resourceID == "" {
		return provider.Box{}, fmt.Errorf("Sevalla resize requires a discovered process resource type ID")
	}
	if err := p.request(ctx, http.MethodPatch, "/applications/"+url.PathEscape(app.ID)+"/processes/"+url.PathEscape(processes[0].ID), map[string]any{"resource_type_id": resourceID}, nil); err != nil {
		return provider.Box{}, err
	}
	if err := p.deploy(ctx, app.ID, app.Image); err != nil {
		return provider.Box{}, err
	}
	return p.Inspect(ctx, app.ID)
}

func (p *Provider) Delete(ctx context.Context, id string, requested provider.Owner) error {
	app, err := p.resolve(ctx, id)
	if errors.Is(err, provider.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	actual, err := p.owner(ctx, app.ID)
	if err != nil {
		return err
	}
	if err := provider.VerifyOwner(actual, requested); err != nil {
		return err
	}
	if err := p.request(ctx, http.MethodDelete, "/applications/"+url.PathEscape(app.ID), nil, nil); err != nil {
		return err
	}
	for attempt := 0; attempt < 30; attempt++ {
		_, err := p.resolve(ctx, app.ID)
		if errors.Is(err, provider.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	return fmt.Errorf("Sevalla application %s is still visible after deletion", app.ID)
}

func (p *Provider) CreateStorage(context.Context, string, provider.Resources) (provider.Storage, error) {
	return provider.Storage{}, fmt.Errorf("Sevalla public v3 API has no application-disk create endpoint; manually attach /data and configure its ID: %w", provider.ErrUnsupported)
}
func (p *Provider) AttachStorage(context.Context, string, provider.Storage) error {
	return fmt.Errorf("Sevalla disk attachment is manual: %w", provider.ErrUnsupported)
}
func (p *Provider) DeleteStorage(context.Context, provider.Storage, provider.Owner) error {
	return fmt.Errorf("Sevalla disk deletion follows application deletion and cannot be called independently: %w", provider.ErrUnsupported)
}

func (p *Provider) Deploy(ctx context.Context, id, image string) (provider.Box, error) {
	app, err := p.resolve(ctx, id)
	if err != nil {
		return provider.Box{}, err
	}
	if image != "" && image != app.Image {
		if err := p.request(ctx, http.MethodPatch, "/applications/"+url.PathEscape(app.ID), map[string]any{"docker_image": image}, nil); err != nil {
			return provider.Box{}, err
		}
	}
	if image == "" {
		image = app.Image
	}
	if err := p.deploy(ctx, app.ID, image); err != nil {
		return provider.Box{}, err
	}
	return p.Inspect(ctx, app.ID)
}

func (p *Provider) Connection(ctx context.Context, id string) (provider.Connection, error) {
	app, err := p.resolve(ctx, id)
	if err != nil {
		return provider.Connection{}, err
	}
	processes, err := p.processes(ctx, app.ID)
	if err != nil || len(processes) == 0 {
		return provider.Connection{}, fmt.Errorf("Sevalla process unavailable: %w", err)
	}
	base := strings.TrimRight(p.cfg.APIURL, "/")
	base = strings.Replace(base, "https://", "wss://", 1)
	return provider.Connection{Transport: "sevalla-websocket", Endpoint: fmt.Sprintf("%s/applications/%s/processes/%s/terminal?shell=bash", base, url.PathEscape(app.ID), url.PathEscape(processes[0].ID)), Metadata: map[string]string{"applicationId": app.ID, "processId": processes[0].ID}}, nil
}

func (p *Provider) Logs(ctx context.Context, id string, opts provider.LogOptions, dst io.Writer) error {
	app, err := p.resolve(ctx, id)
	if err != nil {
		return err
	}
	query := "?limit=" + strconv.Itoa(max(1, min(1000, opts.Tail)))
	var result json.RawMessage
	if err := p.request(ctx, http.MethodGet, "/applications/"+url.PathEscape(app.ID)+"/runtime-logs"+query, nil, &result); err != nil {
		return err
	}
	_, err = dst.Write(append(result, '\n'))
	return err
}

func (p *Provider) Usage(ctx context.Context, id string) (provider.Usage, error) {
	app, err := p.resolve(ctx, id)
	if err != nil {
		return provider.Usage{}, err
	}
	processes, err := p.processes(ctx, app.ID)
	if err != nil || len(processes) == 0 {
		return provider.Usage{}, fmt.Errorf("Sevalla process unavailable: %w", err)
	}
	usage := provider.Usage{ObservedAt: time.Now().UTC(), Cost: provider.Cost{Available: false, Estimated: true, Currency: "USD", Detail: "resource-price estimate; exact per-application accrued cost unavailable"}}
	return usage, nil
}

func (p *Provider) Exec(ctx context.Context, id string, argv []string, opts provider.ExecOptions) (provider.ExecResult, error) {
	if len(argv) == 0 {
		return provider.ExecResult{}, fmt.Errorf("command argv cannot be empty")
	}
	app, err := p.resolve(ctx, id)
	if err != nil {
		return provider.ExecResult{}, err
	}
	processes, err := p.processes(ctx, app.ID)
	if err != nil || len(processes) == 0 {
		return provider.ExecResult{}, fmt.Errorf("Sevalla process unavailable: %w", err)
	}
	if opts.Detach {
		argv = append([]string{"vmbox-runtime", "run", "--detach", "--"}, argv...)
	}
	if opts.Interactive {
		return p.interactive(ctx, app, processes[0], argv, opts)
	}
	if opts.Stdin != nil {
		return p.execStdin(ctx, app, processes[0], argv, opts)
	}
	started := time.Now().UTC()
	var output struct {
		Stdout   string `json:"stdout"`
		Stderr   string `json:"stderr"`
		ExitCode int    `json:"exit_code"`
	}
	input := map[string]any{"command": argv, "timeout": 60}
	path := "/applications/" + url.PathEscape(app.ID) + "/processes/" + url.PathEscape(processes[0].ID) + "/exec"
	if err := p.request(ctx, http.MethodPost, path, input, &output); err != nil {
		return provider.ExecResult{}, err
	}
	if opts.Stdout != nil {
		_, _ = io.WriteString(opts.Stdout, output.Stdout)
	}
	if opts.Stderr != nil {
		_, _ = io.WriteString(opts.Stderr, output.Stderr)
	}
	return provider.ExecResult{ExitCode: output.ExitCode, Stdout: output.Stdout, Stderr: output.Stderr, StartedAt: started, FinishedAt: time.Now().UTC()}, nil
}

func (p *Provider) interactive(ctx context.Context, app application, process process, argv []string, opts provider.ExecOptions) (provider.ExecResult, error) {
	started := time.Now().UTC()
	connection, err := p.Connection(ctx, app.ID)
	if err != nil {
		return provider.ExecResult{}, err
	}
	header := http.Header{}
	header.Set("Authorization", "Bearer "+p.cfg.Token)
	ws, _, err := websocket.Dial(ctx, connection.Endpoint, &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		return provider.ExecResult{}, fmt.Errorf("Sevalla terminal: %w", err)
	}
	defer ws.Close(websocket.StatusNormalClosure, "detached")
	conn := websocket.NetConn(ctx, ws, websocket.MessageText)
	defer conn.Close()
	// Sevalla upgrades the socket before the remote shell is ready to receive
	// input. Commands sent immediately after Dial are silently discarded.
	select {
	case <-ctx.Done():
		return provider.ExecResult{}, ctx.Err()
	case <-time.After(time.Second):
	}
	if _, err := io.WriteString(conn, "exec "+shellCommand(argv)+"\n"); err != nil {
		return provider.ExecResult{}, err
	}
	stdin := opts.Stdin
	if stdin == nil {
		stdin = strings.NewReader("")
	}
	stdout := opts.Stdout
	if stdout == nil {
		stdout = io.Discard
	}
	copyDone := make(chan error, 2)
	go func() { _, err := io.Copy(conn, stdin); copyDone <- err }()
	go func() { _, err := io.Copy(stdout, conn); copyDone <- err }()
	select {
	case <-ctx.Done():
		return provider.ExecResult{}, ctx.Err()
	case err := <-copyDone:
		if err != nil && !errors.Is(err, net.ErrClosed) {
			return provider.ExecResult{}, err
		}
	}
	return provider.ExecResult{ExitCode: 0, StartedAt: started, FinishedAt: time.Now().UTC()}, nil
}

func shellCommand(argv []string) string {
	quoted := make([]string, len(argv))
	for i, value := range argv {
		quoted[i] = "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
	}
	return strings.Join(quoted, " ")
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
