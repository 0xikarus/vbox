package cli

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/0xikarus/vmbox-service/internal/config"
	"github.com/0xikarus/vmbox-service/internal/provider"
)

type lifecycleCLIProvider struct {
	*cliProvider
	detached  []string
	sanitized []string
	deleted   []provider.Storage
}

func (p *lifecycleCLIProvider) AttachSession(context.Context, string, string, []string, provider.ExecOptions) (provider.ExecResult, error) {
	return provider.ExecResult{}, nil
}

func (p *lifecycleCLIProvider) DetachStorage(_ context.Context, id string, storage provider.Storage) error {
	p.detached = append(p.detached, id+":"+storage.ID)
	return nil
}

func (p *lifecycleCLIProvider) SanitizeSlot(_ context.Context, id string) error {
	p.sanitized = append(p.sanitized, id)
	return nil
}

func (p *lifecycleCLIProvider) DeleteStorage(_ context.Context, storage provider.Storage, _ provider.Owner) error {
	p.deleted = append(p.deleted, storage)
	return nil
}

func newLifecycleApp(input io.Reader) (*App, *lifecycleCLIProvider, config.File, *bytes.Buffer) {
	base := newCLIProvider()
	base.boxes["worker"] = provider.Box{
		ID: "service-id", Name: "worker", State: provider.StateRunning,
		Owner:   provider.Owner{AccountID: "standalone", BoxID: "worker"},
		Storage: &provider.Storage{ID: "volume-id", Name: "worker-data", MountPath: "/data"},
	}
	base.boxes["service-id"] = base.boxes["worker"]
	p := &lifecycleCLIProvider{cliProvider: base}
	var stderr bytes.Buffer
	app := New()
	app.In, app.Out, app.Err = input, &bytes.Buffer{}, &stderr
	app.IsTerminal = func() bool { return true }
	app.ExitPromptTimeout = 50 * time.Millisecond
	file := config.File{Contexts: map[string]config.Context{"test": {Name: "test", Provider: "test"}}}
	return app, p, file, &stderr
}

func TestHibernateOnExitCannotBeCombinedWithDetach(t *testing.T) {
	if _, err := parseRunOptions([]string{"worker", "--detach", "--hibernate-on-exit"}); err == nil || !strings.Contains(err.Error(), "cannot be combined") {
		t.Fatalf("error=%v", err)
	}
}
