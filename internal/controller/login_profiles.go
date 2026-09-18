package controller

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/loginprofile"
	"time"
)

var loginProfileName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9@+_.:() -]{0,127}$`)
var errDuplicateLoginProfile = errors.New("duplicate login profile")

// Only portable profile files are accepted, never arbitrary paths or archives.
func validateLoginProfile(application, name string, req v1.SaveLoginProfileRequest) error {
	allowed := map[string]map[string]bool{
		"claude":   {".credentials.json": true, "settings.json": true, ".claude.json": true},
		"codex":    {"auth.json": true, "config.toml": true},
		"github":   {"credential.json": true},
		"opencode": {"auth.json": true, "opencode.json": true, "opencode.jsonc": true},
	}
	files, ok := allowed[application]
	if !ok || !loginProfileName.MatchString(name) {
		return fmt.Errorf("use claude, codex, opencode or github and a profile name of 1–128 safe display characters")
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
	value := v1.LoginProfile{Application: application, Name: name, Model: loginprofile.Model(application, req.Files)}
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
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return value, fmt.Errorf("could not begin profile save")
	}
	defer tx.Rollback()
	// Serialize profile writes for this account/application so two simultaneous
	// uploads cannot both pass the plaintext comparison under different names.
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, p.AccountID+":"+application); err != nil {
		return value, fmt.Errorf("could not lock profile save")
	}
	rows, err := tx.QueryContext(ctx, `SELECT name,encrypted_value FROM login_profiles WHERE account_id=$1 AND application=$2 ORDER BY name`, p.AccountID, application)
	if err != nil {
		return value, fmt.Errorf("could not compare saved profiles")
	}
	for rows.Next() {
		var existingName, existingSealed string
		if err = rows.Scan(&existingName, &existingSealed); err != nil {
			rows.Close()
			return value, fmt.Errorf("could not compare saved profiles")
		}
		existing, openErr := s.Envelope.Open(profileEncryptionScope(p.AccountID, application, existingName), existingSealed)
		if openErr != nil {
			rows.Close()
			return value, fmt.Errorf("could not compare saved profiles")
		}
		duplicate := subtle.ConstantTimeCompare(existing, plain) == 1
		clear(existing)
		if duplicate {
			rows.Close()
			return value, fmt.Errorf("%w: identical %s credentials and configuration are already saved as %q", errDuplicateLoginProfile, application, existingName)
		}
	}
	if err = rows.Close(); err != nil {
		return value, fmt.Errorf("could not compare saved profiles")
	}
	if err = rows.Err(); err != nil {
		return value, fmt.Errorf("could not compare saved profiles")
	}
	sealed, err := s.Envelope.Seal(profileEncryptionScope(p.AccountID, application, name), plain)
	if err != nil {
		return value, fmt.Errorf("could not encrypt profile")
	}
	err = tx.QueryRowContext(ctx, `INSERT INTO login_profiles(account_id,application,name,encrypted_value) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING RETURNING created_at`, p.AccountID, application, name, sealed).Scan(&value.CreatedAt)
	if err != nil {
		return value, err
	}
	if err = tx.Commit(); err != nil {
		return value, fmt.Errorf("could not commit profile save")
	}
	return value, nil
}

func (s *Store) ListLoginProfiles(ctx context.Context, p Principal) ([]v1.LoginProfile, error) {
	if s.Envelope == nil {
		return nil, fmt.Errorf("credential encryption unavailable")
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT application,name,encrypted_value,created_at FROM login_profiles WHERE account_id=$1 ORDER BY application,name`, p.AccountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []v1.LoginProfile{}
	for rows.Next() {
		var v v1.LoginProfile
		var sealed string
		if err := rows.Scan(&v.Application, &v.Name, &sealed, &v.CreatedAt); err != nil {
			return nil, err
		}
		plain, err := s.Envelope.Open(profileEncryptionScope(p.AccountID, v.Application, v.Name), sealed)
		if err != nil {
			return nil, fmt.Errorf("could not read saved profile metadata")
		}
		var profile v1.SaveLoginProfileRequest
		if json.Unmarshal(plain, &profile) == nil {
			v.Model = loginprofile.Model(v.Application, profile.Files)
		}
		for _, data := range profile.Files {
			clear(data)
		}
		clear(plain)
		values = append(values, v)
	}
	return values, rows.Err()
}

// LoadLoginProfile is controller-internal: no endpoint exports plaintext profiles.
func (s *Store) LoadLoginProfile(ctx context.Context, p Principal, application, name string) (v1.SaveLoginProfileRequest, error) {
	return s.loadLoginProfile(ctx, s.DB, p, application, name)
}

type loginProfileRowQuerier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func (s *Store) loadLoginProfile(ctx context.Context, query loginProfileRowQuerier, p Principal, application, name string) (v1.SaveLoginProfileRequest, error) {
	var value v1.SaveLoginProfileRequest
	if s.Envelope == nil {
		return value, fmt.Errorf("credential encryption unavailable")
	}
	var sealed string
	if err := query.QueryRowContext(ctx, `SELECT encrypted_value FROM login_profiles WHERE account_id=$1 AND application=$2 AND name=$3`, p.AccountID, application, name).Scan(&sealed); err != nil {
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

func (s *Server) deleteLoginProfile(w http.ResponseWriter, r *http.Request, p Principal) {
	w.Header().Set("Cache-Control", "no-store")
	result, err := s.Store.DB.ExecContext(r.Context(), `DELETE FROM login_profiles WHERE account_id=$1 AND application=$2 AND name=$3`, p.AccountID, r.PathValue("application"), r.PathValue("name"))
	if err != nil {
		writeError(w, 500, fmt.Errorf("could not delete login profile"))
		return
	}
	n, err := result.RowsAffected()
	if err != nil {
		writeError(w, 500, fmt.Errorf("could not confirm profile deletion"))
		return
	}
	if n == 0 {
		writeError(w, 404, fmt.Errorf("login profile not found"))
		return
	}
	w.WriteHeader(http.StatusNoContent)
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
		if errors.Is(err, errDuplicateLoginProfile) {
			writeError(w, 409, err)
			return
		}
		writeError(w, 409, fmt.Errorf("could not save profile; choose a new name if it already exists, or check controller storage/encryption"))
		return
	}
	writeJSON(w, 201, value)
}
