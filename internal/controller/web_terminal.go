package controller

import (
	"context"
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
	if conn.Metadata["deploymentInstanceId"] == "" || conn.Metadata["deploymentInstanceId"] != a.Slot.DeploymentInstanceID {
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
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				current, err := s.Store.assignment(ctx, p.AccountID, a.Box.ID)
				if err != nil || nativeFence(current) != inv.Assignment || current.Box.State != "running" {
					cancel()
					return
				}
				token, ok := authorizationValue(r.Header.Get("Authorization"), "Bearer")
				if !ok {
					token, ok = s.browserToken(r)
				}
				if !ok {
					cancel()
					return
				}
				principal, err := s.Store.Authenticate(ctx, token)
				if err != nil || principal.AccountID != p.AccountID || principal.UserID != p.UserID || principal.Role != "owner" {
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
