package sharedworker

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/0xikarus/vmbox-service/internal/provider"
	"github.com/0xikarus/vmbox-service/internal/workerprotocol"
	"github.com/coder/websocket"
)

type Request struct {
	Operation string                  `json:"operation"`
	ID        string                  `json:"id,omitempty"`
	Create    provider.CreateRequest  `json:"create,omitempty"`
	Owner     provider.Owner          `json:"owner,omitempty"`
	Resources provider.Resources      `json:"resources,omitempty"`
	Storage   provider.Storage        `json:"storage,omitempty"`
	Settings  provider.WorkerSettings `json:"settings,omitempty"`
}

type Response struct {
	Box           provider.Box            `json:"box,omitempty"`
	Boxes         []provider.Box          `json:"boxes,omitempty"`
	Storage       *provider.Storage       `json:"storage,omitempty"`
	Resources     *provider.Resources     `json:"resources,omitempty"`
	ResourceUsage *provider.ResourceUsage `json:"resourceUsage,omitempty"`
	HostResources *provider.HostResources `json:"hostResources,omitempty"`
	Capacity      int                     `json:"capacity,omitempty"`
	WorkerConfig  *provider.WorkerConfig  `json:"workerConfig,omitempty"`
	Error         string                  `json:"error,omitempty"`
	NotFound      bool                    `json:"notFound,omitempty"`
}

// ErrUnsupportedOperation is what a worker answers for an operation it does
// not know. Its text is the wire contract with older controllers and workers.
var ErrUnsupportedOperation = errors.New("unsupported shared worker operation")

type Server struct {
	Store       *Store
	Runtime     *LinuxRuntime
	Token       string
	connections chan struct{}
}

func NewServer(store *Store, runtime *LinuxRuntime, token string) (*Server, error) {
	if store == nil || runtime == nil || len(token) < 32 {
		return nil, errors.New("shared worker requires store, runtime and token of at least 32 characters")
	}
	return &Server{Store: store, Runtime: runtime, Token: token, connections: make(chan struct{}, 128)}, nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/healthz" && r.Method == http.MethodGet {
		if s.Store.Health() != nil {
			http.Error(w, "worker unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok"}`))
		return
	}
	if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+s.Token)) != 1 {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if r.URL.Path == "/v1/exec" && r.Method == http.MethodGet {
		s.execute(w, r)
		return
	}
	if r.URL.Path != "/v1/rpc" || r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var request Request
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	response, err := s.dispatch(r.Context(), request)
	if err != nil {
		response.Error, response.NotFound = err.Error(), errors.Is(err, provider.ErrNotFound)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func (s *Server) dispatch(ctx context.Context, request Request) (response Response, err error) {
	switch request.Operation {
	case "validate":
		response.Capacity = s.Store.SlotCapacity()
		err = s.Store.Health()
	case "host-resources":
		var resources provider.HostResources
		resources, err = HostResources(filepath.Join(s.Store.Root, "workspaces"))
		if err == nil {
			response.HostResources = &resources
		}
	case "worker-config", "set-worker-settings":
		var config provider.WorkerConfig
		if request.Operation == "worker-config" {
			config, err = s.Store.WorkerConfig()
		} else {
			config, err = s.Store.SetSettings(request.Settings)
		}
		if err == nil {
			response.WorkerConfig = &config
		}
	case "create":
		response.Box, err = s.Store.Create(request.Create)
	case "inspect":
		response.Box, err = s.Store.Inspect(request.ID)
	case "list":
		response.Boxes, err = s.Store.List()
	case "start":
		response.Box, err = s.Store.Start(ctx, request.ID)
	case "stop":
		err = s.Store.Release(ctx, request.ID, nil)
		if err == nil {
			response.Box, err = s.Store.Inspect(request.ID)
		}
	case "delete":
		err = s.Store.DeleteSlot(request.ID, request.Owner)
	case "create-storage":
		var storage provider.Storage
		storage, err = s.Store.CreateStorage(ctx, request.ID, request.Owner, request.Resources)
		if err == nil {
			response.Storage = &storage
		}
	case "attach":
		err = s.Store.Attach(ctx, request.ID, request.Storage)
	case "detach":
		err = s.Store.Release(ctx, request.ID, &request.Storage)
	case "delete-storage":
		err = s.Store.DeleteStorage(ctx, request.Storage, request.Owner)
	case "sanitize":
		var box provider.Box
		box, err = s.Store.Inspect(request.ID)
		if err == nil && box.Storage != nil {
			err = errors.New("slot still contains a workspace")
		}
	case "resource-limits":
		var resources provider.Resources
		resources, err = s.Store.ResourceLimits(ctx, request.ID)
		if err == nil {
			response.Resources = &resources
		}
	case "resource-usage":
		var usage provider.ResourceUsage
		usage, err = s.Store.ResourceUsage(ctx, request.ID)
		if err == nil {
			response.ResourceUsage = &usage
		}
	case "set-resource-limits":
		err = s.Store.SetResourceLimits(ctx, request.ID, request.Resources)
	default:
		err = ErrUnsupportedOperation
	}
	return
}

func connectionFor(binding workerprotocol.Binding) provider.Connection {
	return provider.Connection{Transport: "shared-worker", Endpoint: binding.SlotID, Metadata: map[string]string{"deploymentInstanceId": binding.Incarnation, "workspaceId": binding.Assignment}}
}

func (s *Server) validate(ctx context.Context, binding workerprotocol.Binding) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	workspace, err := s.Store.WorkspaceFor(connectionFor(binding))
	if err != nil {
		return err
	}
	return provider.VerifyOwner(workspace.Owner, provider.Owner{AccountID: binding.AccountID, BoxID: binding.BoxID})
}

func (s *Server) execute(w http.ResponseWriter, r *http.Request) {
	select {
	case s.connections <- struct{}{}:
		defer func() { <-s.connections }()
	default:
		http.Error(w, "worker stream capacity reached", http.StatusServiceUnavailable)
		return
	}
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	peer := workerprotocol.New(r.Context(), conn, false)
	defer peer.Close()
	stream, err := peer.Accept(r.Context())
	if err != nil {
		return
	}
	defer stream.Close()
	if err := s.validate(r.Context(), stream.Request.Binding); err != nil {
		return
	}
	connection := connectionFor(stream.Request.Binding)
	workspace, err := s.Store.WorkspaceFor(connection)
	if err != nil {
		return
	}
	journal := workerprotocol.Journal{Directory: filepath.Join(s.Store.Root, ".shared-worker", "operations", workspace.ID)}
	workerprotocol.ExecuteWithCommand(r.Context(), stream, journal, s.validate,
		func(ctx context.Context, argv []string) (*exec.Cmd, error) {
			return s.Runtime.Command(ctx, workspace, s.workspaceArgv(workspace, argv))
		},
		func(command *exec.Cmd) error { return s.Store.StartCommand(connection, command) })
	select {
	case <-peer.Done():
	case <-r.Context().Done():
	}
}

func (s *Server) workspaceArgv(workspace Workspace, argv []string) []string {
	result := append([]string(nil), argv...)
	// The namespace tier binds the box's own directory at /data, so logical
	// /data paths already resolve inside the box and must not be remapped to the
	// host workspace root.
	if s.Runtime.Isolated() {
		return result
	}
	remap := func(path string) string {
		if path == "/data" {
			return s.Runtime.workspaceRoot(workspace)
		}
		if strings.HasPrefix(path, "/data/") {
			return s.Runtime.workspaceRoot(workspace) + strings.TrimPrefix(path, "/data")
		}
		return path
	}
	if len(result) > 0 {
		result[0] = remap(result[0])
	}
	if len(result) == 7 && result[0] == "sh" && result[1] == "-c" && result[3] == "vmbox-install-runtime" {
		result[4], result[5] = remap(result[4]), remap(result[5])
	}
	return result
}
