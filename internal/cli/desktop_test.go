package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/config"
	"github.com/0xikarus/vmbox-service/internal/procexec"
	"github.com/0xikarus/vmbox-service/internal/provider"
)

type desktopRunner struct {
	mu  sync.Mutex
	ssh []string
}

func (r *desktopRunner) Run(ctx context.Context, args []string, _ io.Reader, _, _ io.Writer) (procexec.Result, error) {
	address := strings.Replace(args[1], "::", ":", 1)
	conn, err := net.DialTimeout("tcp", address, time.Second)
	if err != nil {
		return procexec.Result{}, err
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(time.Second))
	if _, err := conn.Write([]byte{0, 255}); err != nil {
		return procexec.Result{}, err
	}
	data := make([]byte, 2)
	_, err = io.ReadFull(conn, data)
	if err == nil && (data[0] != 0 || data[1] != 255) {
		err = fmt.Errorf("binary data changed")
	}
	return procexec.Result{}, err
}

func (r *desktopRunner) RunAttached(_ context.Context, args []string, input io.Reader, output, _ io.Writer) (procexec.Result, error) {
	r.mu.Lock()
	r.ssh = append([]string(nil), args...)
	r.mu.Unlock()
	data := make([]byte, 2)
	if _, err := io.ReadFull(input, data); err != nil {
		return procexec.Result{}, err
	}
	_, err := output.Write(data)
	return procexec.Result{}, err
}

func TestDesktopReusesControllerLifecycleAndPrivateStream(t *testing.T) {
	for _, running := range []bool{true, false} {
		fence := strings.Repeat("a", 64)
		var requests []string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requests = append(requests, r.Method+" "+r.URL.Path)
			if r.Header.Get("Authorization") != "Bearer owner" {
				w.WriteHeader(401)
				return
			}
			var value any
			switch r.URL.Path {
			case "/v1/capabilities":
				value = map[string]bool{"nativeAttach": true}
			case "/v1/logical-boxes/test":
				state := v1.LogicalBoxHibernated
				if running {
					state = v1.LogicalBoxRunning
				}
				value = v1.LogicalBox{ID: "id", Name: "test", State: state}
			case "/v1/logical-boxes/id/allocate":
				if r.Header.Get("Idempotency-Key") == "" {
					t.Error("missing allocation key")
				}
				value = v1.Allocation{State: "ready"}
			case "/v1/logical-boxes/id/desktop/enable", "/v1/logical-boxes/id/desktop":
				w.WriteHeader(204)
				return
			case "/v1/logical-boxes/id/sessions":
				value = v1.SessionInventory{State: "live", Assignment: fence, Sessions: []v1.Session{{ID: "$2", Name: "vmbox-desktop", Incarnation: "inc"}}}
			case "/v1/logical-boxes/id/native-connection":
				if r.URL.Query().Get("sessionId") != "$2" || r.URL.Query().Get("incarnation") != "inc" {
					t.Error("session not fenced")
				}
				value = v1.NativeConnection{LogicalBoxConnection: v1.LogicalBoxConnection{LogicalBoxID: "id", Connection: provider.Connection{Transport: "openssh", Endpoint: "user@host"}}, Assignment: fence, SessionID: "$2", Incarnation: "inc"}
			default:
				t.Errorf("unexpected operation %s", r.URL.Path)
				w.WriteHeader(404)
				return
			}
			json.NewEncoder(w).Encode(value)
		}))
		a := New()
		a.Out, a.Err = io.Discard, io.Discard
		runner := &desktopRunner{}
		a.Runner = runner
		viewer := filepath.Join(t.TempDir(), "viewer")
		if err := os.WriteFile(viewer, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		err := a.controllerDesktop(ctx, config.Context{Controller: server.URL}, "owner", []string{"test", "--enable", "--viewer", viewer})
		cancel()
		server.Close()
		if err != nil {
			t.Fatal(err)
		}
		allocations := 0
		for _, request := range requests {
			if strings.HasSuffix(request, "/allocate") {
				allocations++
			}
		}
		if (running && allocations != 0) || (!running && allocations != 1) {
			t.Fatalf("allocations=%d running=%v", allocations, running)
		}
		runner.mu.Lock()
		ssh := strings.Join(runner.ssh, " ")
		runner.mu.Unlock()
		for _, part := range []string{"-T", "ControlPath=none", "ForwardAgent=no", "desktop-stream", fence} {
			if !strings.Contains(ssh, part) {
				t.Fatalf("missing %q in transport", part)
			}
		}
		if strings.Contains(ssh, "-tt") || strings.Contains(ssh, "owner") {
			t.Fatal("VNC allocated a tty or leaked controller token")
		}
	}
}

func TestDesktopMissingViewerDoesNotWakeBox(t *testing.T) {
	a := New()
	a.Err = io.Discard
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("missing viewer caused controller operation")
		w.WriteHeader(500)
	}))
	defer server.Close()
	err := a.controllerDesktop(context.Background(), config.Context{Controller: server.URL}, "test", []string{"box", "--viewer", filepath.Join(t.TempDir(), "missing")})
	if err == nil || !strings.Contains(err.Error(), "--no-viewer") {
		t.Fatalf("error=%v", err)
	}
}
