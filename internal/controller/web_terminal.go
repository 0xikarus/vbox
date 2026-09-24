package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/0xikarus/vmbox-service/internal/provider"
	"github.com/coder/websocket"
)

type terminalSocketWriter struct {
	ctx context.Context
	ws  *websocket.Conn
}

func (w terminalSocketWriter) Write(data []byte) (int, error) {
	ctx, cancel := context.WithTimeout(w.ctx, 15*time.Second)
	defer cancel()
	if err := w.ws.Write(ctx, websocket.MessageBinary, data); err != nil {
		return 0, err
	}
	return len(data), nil
}

func (s *Server) webTerminal(w http.ResponseWriter, r *http.Request, p Principal) {
	s.workspaceStream(w, r, p, false)
}
func (s *Server) webDesktop(w http.ResponseWriter, r *http.Request, p Principal) {
	s.workspaceStream(w, r, p, true)
}
func (s *Server) workspaceStream(w http.ResponseWriter, r *http.Request, p Principal, desktop bool) {
	if !s.browserOrigin(r) {
		writeError(w, 403, fmt.Errorf("same-origin workspace required"))
		return
	}
	s.mu.Lock()
	if s.webStreams >= 32 {
		s.mu.Unlock()
		writeError(w, 503, fmt.Errorf("workspace stream capacity reached"))
		return
	}
	s.webStreams++
	s.mu.Unlock()
	defer func() { s.mu.Lock(); s.webStreams--; s.mu.Unlock() }()
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Hour)
	defer cancel()
	inv, err := s.observeSessions(ctx, p, r.PathValue("id"))
	if err != nil {
		writeError(w, 502, err)
		return
	}
	id, incarnation := "", ""
	for _, session := range inv.Sessions {
		if session.Name == r.URL.Query().Get("session") {
			id, incarnation = session.ID, session.Incarnation
			break
		}
	}
	if id == "" && !desktop {
		writeError(w, 409, fmt.Errorf("selected session is not live"))
		return
	}
	a, err := s.Store.assignment(ctx, p.AccountID, inv.LogicalBoxID)
	if err != nil || nativeFence(a) != inv.Assignment {
		writeError(w, 409, fmt.Errorf("assignment changed"))
		return
	}
	prov, err := s.provider(ctx, p.AccountID, a.Box.Provider, a.Box.ProviderCredential)
	if err != nil {
		writeError(w, 502, err)
		return
	}
	executor, ok := prov.(provider.ConnectionStreamer)
	if !ok {
		writeError(w, 409, fmt.Errorf("provider does not support fenced workspace streaming"))
		return
	}
	conn, err := prov.Connection(ctx, a.Slot.ServiceID)
	if err != nil {
		writeError(w, 502, fmt.Errorf("connection resolution failed"))
		return
	}
	if !connectionMatchesAssignment(conn, a) {
		writeError(w, 409, fmt.Errorf("deployment changed"))
		return
	}
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	} // Origin checked above against configured public URL.
	defer ws.CloseNow()
	ws.SetReadLimit(128 * 1024)
	input, writer, err := workspaceInput()
	if err != nil {
		_ = ws.Close(websocket.StatusInternalError, "Could not open terminal transport")
		return
	}
	defer input.Close()
	defer writer.Close()
	go func() {
		defer cancel()
		defer writer.Close()
		for {
			kind, data, err := ws.Read(ctx)
			if err != nil {
				return
			}
			if desktop && kind != websocket.MessageBinary || !desktop && kind != websocket.MessageText {
				return
			}
			if !desktop {
				// Keep terminal latency probes on this WebSocket. They must not
				// reach the worker's terminal input stream.
				if len(data) <= 64 && bytes.HasPrefix(data, []byte(`{"probe":"`)) {
					var probe struct {
						Probe string `json:"probe"`
					}
					if json.Unmarshal(data, &probe) == nil && probe.Probe != "" && len(probe.Probe) <= 32 {
						response, _ := json.Marshal(probe)
						writeCtx, stop := context.WithTimeout(ctx, 5*time.Second)
						err = ws.Write(writeCtx, websocket.MessageText, response)
						stop()
						if err != nil {
							return
						}
						continue
					}
				}
				data = append(data, '\n')
			}
			if _, err = writer.Write(data); err != nil {
				return
			}
		}
	}()
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		var flakySince time.Time
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if !s.workspaceStreamAlive(ctx, r, p, inv.Assignment, a, &flakySince) {
					cancel()
					return
				}
			}
		}
	}()
	command := []string{"vmbox-runtime", "web-terminal", inv.Assignment, id, incarnation}
	if desktop {
		command = []string{"vmbox-runtime", "desktop-stream", inv.Assignment}
	}
	result, err := executor.StreamConnection(ctx, conn, command, provider.ExecOptions{Stdin: input, Stdout: terminalSocketWriter{ctx, ws}, Stderr: io.Discard})
	if err != nil || result.ExitCode != 0 {
		_ = ws.Close(websocket.StatusInternalError, "Terminal stream ended; reconnect to return to your session")
		return
	}
	_ = ws.Close(websocket.StatusNormalClosure, "Terminal detached")
}

// revalidationTolerated reports whether one more transient store failure may be
// tolerated inside streamRevalidationGrace. A healthy revalidation resets the
// window; a persistent fault eventually fails closed.
func revalidationTolerated(flaky *time.Time) bool {
	if flaky.IsZero() {
		*flaky = time.Now()
		return true
	}
	return time.Since(*flaky) <= streamRevalidationGrace
}

// workspaceStreamAlive rechecks one live viewer stream. Transient store failures
// must not disconnect a healthy viewer: they are tolerated for a bounded grace
// window, then fail closed. A genuine assignment change, stopped box, or
// revoked/expired token ends the stream on the first observation.
func (s *Server) workspaceStreamAlive(ctx context.Context, r *http.Request, p Principal, fence string, a fleetAssignment, flaky *time.Time) bool {
	current, err := s.Store.assignment(ctx, p.AccountID, a.Box.ID)
	if err != nil {
		return !errors.Is(err, errLogicalBoxMissing) && revalidationTolerated(flaky)
	}
	*flaky = time.Time{}
	if nativeFence(current) != fence || current.Box.State != "running" {
		return false
	}
	token, ok := authorizationValue(r.Header.Get("Authorization"), "Bearer")
	if !ok {
		token, ok = s.browserToken(r)
	}
	if !ok {
		return false
	}
	principal, err := s.Store.Authenticate(ctx, token)
	if err != nil {
		return !errors.Is(err, errInvalidBearerToken) && revalidationTolerated(flaky)
	}
	*flaky = time.Time{}
	return principal.AccountID == p.AccountID && principal.UserID == p.UserID && principal.Role == "owner"
}
