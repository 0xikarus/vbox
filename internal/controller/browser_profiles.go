package controller

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/browser"
)

func browserProfileScope(account, name string) string {
	return account + ":browser-profile:" + name
}

func validBrowserProfileSource(browserName, sourcePath string) bool {
	if browserName != "chrome" && browserName != "chromium" {
		return false
	}
	if sourcePath == "" || len(sourcePath) > 2048 {
		return false
	}
	for _, r := range sourcePath {
		if r < 32 || r == 127 {
			return false
		}
	}
	return true
}

func (s *Server) saveBrowserProfile(w http.ResponseWriter, r *http.Request, p Principal) {
	w.Header().Set("Cache-Control", "no-store")
	r.Body = http.MaxBytesReader(w, r.Body, browser.MaxStateBytes+64*1024)
	var req v1.SaveBrowserProfileRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, 400, fmt.Errorf("invalid browser profile payload"))
		return
	}
	defer clear(req.State)
	name := r.PathValue("name")
	if !loginProfileName.MatchString(name) || !validBrowserProfileSource(req.SourceBrowser, req.SourcePath) {
		writeError(w, 400, fmt.Errorf("invalid browser profile name or source"))
		return
	}
	state, err := browser.DecodeStateImport(req.State)
	if err != nil {
		writeError(w, 400, err)
		return
	}
	if s.Store.Envelope == nil {
		writeError(w, 503, fmt.Errorf("credential encryption unavailable"))
		return
	}
	origins := make([]string, 0, len(state.Origins))
	for _, site := range state.Origins {
		origins = append(origins, site.Origin)
	}
	encoded, _ := json.Marshal(origins)
	sealed, err := s.Store.Envelope.Seal(browserProfileScope(p.AccountID, name), req.State)
	if err != nil {
		writeError(w, 500, fmt.Errorf("could not protect browser profile"))
		return
	}
	var info v1.BrowserProfile
	info.Name, info.SourceBrowser, info.SourcePath, info.Origins = name, req.SourceBrowser, req.SourcePath, origins
	if req.ReplaceExisting {
		err = s.Store.DB.QueryRowContext(r.Context(), `UPDATE browser_profiles SET source_browser=$3,source_path=$4,encrypted_value=$5,origins=$6,updated_at=now() WHERE account_id=$1 AND name=$2 RETURNING created_at,updated_at`, p.AccountID, name, req.SourceBrowser, req.SourcePath, sealed, encoded).Scan(&info.CreatedAt, &info.UpdatedAt)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				writeError(w, 404, fmt.Errorf("saved browser profile unavailable"))
			} else {
				writeError(w, 500, fmt.Errorf("could not replace browser profile"))
			}
			return
		}
		writeJSON(w, 200, info)
		return
	}
	err = s.Store.DB.QueryRowContext(r.Context(), `INSERT INTO browser_profiles(account_id,name,source_browser,source_path,encrypted_value,origins) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT DO NOTHING RETURNING created_at,updated_at`, p.AccountID, name, req.SourceBrowser, req.SourcePath, sealed, encoded).Scan(&info.CreatedAt, &info.UpdatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, 409, fmt.Errorf("browser profile name already saved"))
		} else {
			writeError(w, 500, fmt.Errorf("could not save browser profile"))
		}
		return
	}
	writeJSON(w, 201, info)
}

func (s *Server) listBrowserProfiles(w http.ResponseWriter, r *http.Request, p Principal) {
	w.Header().Set("Cache-Control", "no-store")
	rows, err := s.Store.DB.QueryContext(r.Context(), `SELECT name,source_browser,source_path,origins,created_at,updated_at FROM browser_profiles WHERE account_id=$1 ORDER BY name`, p.AccountID)
	if err != nil {
		writeError(w, 500, fmt.Errorf("browser profiles unavailable"))
		return
	}
	defer rows.Close()
	values := []v1.BrowserProfile{}
	for rows.Next() {
		var info v1.BrowserProfile
		var origins []byte
		if rows.Scan(&info.Name, &info.SourceBrowser, &info.SourcePath, &origins, &info.CreatedAt, &info.UpdatedAt) != nil || json.Unmarshal(origins, &info.Origins) != nil {
			writeError(w, 500, fmt.Errorf("browser profiles unavailable"))
			return
		}
		values = append(values, info)
	}
	if rows.Err() != nil {
		writeError(w, 500, fmt.Errorf("browser profiles unavailable"))
		return
	}
	writeJSON(w, 200, values)
}

func (s *Server) deleteBrowserProfile(w http.ResponseWriter, r *http.Request, p Principal) {
	w.Header().Set("Cache-Control", "no-store")
	result, err := s.Store.DB.ExecContext(r.Context(), `DELETE FROM browser_profiles WHERE account_id=$1 AND name=$2`, p.AccountID, r.PathValue("name"))
	if err != nil {
		writeError(w, 500, fmt.Errorf("could not delete browser profile"))
		return
	}
	n, err := result.RowsAffected()
	if err != nil {
		writeError(w, 500, fmt.Errorf("could not confirm browser profile deletion"))
		return
	}
	if n == 0 {
		writeError(w, 404, fmt.Errorf("browser profile unavailable"))
		return
	}
	w.WriteHeader(204)
}

func (s *Server) attachBrowserProfile(w http.ResponseWriter, r *http.Request, p Principal) {
	w.Header().Set("Cache-Control", "no-store")
	box, err := s.Store.LogicalBox(r.Context(), p, r.PathValue("id"))
	if err != nil {
		writeError(w, 404, fmt.Errorf("box unavailable"))
		return
	}
	if s.Store.Envelope == nil {
		writeError(w, 503, fmt.Errorf("credential encryption unavailable"))
		return
	}
	name := r.PathValue("name")
	var sealed string
	if err := s.Store.DB.QueryRowContext(r.Context(), `SELECT encrypted_value FROM browser_profiles WHERE account_id=$1 AND name=$2`, p.AccountID, name).Scan(&sealed); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, 404, fmt.Errorf("browser profile unavailable"))
		} else {
			writeError(w, 500, fmt.Errorf("browser profile unavailable"))
		}
		return
	}
	data, err := s.Store.Envelope.Open(browserProfileScope(p.AccountID, name), sealed)
	if err != nil {
		writeError(w, 503, fmt.Errorf("browser profile unavailable"))
		return
	}
	defer clear(data)
	state, err := browser.DecodeStateImport(data)
	if err != nil {
		writeError(w, 503, fmt.Errorf("browser profile unavailable"))
		return
	}
	id := uuid()
	sealed, err = s.Store.Envelope.Seal(browserStateScope(p.AccountID, box.ID, id), data)
	if err != nil {
		writeError(w, 500, fmt.Errorf("could not protect browser import"))
		return
	}
	origins := make([]string, 0, len(state.Origins))
	for _, site := range state.Origins {
		origins = append(origins, site.Origin)
	}
	encoded, _ := json.Marshal(origins)
	_, err = s.Store.DB.ExecContext(r.Context(), `INSERT INTO browser_state_imports(id,account_id,box_id,encrypted_value,origins,source_name) VALUES($1,$2,$3,$4,$5,$6)`, id, p.AccountID, box.ID, sealed, encoded, name)
	if err != nil {
		writeError(w, 500, fmt.Errorf("could not attach browser profile"))
		return
	}
	writeJSON(w, 201, map[string]any{"id": id, "name": name, "origins": origins, "status": "pending"})
}
