package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/config"
	"github.com/0xikarus/vmbox-service/internal/procexec"
)

func TestProfileUploadDialogNeverCreatesBox(t *testing.T) {
	for _, cancel := range []bool{false, true} {
		home := t.TempDir()
		path := filepath.Join(home, ".claude")
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, ".credentials.json"), []byte(`{"email":"person@example.test","synthetic":"profile"}`), 0600); err != nil {
			t.Fatal(err)
		}
		uploads := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.Method == "GET" && r.URL.Path == "/v1/login-profiles":
				json.NewEncoder(w).Encode([]v1.LoginProfile{})
			case r.Method == "PUT" && r.URL.Path == "/v1/login-profiles/claude/person-example.test":
				uploads++
				json.NewEncoder(w).Encode(v1.LoginProfile{Application: "claude", Name: "person-example.test"})
			default:
				t.Error("unexpected operation", r.Method, r.URL.Path)
				http.NotFound(w, r)
			}
		}))
		a := New()
		a.Environ = map[string]string{"HOME": home}
		a.Runner = &procexec.FakeRunner{}
		a.IsTerminal = func() bool { return true }
		var output, screen bytes.Buffer
		a.Out, a.Err = &output, &screen
		keys := " \r " // Space enables, Enter disables, Space enables again.
		if cancel {
			keys += "\x03"
		} else {
			keys += strings.Repeat("\t", 2) + "\r"
		}
		a.In = strings.NewReader(keys)
		err := a.controllerLoginProfiles(context.Background(), config.Context{Controller: server.URL}, "test", []string{"upload"})
		if cancel {
			if err == nil || uploads != 0 {
				t.Fatal("cancel uploaded")
			}
		} else if err != nil || uploads != 1 || !strings.Contains(output.String(), "No box created") {
			t.Fatal("upload failed", err, uploads)
		}
		if !strings.Contains(screen.String(), "[ Upload ]") {
			t.Fatal("wrong submit label")
		}
		for _, want := range []string{"Account", "Source", "[ ] claude", "[x] claude", "Space/Enter"} {
			if !strings.Contains(screen.String(), want) {
				t.Fatalf("table missing %q", want)
			}
		}
		server.Close()
	}
}
