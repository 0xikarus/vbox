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

func TestInteractiveExitEnterKeepsRunning(t *testing.T) {
	app, p, file, stderr := newLifecycleApp(strings.NewReader("\n"))
	if err := app.standalone(context.Background(), file, p, file.Contexts["test"], []string{"new", "worker"}); err != nil {
		t.Fatal(err)
	}
	if len(p.detached) != 0 || len(p.deleted) != 0 || !strings.Contains(stderr.String(), "reconnect with: vmbox worker") {
		t.Fatalf("detached=%v deleted=%v stderr=%q", p.detached, p.deleted, stderr.String())
	}
}

func TestInteractiveExitTimeoutKeepsRunning(t *testing.T) {
	reader, writer := io.Pipe()
	defer writer.Close()
	app, p, file, stderr := newLifecycleApp(reader)
	app.ExitPromptTimeout = 5 * time.Millisecond
	if err := app.standalone(context.Background(), file, p, file.Contexts["test"], []string{"new", "worker"}); err != nil {
		t.Fatal(err)
	}
	if len(p.detached) != 0 || !strings.Contains(stderr.String(), "keeping \"worker\" running") {
		t.Fatalf("detached=%v stderr=%q", p.detached, stderr.String())
	}
}

func TestInteractiveExitHibernatesAndRetainsVolume(t *testing.T) {
	app, p, file, stderr := newLifecycleApp(strings.NewReader("2\n"))
	if err := app.standalone(context.Background(), file, p, file.Contexts["test"], []string{"new", "worker"}); err != nil {
		t.Fatal(err)
	}
	if len(p.detached) != 1 || len(p.sanitized) != 1 || len(p.deleted) != 0 {
		t.Fatalf("detached=%v sanitized=%v deleted=%v", p.detached, p.sanitized, p.deleted)
	}
	if !strings.Contains(stderr.String(), "retained volume") {
		t.Fatalf("stderr=%q", stderr.String())
	}
}

func TestInteractiveDeleteRequiresExactBoxNameAndDeletesOnlyVolume(t *testing.T) {
	app, p, file, stderr := newLifecycleApp(strings.NewReader("3\nworker\n"))
	if err := app.standalone(context.Background(), file, p, file.Contexts["test"], []string{"new", "worker"}); err != nil {
		t.Fatal(err)
	}
	if len(p.detached) != 1 || len(p.sanitized) != 1 || len(p.deleted) != 1 || p.deleted[0].ID != "volume-id" {
		t.Fatalf("detached=%v sanitized=%v deleted=%v", p.detached, p.sanitized, p.deleted)
	}
	if !strings.Contains(stderr.String(), "Railway volume: worker-data (ID: volume-id)") || !strings.Contains(stderr.String(), "deleted only volume") {
		t.Fatalf("stderr=%q", stderr.String())
	}
}

func TestInteractiveDeleteWrongConfirmationKeepsEverything(t *testing.T) {
	app, p, file, stderr := newLifecycleApp(strings.NewReader("3\nnot-worker\n"))
	if err := app.standalone(context.Background(), file, p, file.Contexts["test"], []string{"new", "worker"}); err != nil {
		t.Fatal(err)
	}
	if len(p.detached) != 0 || len(p.deleted) != 0 || !strings.Contains(stderr.String(), "deletion cancelled") {
		t.Fatalf("detached=%v deleted=%v stderr=%q", p.detached, p.deleted, stderr.String())
	}
}

func TestHibernateOnExitCannotBeCombinedWithDetach(t *testing.T) {
	if _, err := parseRunOptions([]string{"worker", "--detach", "--hibernate-on-exit"}); err == nil || !strings.Contains(err.Error(), "cannot be combined") {
		t.Fatalf("error=%v", err)
	}
}
