package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/loginprofile"
	"time"
)

var loginProfileName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,63}$`)

// Only portable profile files are accepted, never arbitrary paths or archives.
func validateLoginProfile(application, name string, req v1.SaveLoginProfileRequest) error {
	allowed := map[string]map[string]bool{
		"claude": {".credentials.json": true, "settings.json": true, ".claude.json": true},
		"codex":  {"auth.json": true, "config.toml": true},
		"github": {"credential.json": true},
	}
	files, ok := allowed[application]
	if !ok || !loginProfileName.MatchString(name) {
		return fmt.Errorf("use claude, codex or github and a profile name of 1–64 letters, digits, dots, underscores or hyphens")
	}
	if len(req.Files) == 0 {
		return fmt.Errorf("profile contains no files")
	}
	total := 0
	for name, data := range req.Files {
		if !files[name] || len(data) == 0 {
			return fmt.Errorf("profile contains an unsupported or empty file")
		}
		total += len(data)
	}
	if total > 512*1024 {
		return fmt.Errorf("profile exceeds 512 KiB")
	}
	return nil
}

func profileEncryptionScope(account, application, name string) string {
	return account + ":login-profile:" + application + ":" + name
}

func (s *Store) SaveLoginProfile(ctx context.Context, p Principal, application, name string, req v1.SaveLoginProfileRequest) (v1.LoginProfile, error) {
	value := v1.LoginProfile{Application: application, Name: name}
	if err := validateLoginProfile(application, name, req); err != nil {
		return value, err
	}
	if err := loginprofile.Validate(application, req.Files, time.Now()); err != nil {
		return value, err
	}
	if s.Envelope == nil {
		return value, fmt.Errorf("credential encryption unavailable")
	}
	plain, err := json.Marshal(req)
	if err != nil {
		return value, fmt.Errorf("could not encode profile")
	}
	defer clear(plain)
	sealed, err := s.Envelope.Seal(profileEncryptionScope(p.AccountID, application, name), plain)
	if err != nil {
		return value, fmt.Errorf("could not encrypt profile")
	}
	err = s.DB.QueryRowContext(ctx, `INSERT INTO login_profiles(account_id,application,name,encrypted_value) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING RETURNING created_at`, p.AccountID, application, name, sealed).Scan(&value.CreatedAt)
	return value, err
}

func (s *Store) ListLoginProfiles(ctx context.Context, p Principal) ([]v1.LoginProfile, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT application,name,created_at FROM login_profiles WHERE account_id=$1 ORDER BY application,name`, p.AccountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []v1.LoginProfile{}
	for rows.Next() {
		var v v1.LoginProfile
		if err := rows.Scan(&v.Application, &v.Name, &v.CreatedAt); err != nil {
			return nil, err
		}
		values = append(values, v)
	}
	return values, rows.Err()
}

// LoadLoginProfile is controller-internal: no endpoint exports plaintext profiles.
func (s *Store) LoadLoginProfile(ctx context.Context, p Principal, application, name string) (v1.SaveLoginProfileRequest, error) {
	var value v1.SaveLoginProfileRequest
	if s.Envelope == nil {
		return value, fmt.Errorf("credential encryption unavailable")
	}
	var sealed string
	if err := s.DB.QueryRowContext(ctx, `SELECT encrypted_value FROM login_profiles WHERE account_id=$1 AND application=$2 AND name=$3`, p.AccountID, application, name).Scan(&sealed); err != nil {
		return value, err
	}
	plain, err := s.Envelope.Open(profileEncryptionScope(p.AccountID, application, name), sealed)
	if err != nil {
		return value, fmt.Errorf("could not decrypt profile")
	}
	defer clear(plain)
	if json.Unmarshal(plain, &value) != nil {
		return value, fmt.Errorf("invalid stored profile")
	}
	return value, validateLoginProfile(application, name, value)
}

func (s *Server) listLoginProfiles(w http.ResponseWriter, r *http.Request, p Principal) {
	w.Header().Set("Cache-Control", "no-store")
	values, err := s.Store.ListLoginProfiles(r.Context(), p)
	if err != nil {
		writeError(w, 500, fmt.Errorf("could not list login profiles"))
		return
	}
	writeJSON(w, 200, values)
}

func (s *Server) saveLoginProfile(w http.ResponseWriter, r *http.Request, p Principal) {
	w.Header().Set("Cache-Control", "no-store")
	r.Body = http.MaxBytesReader(w, r.Body, 768*1024)
	var req v1.SaveLoginProfileRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, 400, fmt.Errorf("invalid login profile payload"))
		return
	}
	defer func() {
		for _, data := range req.Files {
			clear(data)
		}
	}()
	app, name := r.PathValue("application"), r.PathValue("name")
	if err := validateLoginProfile(app, name, req); err != nil {
		writeError(w, 400, err)
		return
	}
	if err := loginprofile.Validate(app, req.Files, time.Now()); err != nil {
		writeError(w, 400, err)
		return
	}
	value, err := s.Store.SaveLoginProfile(r.Context(), p, app, name, req)
	if err != nil {
		writeError(w, 409, fmt.Errorf("could not save profile; choose a new name if it already exists, or check controller storage/encryption"))
		return
	}
	writeJSON(w, 201, value)
}
