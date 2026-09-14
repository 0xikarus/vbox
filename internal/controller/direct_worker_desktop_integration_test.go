package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/provider"
	"github.com/0xikarus/vmbox-service/internal/workeragent"
	"github.com/0xikarus/vmbox-service/internal/workerprotocol"
)

// TestDirectWorkerDesktopRuntimePostgres is an opt-in acceptance test. It uses
// an isolated local container and database schema; it never contacts Railway.
// Run it with a disposable VMBOX_TEST_DATABASE_URL and, after making the image
// available locally, set:
//
//	VMBOX_DIRECT_WORKER_DESKTOP_IMAGE=ghcr.io/0xikarus/vmbox-service:agent-desktop-0fcf3f6
func TestDirectWorkerDesktopRuntimePostgres(t *testing.T) {
	dsn := os.Getenv("VMBOX_TEST_DATABASE_URL")
	image := os.Getenv("VMBOX_DIRECT_WORKER_DESKTOP_IMAGE")
	if dsn == "" || image == "" {
		t.Skip("requires disposable PostgreSQL and a locally available desktop image")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("requires Docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if err := exec.CommandContext(ctx, "docker", "image", "inspect", image).Run(); err != nil {
		t.Skip("desktop image is not available locally")
	}

	container := "vmbox-direct-desktop-" + strings.ReplaceAll(uuid(), "-", "")
	docker := func(args ...string) []byte {
		t.Helper()
		command := exec.CommandContext(ctx, "docker", args...)
		out, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("docker %s: %v: %s", strings.Join(args, " "), err, out)
		}
		return out
	}
	docker("run", "--detach", "--rm", "--network", "none", "--name", container, "--user", "10001:10001", "--entrypoint", "sleep", image, "infinity")
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		_ = exec.CommandContext(cleanup, "docker", "rm", "--force", container).Run()
	})
	docker("exec", "--user", "0:0", container, "sh", "-c", "mkdir -p /data/home /data/workspace && chown -R 10001:10001 /data")

	store, principal, ownerToken, slot, worker, credential, binding := directWorkerDesktopStore(t, ctx, dsn)

	// Initialize the private tmux server and shell through the real runtime with
	// the exact assignment expected by the controller and agent.
	containerExec := func(args ...string) []byte {
		t.Helper()
		base := []string{"exec", "--user", "10001:10001", "--env", "HOME=/data/home", container}
		return docker(append(base, args...)...)
	}
	containerExec("vmbox-runtime", "native-bind", binding.Assignment)
	containerExec("vmbox-runtime", "interactive-start", binding.Assignment, "acceptance-shell", "shell")

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	bindingPath := filepath.Join(dir, "binding.json")
	bindingData, _ := json.Marshal(binding)
	if err := os.WriteFile(bindingPath, bindingData, 0600); err != nil {
		t.Fatal(err)
	}
	config := workeragent.Config{
		ControllerURL:    "https://controller.invalid",
		AccountID:        principal.AccountID,
		SlotID:           slot,
		WorkerID:         worker.ID,
		Credential:       credential,
		JournalDirectory: filepath.Join(dir, "journal"),
		BindingFile:      bindingPath,
	}
	configData, _ := json.Marshal(config)
	if err := os.WriteFile(configPath, configData, 0600); err != nil {
		t.Fatal(err)
	}

	// The shim accepts only the workload-user form emitted by the provider and
	// forwards stdin/stdout unchanged to this single disposable container.
	shim := filepath.Join(dir, "sudo")
	shimScript := fmt.Sprintf(`#!/bin/sh
[ "$1" = -n ] && [ "$2" = -H ] && [ "$3" = -u ] && [ "$4" = vmbox ] && [ "$5" = -- ] || exit 97
shift 5
exec docker exec --interactive --user 10001:10001 --env HOME=/data/home %q "$@"
`, container)
	if err := os.WriteFile(shim, []byte(shimScript), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))

	server := NewServer(store, nil)
	server.DirectWorkersEnabled = true
	server.Resolve = func(context.Context, string, string, string) (provider.Provider, error) {
		return &noRailwayCallsProvider{}, nil
	}
	// Keep one stable HTTPS address while allowing the controller process object
	// to be replaced later in the test, as it would be behind a stable service.
	var handlerMu sync.RWMutex
	activeHandler := server.Handler()
	tls := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerMu.RLock()
		handler := activeHandler
		handlerMu.RUnlock()
		handler.ServeHTTP(w, r)
	}))
	defer tls.Close()
	config.ControllerURL = tls.URL
	agentCtx, stopAgent := context.WithCancel(ctx)
	defer stopAgent()
	connected := make(chan struct{}, 4)
	agent := &workeragent.Agent{Config: config, ConfigFile: configPath, HTTP: tls.Client(), Incarnation: binding.Incarnation, Report: func(message string) {
		if message == "worker connected" {
			select {
			case connected <- struct{}{}:
			default:
			}
		}
	}}
	agentDone := make(chan error, 1)
	go func() { agentDone <- agent.Run(agentCtx) }()
	waitDirectWorkerConnect(t, ctx, connected)
	active := waitDirectWorkerPeer(t, ctx, server, worker.ID, 0)
	if _, err := store.DB.ExecContext(ctx, `UPDATE direct_workers SET transport_enabled=true WHERE id=$1`, worker.ID); err != nil {
		t.Fatal(err)
	}

	wrapped := &directWorkerProvider{Provider: &noRailwayCallsProvider{}, server: server, accountID: principal.AccountID}
	connection, err := wrapped.Connection(ctx, "disposable-desktop-service")
	if err != nil || connection.Transport != directWorkerTransport {
		t.Fatalf("direct connection: %+v %v", connection, err)
	}

	inventory := directWorkerRuntimeInventory(t, ctx, wrapped, binding.Assignment)
	shell := sessionNamed(t, inventory, "acceptance-shell")
	firstToken := "terminal-before-reconnect-" + strings.ReplaceAll(uuid(), "-", "")
	directWorkerTerminalInput(t, ctx, wrapped, connection, binding.Assignment, shell, firstToken, containerExec)

	result, err := wrapped.Exec(ctx, "disposable-desktop-service", []string{"vmbox-runtime", "desktop-start", binding.Assignment}, provider.ExecOptions{})
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("start real desktop: %+v %v", result, err)
	}
	waitDesktopWindow(t, ctx, wrapped)

	// Pause must take effect before its acknowledgement. Input while paused is
	// rejected; resume then allows real xdotool input into the focused xterm.
	desktopAction(t, ctx, wrapped, binding.Assignment, map[string]any{"action": "pause"}, 0)
	desktopAction(t, ctx, wrapped, binding.Assignment, map[string]any{"action": "type", "text": "must-not-be-typed"}, 1)
	desktopAction(t, ctx, wrapped, binding.Assignment, map[string]any{"action": "resume"}, 0)
	containerExec("sh", "-c", `id=$(DISPLAY=:99 xdotool search --name '^vmbox managed session$' | head -n1); test -n "$id"; DISPLAY=:99 xdotool windowactivate --sync "$id"`)
	secondToken := "desktop-input-" + strings.ReplaceAll(uuid(), "-", "")
	desktopAction(t, ctx, wrapped, binding.Assignment, map[string]any{"action": "type", "text": "printf '%s\\n' '" + secondToken + "'"}, 0)
	desktopAction(t, ctx, wrapped, binding.Assignment, map[string]any{"action": "key", "keys": []string{"Return"}}, 0)
	waitContainerPane(t, ctx, containerExec, shell.ID, secondToken)

	result, err = wrapped.Exec(ctx, "disposable-desktop-service", []string{"vmbox-runtime", "desktop-screenshot", binding.Assignment}, provider.ExecOptions{})
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("capture real desktop: %+v %v", result, err)
	}
	frame, err := png.Decode(bytes.NewReader([]byte(result.Stdout)))
	if err != nil {
		t.Fatalf("invalid desktop PNG: %v", err)
	}
	if frame.Bounds().Dx() != 1280 || frame.Bounds().Dy() != 800 {
		t.Fatalf("invalid desktop PNG bounds=%v", frame.Bounds())
	}
	assertDirectWorkerScreenshotHTTP(t, ctx, tls, ownerToken, binding.BoxID)
	otherToken := uuid()
	if _, err := store.Bootstrap(ctx, "direct-desktop-other-account", "owner", otherToken); err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, tls.URL+"/v1/logical-boxes/"+binding.BoxID+"/desktop/screenshot", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+otherToken)
	response, err := tls.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("cross-account screenshot status=%d", response.StatusCode)
	}

	// Drop only the controller/agent socket. The agent process and container keep
	// running; the new peer must retain its incarnation and rediscover the exact
	// tmux session incarnation in its immediate observation.
	oldEpoch := active.Worker.Epoch
	active.Peer.Close()
	waitDirectWorkerConnect(t, ctx, connected)
	reconnected := waitDirectWorkerPeer(t, ctx, server, worker.ID, oldEpoch)
	if reconnected.Worker.Incarnation != binding.Incarnation {
		t.Fatal("socket reconnect changed agent incarnation")
	}
	observed := waitDirectWorkerObservation(t, ctx, store, worker.ID, reconnected.Worker.Epoch)
	observedSurvivor := sessionNamed(t, observed, "acceptance-shell")
	if observedSurvivor.ID != shell.ID || observedSurvivor.Incarnation != shell.Incarnation {
		t.Fatalf("reconnect observation lost session: before=%+v observed=%+v", shell, observedSurvivor)
	}
	after := directWorkerRuntimeInventory(t, ctx, wrapped, binding.Assignment)
	survivor := sessionNamed(t, after, "acceptance-shell")
	if survivor.ID != shell.ID || survivor.Incarnation != shell.Incarnation {
		t.Fatalf("session did not survive reconnect: before=%+v after=%+v", shell, survivor)
	}
	thirdToken := "terminal-after-reconnect-" + strings.ReplaceAll(uuid(), "-", "")
	directWorkerTerminalInput(t, ctx, wrapped, connection, binding.Assignment, survivor, thirdToken, containerExec)

	// Stop only the agent process. Its journal and binding stay in place and the
	// desktop/tmux container is untouched. A fresh incarnation must supersede
	// stale connection metadata while rediscovering the same runtime session.
	stopAgent()
	select {
	case err := <-agentDone:
		if err != nil && agentCtx.Err() == nil {
			t.Fatalf("first agent stopped unexpectedly: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("first agent process did not stop")
	}
	restartIncarnation, _ := secretToken()
	restartCtx, stopRestart := context.WithCancel(ctx)
	defer stopRestart()
	restartedDone := make(chan error, 1)
	restarted := &workeragent.Agent{Config: config, ConfigFile: configPath, HTTP: tls.Client(), Incarnation: restartIncarnation, Report: func(message string) {
		if message == "worker connected" {
			select {
			case connected <- struct{}{}:
			default:
			}
		}
	}}
	go func() { restartedDone <- restarted.Run(restartCtx) }()
	waitDirectWorkerConnect(t, ctx, connected)
	restartedPeer := waitDirectWorkerPeer(t, ctx, server, worker.ID, reconnected.Worker.Epoch)
	if restartedPeer.Worker.Incarnation != restartIncarnation {
		t.Fatal("agent process restart retained stale incarnation")
	}
	if _, err := wrapped.ExecConnection(ctx, connection, []string{"true"}, provider.ExecOptions{}); err == nil {
		t.Fatal("stale pre-restart connection remained executable")
	}
	restartObservation := waitDirectWorkerObservation(t, ctx, store, worker.ID, restartedPeer.Worker.Epoch)
	restartSurvivor := sessionNamed(t, restartObservation, "acceptance-shell")
	if restartSurvivor.ID != shell.ID || restartSurvivor.Incarnation != shell.Incarnation {
		t.Fatalf("agent restart observation lost session: before=%+v observed=%+v", shell, restartSurvivor)
	}
	restartConnection, err := wrapped.Connection(ctx, "disposable-desktop-service")
	if err != nil || restartConnection.Metadata["connectionRevision"] != restartIncarnation {
		t.Fatalf("fresh agent connection: %+v %v", restartConnection, err)
	}
	restartToken := "terminal-after-agent-restart-" + strings.ReplaceAll(uuid(), "-", "")
	directWorkerTerminalInput(t, ctx, wrapped, restartConnection, binding.Assignment, restartSurvivor, restartToken, containerExec)

	// Replace the controller Server while retaining its stable TLS address and
	// database. Expiring the lease directly models process death without waiting
	// 60 seconds and affects only this test's isolated schema.
	server.mu.Lock()
	oldControllerOwner := server.directWorkerOwner
	server.mu.Unlock()
	nextServer := NewServer(store, nil)
	nextServer.DirectWorkersEnabled = true
	nextServer.Resolve = func(context.Context, string, string, string) (provider.Provider, error) {
		return &noRailwayCallsProvider{}, nil
	}
	handlerMu.Lock()
	activeHandler = nextServer.Handler()
	handlerMu.Unlock()
	if _, err := store.DB.ExecContext(ctx, `UPDATE direct_worker_controller_lease SET expires_at=now()-interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	restartedPeer.Peer.Close()
	waitDirectWorkerConnect(t, ctx, connected)
	controllerPeer := waitDirectWorkerPeer(t, ctx, nextServer, worker.ID, restartedPeer.Worker.Epoch)
	if controllerPeer.Worker.Incarnation != restartIncarnation {
		t.Fatal("controller restart changed the surviving agent incarnation")
	}
	nextServer.mu.Lock()
	newControllerOwner := nextServer.directWorkerOwner
	nextServer.mu.Unlock()
	if oldControllerOwner == "" || newControllerOwner == "" || oldControllerOwner == newControllerOwner {
		t.Fatal("controller restart did not replace connection ownership")
	}
	if err := store.ClaimWorkerController(ctx, oldControllerOwner); err == nil {
		t.Fatal("old controller owner reclaimed the live lease")
	}
	controllerObservation := waitDirectWorkerObservation(t, ctx, store, worker.ID, controllerPeer.Worker.Epoch)
	controllerSurvivor := sessionNamed(t, controllerObservation, "acceptance-shell")
	if controllerSurvivor.ID != shell.ID || controllerSurvivor.Incarnation != shell.Incarnation {
		t.Fatalf("controller restart observation lost session: before=%+v observed=%+v", shell, controllerSurvivor)
	}
	nextWrapped := &directWorkerProvider{Provider: &noRailwayCallsProvider{}, server: nextServer, accountID: principal.AccountID}
	controllerConnection, err := nextWrapped.Connection(ctx, "disposable-desktop-service")
	if err != nil || controllerConnection.Metadata["connectionRevision"] != restartIncarnation {
		t.Fatalf("post-controller-restart connection: %+v %v", controllerConnection, err)
	}
	controllerToken := "terminal-after-controller-restart-" + strings.ReplaceAll(uuid(), "-", "")
	directWorkerTerminalInput(t, ctx, nextWrapped, controllerConnection, binding.Assignment, controllerSurvivor, controllerToken, containerExec)

	stopRestart()
	select {
	case <-restartedDone:
	case <-time.After(10 * time.Second):
		t.Fatal("restarted agent did not stop")
	}
}

func assertDirectWorkerScreenshotHTTP(t *testing.T, ctx context.Context, tls *httptest.Server, token, boxID string) {
	t.Helper()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, tls.URL+"/v1/logical-boxes/"+boxID+"/desktop/screenshot", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := tls.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, desktopCaptureLimit+1))
	if err != nil || response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "image/png" || response.Header.Get("X-Captured-At") == "" {
		t.Fatalf("owner screenshot status=%d type=%q captured=%q bytes=%d err=%v", response.StatusCode, response.Header.Get("Content-Type"), response.Header.Get("X-Captured-At"), len(data), err)
	}
	frame, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("owner screenshot is not the real desktop PNG: %v", err)
	}
	if frame.Bounds().Dx() != 1280 || frame.Bounds().Dy() != 800 {
		t.Fatalf("owner screenshot bounds=%v", frame.Bounds())
	}
}

func directWorkerDesktopStore(t *testing.T, ctx context.Context, dsn string) (*Store, Principal, string, string, DirectWorker, string, workerprotocol.Binding) {
	t.Helper()
	store, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	store.DB.SetMaxOpenConns(1)
	t.Cleanup(func() { store.Close() })
	schema := "direct_desktop_" + strings.ReplaceAll(uuid(), "-", "")
	if _, err = store.DB.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		store.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = store.DB.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE") })
	if _, err = store.DB.ExecContext(ctx, "SET search_path TO "+schema); err != nil {
		t.Fatal(err)
	}
	if err = store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	ownerToken := uuid()
	principal, err := store.Bootstrap(ctx, "direct-desktop-acceptance", "owner", ownerToken)
	if err != nil {
		t.Fatal(err)
	}
	slot, boxID := uuid(), uuid()
	if _, err = store.DB.ExecContext(ctx, `INSERT INTO compute_slots(id,account_id,provider,provider_credential,ordinal,state,service_id,assignment_generation,fencing_token) VALUES($1,$2,'railway','',1,'occupied','disposable-desktop-service',1,'desktop-fence')`, slot, principal.AccountID); err != nil {
		t.Fatal(err)
	}
	if _, err = store.DB.ExecContext(ctx, `INSERT INTO logical_boxes(id,account_id,owner_user_id,name,provider,provider_credential,state,volume_id,volume_name,slot_id,assignment_generation,fencing_token) VALUES($1,$2,$3,'direct-desktop','railway','','running','disposable-desktop-volume','disposable-desktop-volume',$4,1,'desktop-fence')`, boxID, principal.AccountID, principal.UserID, slot); err != nil {
		t.Fatal(err)
	}
	worker, enrollment, err := store.IssueWorkerEnrollment(ctx, principal.AccountID, slot)
	if err != nil {
		t.Fatal(err)
	}
	credential, _ := secretToken()
	if _, err = store.ExchangeWorkerEnrollment(ctx, enrollment, credential); err != nil {
		t.Fatal(err)
	}
	incarnation, _ := secretToken()
	assigned, err := store.assignment(ctx, principal.AccountID, boxID)
	if err != nil {
		t.Fatal(err)
	}
	binding := workerprotocol.Binding{AccountID: principal.AccountID, BoxID: boxID, SlotID: slot, Assignment: nativeFence(assigned), Incarnation: incarnation}
	return store, principal, ownerToken, slot, worker, credential, binding
}

func waitDirectWorkerConnect(t *testing.T, ctx context.Context, connected <-chan struct{}) {
	t.Helper()
	select {
	case <-connected:
	case <-ctx.Done():
		t.Fatal("worker agent did not connect")
	}
}

func waitDirectWorkerPeer(t *testing.T, ctx context.Context, server *Server, workerID string, afterEpoch int64) *directWorkerConnection {
	t.Helper()
	for {
		server.mu.Lock()
		active := server.directWorkers[workerID]
		server.mu.Unlock()
		if active != nil && active.Worker.Epoch > afterEpoch {
			return active
		}
		select {
		case <-ctx.Done():
			t.Fatal("controller did not register worker peer")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func waitDirectWorkerObservation(t *testing.T, ctx context.Context, store *Store, workerID string, epoch int64) v1.SessionInventory {
	t.Helper()
	for {
		var raw []byte
		err := store.DB.QueryRowContext(ctx, `SELECT observation FROM direct_workers WHERE id=$1 AND observation_epoch=$2 AND observation_sequence>0`, workerID, epoch).Scan(&raw)
		if err == nil {
			var observation workerprotocol.Observation
			var inventory v1.SessionInventory
			if json.Unmarshal(raw, &observation) == nil && json.Unmarshal(observation.Runtime, &inventory) == nil {
				return inventory
			}
		}
		select {
		case <-ctx.Done():
			t.Fatal("reconnected agent did not publish runtime inventory")
		case <-time.After(25 * time.Millisecond):
		}
	}
}

func directWorkerRuntimeInventory(t *testing.T, ctx context.Context, wrapped *directWorkerProvider, assignment string) v1.SessionInventory {
	t.Helper()
	result, err := wrapped.Exec(ctx, "disposable-desktop-service", []string{"vmbox-runtime", "native-sessions", assignment}, provider.ExecOptions{})
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("real runtime inventory: %+v %v", result, err)
	}
	var inventory v1.SessionInventory
	if err := json.Unmarshal([]byte(result.Stdout), &inventory); err != nil {
		t.Fatal(err)
	}
	return inventory
}

func sessionNamed(t *testing.T, inventory v1.SessionInventory, name string) v1.Session {
	t.Helper()
	for _, session := range inventory.Sessions {
		if session.Name == name {
			return session
		}
	}
	t.Fatalf("session %q absent from inventory: %+v", name, inventory)
	return v1.Session{}
}

func directWorkerTerminalInput(t *testing.T, ctx context.Context, wrapped *directWorkerProvider, connection provider.Connection, assignment string, session v1.Session, token string, run func(...string) []byte) {
	t.Helper()
	input, writer := io.Pipe()
	done := make(chan error, 1)
	attached := make(chan struct{}, 1)
	go func() {
		_, err := wrapped.StreamConnection(ctx, connection, []string{"vmbox-runtime", "web-terminal", assignment, session.ID, session.Incarnation}, provider.ExecOptions{Stdin: input, Stdout: terminalReadyWriter{ready: attached}, Stderr: io.Discard})
		done <- err
	}()
	select {
	case <-attached:
	case err := <-done:
		t.Fatalf("terminal viewer exited before attachment: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("terminal viewer produced no attachment output")
	}
	command := "printf '%s\\n' '" + token + "'\r"
	if err := json.NewEncoder(writer).Encode(struct {
		Data []byte `json:"data"`
	}{Data: []byte(command)}); err != nil {
		t.Fatal(err)
	}
	waitContainerPane(t, ctx, run, session.ID, token)
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("terminal viewer: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("terminal viewer did not close")
	}
}

type terminalReadyWriter struct{ ready chan<- struct{} }

func (w terminalReadyWriter) Write(data []byte) (int, error) {
	if len(data) > 0 {
		select {
		case w.ready <- struct{}{}:
		default:
		}
	}
	return len(data), nil
}

func waitContainerPane(t *testing.T, ctx context.Context, run func(...string) []byte, sessionID, token string) {
	t.Helper()
	stageCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var content []byte
	for {
		content = run("tmux", "capture-pane", "-p", "-J", "-S", "-50", "-t", sessionID)
		for _, line := range strings.Split(string(content), "\n") {
			if strings.TrimSpace(line) == token {
				return
			}
		}
		select {
		case <-stageCtx.Done():
			t.Fatalf("tmux pane never displayed standalone %q; pane=%q", token, content)
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func desktopAction(t *testing.T, ctx context.Context, wrapped *directWorkerProvider, assignment string, action any, wantCode int) {
	t.Helper()
	payload, _ := json.Marshal(action)
	result, err := wrapped.Exec(ctx, "disposable-desktop-service", []string{"vmbox-runtime", "desktop-input", assignment}, provider.ExecOptions{Stdin: bytes.NewReader(payload)})
	if err != nil || result.ExitCode != wantCode {
		t.Fatalf("desktop action %s: %+v %v", payload, result, err)
	}
}

func waitDesktopWindow(t *testing.T, ctx context.Context, wrapped *directWorkerProvider) {
	t.Helper()
	for {
		result, err := wrapped.Exec(ctx, "disposable-desktop-service", []string{"sh", "-c", "DISPLAY=:99 xdotool search --name '^vmbox managed session$'"}, provider.ExecOptions{})
		if err == nil && result.ExitCode == 0 && strings.TrimSpace(result.Stdout) != "" {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("desktop xterm did not appear")
		case <-time.After(100 * time.Millisecond):
		}
	}
}
