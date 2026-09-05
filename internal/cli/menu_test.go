package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/config"
)

func TestExplicitMenuOpensPickerWithoutMutatingOnCancel(t *testing.T) {
	for _, empty := range []bool{false, true} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet || r.URL.Path != "/v1/logical-boxes" {
				t.Error("unexpected operation", r.Method, r.URL.Path)
			}
			boxes := []v1.LogicalBox{}
			if !empty {
				boxes = []v1.LogicalBox{{Name: "ready-box", State: v1.LogicalBoxRunning}, {Name: "removing-box", State: v1.LogicalBoxDeleting}, {Name: "waking-box", State: v1.LogicalBoxHibernating, RestorationState: "detaching-volume"}}
			}
			json.NewEncoder(w).Encode(boxes)
		}))
		path := filepath.Join(t.TempDir(), "config.json")
		c := config.Context{Name: "test", Controller: server.URL, TokenEnv: "TEST_TOKEN"}
		if err := config.Save(path, config.File{Current: "test", Contexts: map[string]config.Context{"test": c}}); err != nil {
			t.Fatal(err)
		}
		var screen bytes.Buffer
		a := New()
		a.ConfigPath, a.In, a.Out, a.Err = path, strings.NewReader("q"), &bytes.Buffer{}, &screen
		a.Environ = map[string]string{"TEST_TOKEN": "synthetic"}
		a.IsTerminal = func() bool { return true }
		err := a.Run(context.Background(), []string{"menu"})
		server.Close()
		if err == nil || !strings.Contains(err.Error(), "selection cancelled") {
			t.Fatal("bare command did not open cancellable picker", err)
		}
		if !strings.Contains(screen.String(), "+ Create a box") || strings.Contains(screen.String(), "removing-box") {
			t.Fatal("picker missing create action or offering deleting box")
		}
		if !empty && !strings.Contains(screen.String(), "detaching-volume") {
			t.Fatal("precise box phase missing")
		}
	}
}

func TestHelpIsOfflineAndShortByDefault(t *testing.T) {
	for _, args := range [][]string{{"help"}, {"help", "--all"}} {
		var out bytes.Buffer
		a := New()
		a.ConfigPath = "/nonexistent/vmbox-test/config"
		a.Out = &out
		a.IsTerminal = func() bool { return false }
		if err := a.Run(context.Background(), args); err != nil {
			t.Fatal(err)
		}
		full := len(args) == 2
		if strings.Contains(out.String(), "notifications list") != full {
			t.Fatal("advanced commands not confined to full help")
		}
		if !full && !strings.Contains(out.String(), "vmbox help --all") {
			t.Fatal("full help not discoverable")
		}
	}
}
