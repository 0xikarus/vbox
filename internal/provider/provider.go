package provider

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"
)

type State string

const (
	StateUnknown      State = "unknown"
	StateProvisioning State = "provisioning"
	StateRunning      State = "running"
	StateStopped      State = "stopped"
	StateFailed       State = "failed"
	StateDeleting     State = "deleting"
)

type Resources struct {
	CPU       float64 `json:"cpu"`
	MemoryMiB int64   `json:"memoryMiB"`
	DiskGiB   int64   `json:"diskGiB"`
	PIDs      int64   `json:"pids,omitempty"`
}

type Owner struct {
	AccountID string `json:"accountId"`
	BoxID     string `json:"boxId"`
	RunID     string `json:"runId,omitempty"`
	Lease     string `json:"lease,omitempty"`
}

type CreateRequest struct {
	Name       string            `json:"name"`
	Image      string            `json:"image"`
	Region     string            `json:"region,omitempty"`
	Resources  Resources         `json:"resources"`
	Owner      Owner             `json:"owner"`
	Env        map[string]string `json:"env,omitempty"`
	Command    []string          `json:"command,omitempty"`
	Components []string          `json:"components,omitempty"`
	Detached   bool              `json:"detached,omitempty"`
}

type Box struct {
	ID            string            `json:"id"`
	Name          string            `json:"name"`
	Provider      string            `json:"provider"`
	State         State             `json:"state"`
	ProviderState string            `json:"providerState,omitempty"`
	Image         string            `json:"image,omitempty"`
	ImageDigest   string            `json:"imageDigest,omitempty"`
	Region        string            `json:"region,omitempty"`
	Resources     Resources         `json:"resources"`
	Owner         Owner             `json:"owner"`
	Labels        map[string]string `json:"labels,omitempty"`
	CreatedAt     time.Time         `json:"createdAt,omitempty"`
	UpdatedAt     time.Time         `json:"updatedAt,omitempty"`
	Connection    Connection        `json:"connection,omitempty"`
	Storage       *Storage          `json:"storage,omitempty"`
}

type Storage struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	MountPath string `json:"mountPath"`
	SizeGiB   int64  `json:"sizeGiB,omitempty"`
	Manual    bool   `json:"manual,omitempty"`
}

type Connection struct {
	Transport string            `json:"transport"`
	Endpoint  string            `json:"endpoint,omitempty"`
	Metadata  map[string]string `json:"metadata,omitempty"`
}

type Cost struct {
	Currency  string  `json:"currency,omitempty"`
	Accrued   float64 `json:"accrued,omitempty"`
	Estimated bool    `json:"estimated,omitempty"`
	Available bool    `json:"available"`
	Detail    string  `json:"detail,omitempty"`
}

type Usage struct {
	CPUPercent   float64   `json:"cpuPercent,omitempty"`
	MemoryBytes  int64     `json:"memoryBytes,omitempty"`
	StorageBytes int64     `json:"storageBytes,omitempty"`
	ObservedAt   time.Time `json:"observedAt"`
	Cost         Cost      `json:"cost"`
}

type LogOptions struct {
	Follow bool
	Tail   int
	Since  time.Time
}

type ExecOptions struct {
	Interactive bool
	Detach      bool
	Stdin       io.Reader
	Stdout      io.Writer
	Stderr      io.Writer
}

type ExecResult struct {
	RunID      string    `json:"runId,omitempty"`
	ExitCode   int       `json:"exitCode"`
	Stdout     string    `json:"stdout,omitempty"`
	Stderr     string    `json:"stderr,omitempty"`
	StartedAt  time.Time `json:"startedAt,omitempty"`
	FinishedAt time.Time `json:"finishedAt,omitempty"`
}

// BootstrapRequest contains the provider-neutral runtime payload installed
// into a newly created or resumed generic Linux image. Providers select the
// binary matching the workload architecture and transport it without placing
// its bytes or credentials in process arguments.
type BootstrapRequest struct {
	Components        []string
	RestoreComponents bool
	RuntimeBinaries   map[string][]byte
	Entrypoint        []byte
}

// Bootstrapper is implemented by providers that can turn a standard
// Debian/Ubuntu-compatible image into a vmbox workload. Bootstrap must be
// idempotent because the CLI invokes it on both creation and resume.
type Bootstrapper interface {
	Bootstrap(context.Context, string, BootstrapRequest) error
}

// SessionAttacher lets providers use their native interactive transport after
// vmbox has prepared a named tmux session inside the workload.
type SessionAttacher interface {
	AttachSession(context.Context, string, string, ExecOptions) (ExecResult, error)
}

type Capabilities struct {
	Provider             string   `json:"provider"`
	Architectures        []string `json:"architectures"`
	Interactive          bool     `json:"interactive"`
	Detached             bool     `json:"detached"`
	ExactArgv            bool     `json:"exactArgv"`
	PersistentStorage    bool     `json:"persistentStorage"`
	AutomatedStorage     bool     `json:"automatedStorage"`
	Resize               bool     `json:"resize"`
	Metrics              bool     `json:"metrics"`
	Cost                 bool     `json:"cost"`
	ControllerCompatible bool     `json:"controllerCompatible"`
	Warnings             []string `json:"warnings,omitempty"`
}

// Provider is the single lifecycle contract used in standalone and controller
// mode. Implementations must be idempotent and scope destructive operations to
// resources carrying the requested Owner metadata.
type Provider interface {
	Name() string
	Validate(context.Context) (Capabilities, error)
	Create(context.Context, CreateRequest) (Box, error)
	Inspect(context.Context, string) (Box, error)
	List(context.Context) ([]Box, error)
	Start(context.Context, string) (Box, error)
	Stop(context.Context, string) (Box, error)
	Resize(context.Context, string, Resources) (Box, error)
	Delete(context.Context, string, Owner) error
	CreateStorage(context.Context, string, Resources) (Storage, error)
	AttachStorage(context.Context, string, Storage) error
	DeleteStorage(context.Context, Storage, Owner) error
	Deploy(context.Context, string, string) (Box, error)
	Connection(context.Context, string) (Connection, error)
	Logs(context.Context, string, LogOptions, io.Writer) error
	Usage(context.Context, string) (Usage, error)
	Exec(context.Context, string, []string, ExecOptions) (ExecResult, error)
	Reconcile(context.Context, Box) (Box, error)
}

var ErrNotFound = errors.New("box not found")
var ErrUnsupported = errors.New("provider capability unsupported")

func ValidateName(name string) error {
	if name == "" || len(name) > 63 {
		return fmt.Errorf("box name must contain 1 to 63 characters")
	}
	for i, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-' {
			continue
		}
		return fmt.Errorf("invalid box name character %q at offset %d", r, i)
	}
	return nil
}

func VerifyOwner(actual, requested Owner) error {
	if actual.BoxID == "" || requested.BoxID == "" || actual.BoxID != requested.BoxID ||
		actual.AccountID != requested.AccountID {
		return fmt.Errorf("ownership mismatch: refusing destructive operation")
	}
	if requested.Lease != "" && actual.Lease != requested.Lease {
		return fmt.Errorf("lease mismatch: refusing stale destructive operation")
	}
	return nil
}
