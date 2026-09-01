package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/0xikarus/vmbox-service/internal/config"
	"github.com/0xikarus/vmbox-service/internal/procexec"
)

func TestSetupRegionsUseProviderIdentifiers(t *testing.T) {
	railway := setupRegions(context.Background(), config.Context{Provider: "railway"}, "", nil)
	want := map[string]bool{"ams": true, "sfo": true, "iad": true, "sin": true}
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
	markdown := t.TempDir() + "/instructions.md"
	if err := os.WriteFile(markdown, []byte("instructions"), 0o600); err != nil {
		t.Fatal(err)
	}
	setup := defaultSetup(config.Context{Provider: "railway", Cluster: "iad"})
	setup.Save = false // Save is a UI-only choice and is intentionally not serialized.
	setup.Instructions = []string{markdown}
	file := config.File{LastSetups: map[string]config.CreationSetup{"test": setup}}
	loaded, err := loadSetup(file, "test")
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
	if !strings.Contains(screen, "[x] US East") || !strings.Contains(screen, "[x] "+markdown) || !strings.Contains(screen, "[x] Save this setup for --reuse") {
		t.Fatalf("saved selections were not restored: %q", screen)
	}
}
