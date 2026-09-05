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

func TestLocationPickerRemembersProviderScopedChoice(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/locations" || r.URL.Query().Get("provider") != "example" || r.URL.Query().Get("providerCredential") != "primary" {
			t.Error("wrong provider target")
		}
		json.NewEncoder(w).Encode([]v1.LocationPreset{{ID: "near", AvailableSlots: 1}, {ID: "far", AvailableSlots: 0}})
	}))
	defer server.Close()
	a := New()
	a.ConfigPath = filepath.Join(t.TempDir(), "config.json")
	a.In = strings.NewReader("\x1b[B\r")
	a.Out, a.Err = &bytes.Buffer{}, &bytes.Buffer{}
	a.IsTerminal = func() bool { return true }
	c := config.Context{Name: "test", Controller: server.URL, Provider: "example", ProviderCredential: "primary"}
	if err := config.Save(a.ConfigPath, config.File{Current: "test", Contexts: map[string]config.Context{"test": c}}); err != nil {
		t.Fatal(err)
	}
	got, err := a.pickLocation(context.Background(), c, "test")
	if err != nil || got != "near" {
		t.Fatalf("location=%s error=%v", got, err)
	}
	file, err := config.Load(a.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	c = file.Contexts["test"]
	if c.LocationPresets["example/primary"] != "near" {
		t.Fatal("location not persisted")
	}
	a.In = strings.NewReader("\r")
	got, err = a.pickLocation(context.Background(), c, "test")
	if err != nil || got != "near" {
		t.Fatal("remembered selection not initial choice")
	}
}
