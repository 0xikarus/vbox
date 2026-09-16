package controller

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/0xikarus/vmbox-service/internal/provider"
	"github.com/0xikarus/vmbox-service/internal/workerprotocol"
	"github.com/coder/websocket"
)

// This owner-only endpoint carries a single operation. It neither accepts a
// client-selected provider endpoint nor provisions compute for a sleeping box.
func (s *Server) directWorkerClient(w http.ResponseWriter, r *http.Request, principal Principal) {
	token, bearer := authorizationValue(r.Header.Get("Authorization"), "Bearer")
	if !bearer || r.Header.Get("Origin") != "" {
		writeError(w, 403, errors.New("authenticated CLI connection required"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Hour)
	defer cancel()
	a, err := s.Store.assignment(ctx, principal.AccountID, r.PathValue("id"))
	if err != nil || a.Box.State != "running" {
		writeError(w, 409, errors.New("running box assignment required"))
		return
	}
	prov, err := s.provider(ctx, principal.AccountID, a.Box.Provider, a.Box.ProviderCredential)
	if err != nil {
		writeError(w, 502, errors.New("worker provider unavailable"))
		return
	}
	direct, ok := prov.(provider.ConnectionStreamer)
	if !ok {
		writeError(w, 409, errors.New("direct worker transport unavailable"))
		return
	}
	conn, err := prov.Connection(ctx, a.Slot.ServiceID)
	if err != nil || (conn.Transport != directWorkerTransport && conn.Transport != "shared-worker") || !connectionMatchesAssignment(conn, a) {
		writeError(w, 409, errors.New("worker connection unavailable or changed"))
		return
	}
	s.mu.Lock()
	if s.webStreams >= 32 {
		s.mu.Unlock()
		writeError(w, 503, errors.New("worker stream capacity reached"))
		return
	}
	s.webStreams++
	s.mu.Unlock()
	defer func() { s.mu.Lock(); s.webStreams--; s.mu.Unlock() }()
	ws, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	peer := workerprotocol.New(ctx, ws, false)
	defer peer.Close()
	openCtx, stopOpen := context.WithTimeout(ctx, 10*time.Second)
	stream, err := peer.Accept(openCtx)
	stopOpen()
	if err != nil {
		return
	}
	encoder := json.NewEncoder(stream)
	if stream.Request.Rebind != nil || stream.Request.Binding != bindingForConnection(clientWorkerConnection(conn, a)) {
		_ = encoder.Encode(workerprotocol.Output{Kind: "error", Error: "worker assignment changed"})
		_ = stream.CloseWrite()
		// Let the client consume the error before closing the connection.
		select {
		case <-peer.Done():
		case <-ctx.Done():
		case <-time.After(10 * time.Second):
		}
		return
	}
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-peer.Done():
				cancel()
				return
			case <-stream.Done():
				cancel()
				return
			case <-ticker.C:
				p, err := s.Store.Authenticate(ctx, token)
				current, assignmentErr := s.Store.assignment(ctx, principal.AccountID, a.Box.ID)
				if err != nil || p.AccountID != principal.AccountID || p.UserID != principal.UserID || p.Role != "owner" || assignmentErr != nil || current.Box.State != "running" || nativeFence(current) != nativeFence(a) {
					cancel()
					return
				}
			}
		}
	}()
	// direct.execute serializes stdout/stderr through ReadOutput, so both writers
	// share this encoder without concurrent calls. No output is accumulated here.
	result, err := direct.StreamConnection(ctx, conn, stream.Request.Argv, provider.ExecOptions{Stdin: stream, Stdout: workerClientOutput{encoder, "stdout"}, Stderr: workerClientOutput{encoder, "stderr"}})
	if err != nil {
		_ = encoder.Encode(workerprotocol.Output{Kind: "error", Error: "worker operation interrupted; input was not replayed"})
	} else {
		_ = encoder.Encode(workerprotocol.Output{Kind: "exit", ExitCode: &result.ExitCode})
	}
	_ = stream.CloseWrite()
	select {
	case <-peer.Done():
	case <-ctx.Done():
	case <-time.After(10 * time.Second):
	}
}

type workerClientOutput struct {
	encoder *json.Encoder
	kind    string
}

func (w workerClientOutput) Write(data []byte) (int, error) {
	n := len(data)
	for len(data) > 0 {
		size := min(len(data), 32*1024)
		if err := w.encoder.Encode(workerprotocol.Output{Kind: w.kind, Data: data[:size]}); err != nil {
			return n - len(data), err
		}
		data = data[size:]
	}
	return n, nil
}
