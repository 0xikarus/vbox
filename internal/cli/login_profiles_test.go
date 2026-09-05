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
)

func TestCreationProfilePickerSelectsOnlyChosenApplication(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/v1/login-profiles" {
			t.Error("unexpected mutation")
			return
		}
		json.NewEncoder(w).Encode([]v1.LoginProfile{{Application: "claude", Name: "personal"}, {Application: "codex", Name: "work"}})
	}))
	defer server.Close()
	a := New()
	a.In = strings.NewReader("\r\x1b[B\x1b[B\r")
	a.Out, a.Err = &bytes.Buffer{}, &bytes.Buffer{}
	a.IsTerminal = func() bool { return true }
	got, err := a.pickCreationProfiles(context.Background(), config.Context{Controller: server.URL}, "test")
	if err != nil || len(got) != 1 || got[0].Application != "codex" || got[0].Name != "work" {
		t.Fatalf("selection=%v error=%v", got, err)
	}
}

func TestSaveSelectedLocalLoginProfile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "auth.json"), []byte(`{"token":"synthetic-selected"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "unrelated-secret"), []byte("must-not-upload"), 0600); err != nil {
		t.Fatal(err)
	}
	writes := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PUT" || r.URL.Path != "/v1/login-profiles/codex/work" {
			t.Error("wrong target")
			return
		}
		var req v1.SaveLoginProfileRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
		}
		if len(req.Files) != 1 || !strings.Contains(string(req.Files["auth.json"]), "synthetic-selected") {
			t.Error("wrong files uploaded")
		}
		writes++
		json.NewEncoder(w).Encode(v1.LoginProfile{Application: "codex", Name: "work"})
	}))
	defer server.Close()
	a := New()
	var output bytes.Buffer
	a.Out, a.Err = &output, &output
	err := a.controllerLoginProfiles(context.Background(), config.Context{Controller: server.URL}, "test", []string{"save", "codex", "work", "--from", dir})
	if err != nil || writes != 1 {
		t.Fatalf("writes=%d error=%v", writes, err)
	}
	if strings.Contains(output.String(), "synthetic-selected") || strings.Contains(output.String(), "must-not-upload") {
		t.Fatal("secret leaked to output")
	}
}
