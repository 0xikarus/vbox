package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/0xikarus/vmbox-service/internal/browser"
	"github.com/0xikarus/vmbox-service/internal/provider"
)

func browserStateScope(account, box, id string) string {
	return account + ":browser-state:" + box + ":" + id
}

func (s *Server) browserStateImports(w http.ResponseWriter, r *http.Request, p Principal) {
	w.Header().Set("Cache-Control", "no-store")
	box, err := s.Store.LogicalBox(r.Context(), p, r.PathValue("id"))
	if err != nil {
		writeError(w, 404, fmt.Errorf("box unavailable"))
		return
	}
	if r.Method == "GET" {
		rows, err := s.Store.DB.QueryContext(r.Context(), `SELECT id::text,origins,status,created_at FROM browser_state_imports WHERE account_id=$1 AND box_id=$2 ORDER BY created_at`, p.AccountID, box.ID)
		if err != nil {
			writeError(w, 500, fmt.Errorf("imports unavailable"))
			return
		}
		defer rows.Close()
		values := []map[string]any{}
		for rows.Next() {
			var id, status string
			var origins json.RawMessage
			var created time.Time
			if rows.Scan(&id, &origins, &status, &created) != nil {
				writeError(w, 500, fmt.Errorf("imports unavailable"))
				return
			}
			values = append(values, map[string]any{"id": id, "origins": origins, "status": status, "createdAt": created})
		}
		if rows.Err() != nil {
			writeError(w, 500, fmt.Errorf("imports unavailable"))
			return
		}
		writeJSON(w, 200, values)
		return
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, browser.MaxStateBytes))
	if err != nil {
		writeError(w, 400, fmt.Errorf("browser state exceeds 1 MiB"))
		return
	}
	defer clear(data)
	state, err := browser.DecodeStateImport(data)
	if err != nil {
		writeError(w, 400, err)
		return
	}
	if s.Store.Envelope == nil {
		writeError(w, 503, fmt.Errorf("credential encryption unavailable"))
		return
	}
	id := uuid()
	sealed, err := s.Store.Envelope.Seal(browserStateScope(p.AccountID, box.ID, id), data)
	if err != nil {
		writeError(w, 500, fmt.Errorf("could not protect browser state"))
		return
	}
	origins := make([]string, 0, len(state.Origins))
	for _, site := range state.Origins {
		origins = append(origins, site.Origin)
	}
	encoded, _ := json.Marshal(origins)
	_, err = s.Store.DB.ExecContext(r.Context(), `INSERT INTO browser_state_imports(id,account_id,box_id,encrypted_value,origins) VALUES($1,$2,$3,$4,$5)`, id, p.AccountID, box.ID, sealed, encoded)
	if err != nil {
		writeError(w, 500, fmt.Errorf("could not save browser state"))
		return
	}
	writeJSON(w, 201, map[string]any{"id": id, "origins": origins, "status": "pending"})
}

func (s *Server) deleteBrowserState(w http.ResponseWriter, r *http.Request, p Principal) {
	box, err := s.Store.LogicalBox(r.Context(), p, r.PathValue("id"))
	if err != nil {
		writeError(w, 404, fmt.Errorf("box unavailable"))
		return
	}
	result, err := s.Store.DB.ExecContext(r.Context(), `DELETE FROM browser_state_imports WHERE id=$1 AND account_id=$2 AND box_id=$3`, r.PathValue("import"), p.AccountID, box.ID)
	if err != nil {
		writeError(w, 400, fmt.Errorf("could not remove import"))
		return
	}
	n, err := result.RowsAffected()
	if err != nil || n != 1 {
		writeError(w, 404, fmt.Errorf("import unavailable"))
		return
	}
	w.WriteHeader(204)
}

func (s *Server) applyBrowserState(w http.ResponseWriter, r *http.Request, p Principal) {
	w.Header().Set("Cache-Control", "no-store")
	ctx, cancel := context.WithTimeout(r.Context(), 70*time.Second)
	defer cancel()
	box, err := s.Store.LogicalBox(ctx, p, r.PathValue("id"))
	if err != nil {
		writeError(w, 404, fmt.Errorf("box unavailable"))
		return
	}
	a, err := s.Store.assignment(ctx, p.AccountID, box.ID)
	if err != nil || a.Box.State != "running" {
		writeError(w, 409, fmt.Errorf("resume the box and open Chromium first"))
		return
	}
	prov, err := s.provider(ctx, p.AccountID, box.Provider, box.ProviderCredential)
	if err != nil {
		writeError(w, 502, fmt.Errorf("worker unavailable"))
		return
	}
	if s.Store.Envelope == nil {
		writeError(w, 503, fmt.Errorf("credential encryption unavailable"))
		return
	}
	tx, err := s.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		writeError(w, 503, fmt.Errorf("import unavailable"))
		return
	}
	defer tx.Rollback()
	var locked string
	if tx.QueryRowContext(ctx, `SELECT id::text FROM logical_boxes WHERE id=$1 AND account_id=$2 AND state='running' AND fencing_token=$3 FOR UPDATE`, box.ID, p.AccountID, a.FencingToken).Scan(&locked) != nil {
		writeError(w, 409, fmt.Errorf("assignment changed"))
		return
	}
	var sealed string
	id := r.PathValue("import")
	if tx.QueryRowContext(ctx, `SELECT encrypted_value FROM browser_state_imports WHERE id=$1 AND account_id=$2 AND box_id=$3 FOR UPDATE`, id, p.AccountID, box.ID).Scan(&sealed) != nil {
		writeError(w, 404, fmt.Errorf("import unavailable"))
		return
	}
	data, err := s.Store.Envelope.Open(browserStateScope(p.AccountID, box.ID, id), sealed)
	if err != nil {
		writeError(w, 503, fmt.Errorf("import unavailable"))
		return
	}
	defer clear(data)
	result, err := prov.Exec(ctx, a.Slot.ServiceID, []string{"vmbox-runtime", "desktop-import-state", nativeFence(a)}, provider.ExecOptions{Stdin: bytes.NewReader(data), Stdout: io.Discard, Stderr: io.Discard})
	if err != nil || result.ExitCode != 0 {
		writeError(w, 409, fmt.Errorf("import incomplete; inspect browser control and destinations before retrying; some state may have been applied"))
		return
	}
	if _, err = tx.ExecContext(ctx, `UPDATE browser_state_imports SET status='applied' WHERE id=$1`, id); err != nil {
		writeError(w, 500, fmt.Errorf("import status unavailable"))
		return
	}
	if tx.Commit() != nil {
		writeError(w, 409, fmt.Errorf("import outcome uncertain; inspect the browser"))
		return
	}
	writeJSON(w, 200, map[string]string{"id": id, "status": "applied"})
}
