package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/0xikarus/vmbox-service/internal/config"
)

func TestSplitRunPreservesArgv(t *testing.T) {
	name, detach, reuse, argv, err := splitRun([]string{"worker", "--detach", "--reuse", "--", "tool", "--flag", "two words", "$HOME;"})
	if err != nil {
		t.Fatal(err)
	}
	if name != "worker" || !detach || !reuse || !reflect.DeepEqual(argv, []string{"tool", "--flag", "two words", "$HOME;"}) {
		t.Fatalf("name=%q detach=%v reuse=%v argv=%#v", name, detach, reuse, argv)
	}
}
func TestControllerFailureDoesNotFallback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	file := config.File{Current: "team", Contexts: map[string]config.Context{"team": {Provider: "docker", Controller: "http://127.0.0.1:1", TokenEnv: "TEST_CONTROLLER_TOKEN"}}}
	if err := config.Save(path, file); err != nil {
		t.Fatal(err)
	}
	var out, stderr bytes.Buffer
	app := New()
	app.ConfigPath = path
	app.Out = &out
	app.Err = &stderr
	app.Environ = map[string]string{"TEST_CONTROLLER_TOKEN": "not-a-real-token"}
	err := app.Run(context.Background(), []string{"new", "box", "--detach", "--", "printf", "ok"})
	if err == nil || !strings.Contains(err.Error(), "no standalone fallback") {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(out.String(), "accepted") {
		t.Fatal("run was accepted despite unavailable controller")
	}
}
func TestContextContainsNoAmbientToken(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	app := New()
	app.ConfigPath = path
	app.Environ = map[string]string{"SEVALLA_API_TOKEN": "super-secret"}
	app.Out = &bytes.Buffer{}
	app.Err = &bytes.Buffer{}
	if err := app.Run(context.Background(), []string{"context", "add", "sev", "--provider", "sevalla", "--project", "p", "--cluster", "c"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("super-secret")) {
		t.Fatal("ambient token persisted in context")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("mode=%o", info.Mode().Perm())
	}
}
