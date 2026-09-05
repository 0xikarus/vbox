package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/0xikarus/vmbox-service/internal/config"
	"github.com/0xikarus/vmbox-service/internal/procexec"
)

func TestSetupRegionsUseProviderIdentifiers(t *testing.T) {
	railway := setupRegions(context.Background(), config.Context{Provider: "future-provider"}, "custom-region", nil)
	want := map[string]bool{"custom-region": true}
	if len(railway) != len(want) {
		t.Fatalf("Railway regions = %#v", railway)
	}
	for _, region := range railway {
		if !want[region.ID] {
			t.Fatalf("unexpected Railway region %#v", region)
		}
	}
}

func TestReusableSetupRestoresSelectionsAndSaveIntent(t *testing.T) {
	directory := t.TempDir()
	markdown := t.TempDir() + "/instructions.md"
	if err := os.WriteFile(markdown, []byte("instructions"), 0o600); err != nil {
		t.Fatal(err)
	}
	setup := defaultSetup(config.Context{Provider: "railway", Cluster: "iad"})
	setup.Save = false // Save is a UI-only choice and is intentionally not serialized.
	setup.Instructions = []string{markdown}
	path := filepath.Join(t.TempDir(), "config.json")
	if err := saveSetup(path, config.File{}, "test", directory, setup); err != nil {
		t.Fatal(err)
	}
	file, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := loadSetup(file, "test", directory)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Save {
		t.Fatal("a reused setup must default to saving subsequent changes")
	}

	app := New()
	var out bytes.Buffer
	app.In, app.Out, app.Err = strings.NewReader("q"), &out, &bytes.Buffer{}
	app.Runner = &procexec.FakeRunner{}
	_, err = app.configureSetup(context.Background(), config.Context{Name: "test", Provider: "railway"}, nil, "worker", loaded, nil)
	if !errors.Is(err, errSetupCancelled) {
		t.Fatalf("configureSetup error = %v", err)
	}
	screen := out.String()
	if !strings.Contains(screen, "[x] iad") || !strings.Contains(screen, "[x] "+markdown) || !strings.Contains(screen, "[x] Save this setup for --reuse in this working directory") {
		t.Fatalf("saved selections were not restored: %q", screen)
	}
	if _, err := loadSetup(file, "test", t.TempDir()); err == nil || !strings.Contains(err.Error(), "no complete reusable setup") {
		t.Fatalf("cross-directory reuse error = %v", err)
	}
}
