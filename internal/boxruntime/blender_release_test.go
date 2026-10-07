package boxruntime

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

type blenderDownloadTransport func(*http.Request) (*http.Response, error)

func (transport blenderDownloadTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func TestBlenderPreservesExistingExecutable(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "bin"), 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, "bin", "blender")
	if err := os.WriteFile(path, []byte("user executable"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := linkPinnedBlender(home, "/new/blender"); err == nil {
		t.Fatal("existing executable replaced")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "user executable" {
		t.Fatal("existing executable changed")
	}
}

func TestOpenCodeBlenderRegistrationRespectsDesktopLock(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".config", "vmbox")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	lock, err := os.OpenFile(filepath.Join(dir, "mcp-registration.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	if err := registerOpenCodeBlender(home, "/blender-mcp"); err == nil {
		t.Fatal("concurrent MCP registration accepted")
	}
}

func TestBlenderRejectsUnverifiedArchive(t *testing.T) {
	isolateRuntimeEnv(t)
	withoutImageBlender(t)
	bin, home := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "sudo"), []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	original := http.DefaultClient
	t.Cleanup(func() { http.DefaultClient = original })
	http.DefaultClient = &http.Client{Transport: blenderDownloadTransport(func(request *http.Request) (*http.Response, error) {
		if request.URL.String() != blenderReleaseURL {
			t.Fatalf("unexpected release URL %s", request.URL)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("not the verified release"))}, nil
	})}
	err := installPinnedBlender(context.Background(), home, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("unverified archive accepted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "bin", "blender")); !os.IsNotExist(err) {
		t.Fatal("unverified executable installed")
	}
}

func TestOpenCodeBlenderPreservesExistingMCP(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("OPENCODE_CONFIG", "")
	path := filepath.Join(home, ".config", "opencode", "opencode.json")
	original := map[string]any{"mcp": map[string]any{"vmbox-desktop": map[string]any{"type": "local", "command": []string{"desktop-runtime"}}}, "model": "preserved-model"}
	if err := writeJSONAtomic(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	if err := registerOpenCodeBlender(home, "/custom/blender-mcp"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	var result map[string]json.RawMessage
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	if string(result["model"]) != `"preserved-model"` || !strings.Contains(string(data), "desktop-runtime") || !strings.Contains(string(data), "/custom/blender-mcp") {
		t.Fatalf("existing config damaged: %s", data)
	}
	if err := registerOpenCodeBlender(home, "/replacement"); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(data) {
		t.Fatal("existing Blender entry overwritten")
	}
}
