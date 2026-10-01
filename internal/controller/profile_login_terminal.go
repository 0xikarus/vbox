package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/coder/websocket"
	"github.com/creack/pty"
)

// The browser sees the actual login command in a short-lived terminal. It is
// never given a shell, executable selector, credential file, or controller env.
func (s *Server) browserProfileLoginTerminal(w http.ResponseWriter, r *http.Request, p Principal) {
	if !s.browserOrigin(r) {
		writeError(w, http.StatusForbidden, fmt.Errorf("same-origin login terminal required"))
		return
	}
	session := s.profileLogins.get(r.PathValue("id"), p)
	if session == nil {
		writeError(w, http.StatusNotFound, fmt.Errorf("login session not found"))
		return
	}
	session.mu.Lock()
	if session.pty == nil || (session.status != "starting" && session.status != "waiting") {
		session.mu.Unlock()
		writeError(w, http.StatusConflict, fmt.Errorf("login terminal is no longer running"))
		return
	}
	session.mu.Unlock()
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	defer ws.CloseNow()
	ws.SetReadLimit(8192)
	ctx, cancel := context.WithTimeout(r.Context(), profileLoginLifetime+time.Minute)
	defer cancel()
	updates := make(chan []byte, 64)
	session.mu.Lock()
	if session.pty == nil || (session.status != "starting" && session.status != "waiting") {
		session.mu.Unlock()
		_ = ws.Close(websocket.StatusNormalClosure, "Login finished")
		return
	}
	backlog := append([]byte(nil), session.terminalOutput...)
	if session.watchers == nil {
		session.watchers = make(map[chan []byte]struct{})
	}
	session.watchers[updates] = struct{}{}
	session.mu.Unlock()
	defer func() {
		session.mu.Lock()
		if _, ok := session.watchers[updates]; ok {
			delete(session.watchers, updates)
			close(updates)
		}
		session.mu.Unlock()
	}()
	go func() {
		defer cancel()
		if len(backlog) > 0 {
			writeCtx, stop := context.WithTimeout(ctx, 5*time.Second)
			err := ws.Write(writeCtx, websocket.MessageBinary, backlog)
			stop()
			if err != nil {
				return
			}
		}
		for {
			select {
			case <-ctx.Done():
				return
			case data, ok := <-updates:
				if !ok {
					return
				}
				writeCtx, stop := context.WithTimeout(ctx, 5*time.Second)
				err := ws.Write(writeCtx, websocket.MessageBinary, data)
				stop()
				if err != nil {
					return
				}
			}
		}
	}()
	for {
		kind, data, err := ws.Read(ctx)
		if err != nil {
			return
		}
		session.mu.Lock()
		terminal := session.pty
		active := session.status == "starting" || session.status == "waiting"
		session.mu.Unlock()
		if terminal == nil || !active {
			return
		}
		if kind == websocket.MessageBinary {
			if len(data) > 4096 {
				return
			}
			if _, err := terminal.Write(data); err != nil {
				return
			}
			continue
		}
		if kind != websocket.MessageText || len(data) > 128 {
			return
		}
		var resize struct {
			Cols uint16 `json:"cols"`
			Rows uint16 `json:"rows"`
		}
		if json.Unmarshal(data, &resize) != nil || resize.Cols < 20 || resize.Cols > 300 || resize.Rows < 8 || resize.Rows > 120 {
			return
		}
		if pty.Setsize(terminal, &pty.Winsize{Cols: resize.Cols, Rows: resize.Rows}) != nil {
			return
		}
	}
}
