package shared

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/0xikarus/vmbox-service/internal/provider"
	"github.com/0xikarus/vmbox-service/internal/sharedworker"
	"github.com/0xikarus/vmbox-service/internal/workerprotocol"
	"github.com/coder/websocket"
)

type Provider struct {
	endpoint string
	token    string
	client   *http.Client
}

func New(endpoint, token string) (*Provider, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return nil, errors.New("shared worker endpoint must be an HTTPS origin")
	}
	if parsed.Scheme != "https" && !(parsed.Scheme == "http" && (parsed.Hostname() == "127.0.0.1" || parsed.Hostname() == "localhost" || parsed.Hostname() == "::1")) {
		return nil, errors.New("shared worker endpoint requires HTTPS except on loopback")
	}
	if len(token) < 32 {
		return nil, errors.New("shared worker token requires at least 32 characters")
	}
	return &Provider{endpoint: strings.TrimRight(endpoint, "/"), token: token, client: &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func (p *Provider) Name() string { return "shared-worker" }

func (p *Provider) rpc(ctx context.Context, request sharedworker.Request) (sharedworker.Response, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	data, err := json.Marshal(request)
	if err != nil {
		return sharedworker.Response{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint+"/v1/rpc", bytes.NewReader(data))
	if err != nil {
		return sharedworker.Response{}, err
	}
	req.Header.Set("Authorization", "Bearer "+p.token)
	req.Header.Set("Content-Type", "application/json")
	response, err := p.client.Do(req)
	if err != nil {
		return sharedworker.Response{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return sharedworker.Response{}, fmt.Errorf("shared worker returned HTTP %d", response.StatusCode)
	}
	var result sharedworker.Response
	if err := json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(&result); err != nil {
		return result, err
	}
	if result.NotFound {
		return result, provider.ErrNotFound
	}
	if result.Error != "" {
		return result, errors.New(result.Error)
	}
	return result, nil
}

func (p *Provider) Validate(ctx context.Context) (provider.Capabilities, error) {
	_, err := p.rpc(ctx, sharedworker.Request{Operation: "validate"})
	return provider.Capabilities{Provider: p.Name(), Architectures: []string{"amd64"}, Interactive: true, Detached: true, ExactArgv: true, PersistentStorage: true, AutomatedStorage: true, ControllerCompatible: true, Warnings: []string{"Shared worker boxes use separate Unix users, not containers. CPU, memory, network and disk capacity are shared. Retained workspaces are pinned to this worker."}}, err
}

func (p *Provider) Create(ctx context.Context, req provider.CreateRequest) (provider.Box, error) {
	result, err := p.rpc(ctx, sharedworker.Request{Operation: "create", Create: req})
	return result.Box, err
}
func (p *Provider) Inspect(ctx context.Context, id string) (provider.Box, error) {
	result, err := p.rpc(ctx, sharedworker.Request{Operation: "inspect", ID: id})
	return result.Box, err
}
func (p *Provider) List(ctx context.Context) ([]provider.Box, error) {
	result, err := p.rpc(ctx, sharedworker.Request{Operation: "list"})
	return result.Boxes, err
}
func (p *Provider) Start(ctx context.Context, id string) (provider.Box, error) {
	result, err := p.rpc(ctx, sharedworker.Request{Operation: "start", ID: id})
	return result.Box, err
}
func (p *Provider) Stop(ctx context.Context, id string) (provider.Box, error) {
	result, err := p.rpc(ctx, sharedworker.Request{Operation: "stop", ID: id})
	return result.Box, err
}
func (p *Provider) Resize(context.Context, string, provider.Resources) (provider.Box, error) {
	return provider.Box{}, provider.ErrUnsupported
}
func (p *Provider) Delete(ctx context.Context, id string, owner provider.Owner) error {
	_, err := p.rpc(ctx, sharedworker.Request{Operation: "delete", ID: id, Owner: owner})
	return err
}
func (p *Provider) CreateStorage(context.Context, string, provider.Resources) (provider.Storage, error) {
	return provider.Storage{}, errors.New("shared storage requires explicit logical box ownership")
}
func (p *Provider) CreateWorkspaceStorage(ctx context.Context, id string, owner provider.Owner, resources provider.Resources) (provider.Storage, error) {
	result, err := p.rpc(ctx, sharedworker.Request{Operation: "create-storage", ID: id, Owner: owner, Resources: resources})
	if err != nil {
		return provider.Storage{}, err
	}
	if result.Storage == nil {
		return provider.Storage{}, errors.New("shared worker omitted storage identity")
	}
	return *result.Storage, nil
}
func (p *Provider) AttachStorage(ctx context.Context, id string, storage provider.Storage) error {
	_, err := p.rpc(ctx, sharedworker.Request{Operation: "attach", ID: id, Storage: storage})
	return err
}
func (p *Provider) DetachStorage(ctx context.Context, id string, storage provider.Storage) error {
	_, err := p.rpc(ctx, sharedworker.Request{Operation: "detach", ID: id, Storage: storage})
	return err
}
func (p *Provider) AttachedStorage(ctx context.Context, id string) (*provider.Storage, error) {
	box, err := p.Inspect(ctx, id)
	return box.Storage, err
}
func (p *Provider) SanitizeSlot(ctx context.Context, id string) error {
	_, err := p.rpc(ctx, sharedworker.Request{Operation: "sanitize", ID: id})
	return err
}
func (p *Provider) DeleteStorage(ctx context.Context, storage provider.Storage, owner provider.Owner) error {
	_, err := p.rpc(ctx, sharedworker.Request{Operation: "delete-storage", Storage: storage, Owner: owner})
	return err
}
func (p *Provider) Deploy(context.Context, string, string) (provider.Box, error) {
	return provider.Box{}, provider.ErrUnsupported
}
func (p *Provider) Connection(ctx context.Context, id string) (provider.Connection, error) {
	box, err := p.Inspect(ctx, id)
	return box.Connection, err
}
func (p *Provider) Logs(context.Context, string, provider.LogOptions, io.Writer) error {
	return provider.ErrUnsupported
}
func (p *Provider) Usage(context.Context, string) (provider.Usage, error) {
	return provider.Usage{}, provider.ErrUnsupported
}
func (p *Provider) Reconcile(ctx context.Context, box provider.Box) (provider.Box, error) {
	return p.Inspect(ctx, box.ID)
}
func (p *Provider) Exec(ctx context.Context, id string, argv []string, options provider.ExecOptions) (provider.ExecResult, error) {
	connection, err := p.Connection(ctx, id)
	if err != nil {
		return provider.ExecResult{}, err
	}
	return p.ExecConnection(ctx, connection, argv, options)
}

type boundedBuffer struct{ bytes.Buffer }

func (b *boundedBuffer) Write(data []byte) (int, error) {
	if b.Len()+len(data) > 16<<20 {
		return 0, errors.New("shared worker captured output limit exceeded")
	}
	return b.Buffer.Write(data)
}

func (p *Provider) ExecConnection(ctx context.Context, connection provider.Connection, argv []string, options provider.ExecOptions) (provider.ExecResult, error) {
	var stdout, stderr boundedBuffer
	if options.Stdout == nil {
		options.Stdout = &stdout
	}
	if options.Stderr == nil {
		options.Stderr = &stderr
	}
	result, err := p.StreamConnection(ctx, connection, argv, options)
	result.Stdout, result.Stderr = stdout.String(), stderr.String()
	return result, err
}

func (p *Provider) StreamConnection(ctx context.Context, connection provider.Connection, argv []string, options provider.ExecOptions) (result provider.ExecResult, err error) {
	if connection.Transport != "shared-worker" || connection.Endpoint == "" || len(argv) == 0 || options.Detach {
		return result, provider.ErrUnsupported
	}
	result.StartedAt = time.Now().UTC()
	defer func() { result.FinishedAt = time.Now().UTC() }()
	endpoint := strings.Replace(p.endpoint, "https://", "wss://", 1)
	endpoint = strings.Replace(endpoint, "http://", "ws://", 1)
	conn, _, err := websocket.Dial(ctx, endpoint+"/v1/exec", &websocket.DialOptions{HTTPClient: p.client, HTTPHeader: http.Header{"Authorization": {"Bearer " + p.token}}})
	if err != nil {
		return result, err
	}
	peer := workerprotocol.New(ctx, conn, true)
	defer peer.Close()
	binding := workerprotocol.Binding{AccountID: connection.Metadata["accountId"], BoxID: connection.Metadata["boxId"], SlotID: connection.Endpoint, Assignment: connection.Metadata["workspaceId"], Incarnation: connection.Metadata["deploymentInstanceId"]}
	stream, err := peer.Open(ctx, workerprotocol.Request{Binding: binding, OperationID: sharedworker.NewID(), Argv: argv})
	if err != nil {
		return result, err
	}
	defer stream.Close()
	go func() {
		if options.Stdin != nil {
			io.Copy(stream, options.Stdin)
		}
		stream.CloseWrite()
	}()
	result.ExitCode, err = workerprotocol.ReadOutput(stream, options.Stdout, options.Stderr)
	return result, err
}

var _ provider.Provider = (*Provider)(nil)
var _ provider.ConnectionStreamer = (*Provider)(nil)
var _ provider.DetachableStorageProvider = (*Provider)(nil)
