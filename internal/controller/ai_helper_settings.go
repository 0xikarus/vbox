package controller

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
)

const defaultAIHelperModel = "openrouter/auto"

var aiHelperModelPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._:/~-]{0,199}$`)

type aiHelperOpenRouterSetting struct {
	Key   string `json:"key"`
	Model string `json:"model"`
}

func aiHelperKeyScope(accountID string) string { return accountID + ":ai-helper:openrouter" }

func validateAIHelperSetting(setting *aiHelperOpenRouterSetting) error {
	setting.Model = strings.TrimSpace(setting.Model)
	if setting.Model == "" {
		setting.Model = defaultAIHelperModel
	}
	if strings.HasPrefix(setting.Model, "openrouter/") && setting.Model != defaultAIHelperModel {
		setting.Model = strings.TrimPrefix(setting.Model, "openrouter/")
	}
	if !aiHelperModelPattern.MatchString(setting.Model) {
		return fmt.Errorf("enter a valid OpenRouter model ID")
	}
	if setting.Key != "" && (len(setting.Key) < 8 || len(setting.Key) > 512 || strings.IndexFunc(setting.Key, func(r rune) bool { return r < '!' || r > '~' }) >= 0) {
		return fmt.Errorf("enter an OpenRouter API key without spaces")
	}
	return nil
}

func (s *Store) AIHelperOpenRouter(ctx context.Context, p Principal) (aiHelperOpenRouterSetting, error) {
	var setting aiHelperOpenRouterSetting
	if s.Envelope == nil {
		return setting, fmt.Errorf("credential encryption unavailable")
	}
	var sealed string
	err := s.DB.QueryRowContext(ctx, `SELECT encrypted_key,model FROM ai_helper_settings WHERE account_id=$1`, p.AccountID).Scan(&sealed, &setting.Model)
	if err != nil {
		return setting, err
	}
	plain, err := s.Envelope.Open(aiHelperKeyScope(p.AccountID), sealed)
	if err != nil {
		return aiHelperOpenRouterSetting{}, fmt.Errorf("could not decrypt AI helper key")
	}
	setting.Key = string(plain)
	clear(plain)
	return setting, nil
}

func (s *Store) SaveAIHelperOpenRouter(ctx context.Context, p Principal, setting aiHelperOpenRouterSetting) error {
	if err := validateAIHelperSetting(&setting); err != nil {
		return err
	}
	if s.Envelope == nil {
		return fmt.Errorf("credential encryption unavailable")
	}
	if setting.Key == "" {
		result, err := s.DB.ExecContext(ctx, `UPDATE ai_helper_settings SET model=$2,updated_at=now() WHERE account_id=$1`, p.AccountID, setting.Model)
		if err != nil {
			return err
		}
		count, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if count == 0 {
			return sql.ErrNoRows
		}
		return nil
	}
	plain := []byte(setting.Key)
	defer clear(plain)
	sealed, err := s.Envelope.Seal(aiHelperKeyScope(p.AccountID), plain)
	if err != nil {
		return fmt.Errorf("could not encrypt AI helper key")
	}
	_, err = s.DB.ExecContext(ctx, `INSERT INTO ai_helper_settings(account_id,encrypted_key,model) VALUES($1,$2,$3)
		ON CONFLICT(account_id) DO UPDATE SET encrypted_key=EXCLUDED.encrypted_key,model=EXCLUDED.model,updated_at=now()`, p.AccountID, sealed, setting.Model)
	return err
}

func (s *Server) getAIHelperOpenRouter(w http.ResponseWriter, r *http.Request, p Principal) {
	w.Header().Set("Cache-Control", "no-store")
	setting, err := s.Store.AIHelperOpenRouter(r.Context(), p)
	if errors.Is(err, sql.ErrNoRows) {
		writeJSON(w, http.StatusOK, map[string]any{"configured": false, "model": defaultAIHelperModel})
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("could not read AI helper setting"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"configured": true, "key": setting.Key, "model": setting.Model})
}

func (s *Server) putAIHelperOpenRouter(w http.ResponseWriter, r *http.Request, p Principal) {
	w.Header().Set("Cache-Control", "no-store")
	r.Body = http.MaxBytesReader(w, r.Body, 2<<10)
	var setting aiHelperOpenRouterSetting
	if err := decodeJSON(r, &setting); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid AI helper setting"))
		return
	}
	if err := validateAIHelperSetting(&setting); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := s.Store.SaveAIHelperOpenRouter(r.Context(), p, setting); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusBadRequest, fmt.Errorf("enter an OpenRouter API key first"))
			return
		}
		writeError(w, http.StatusInternalServerError, fmt.Errorf("could not save AI helper setting"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"configured": true, "model": setting.Model})
}

func (s *Server) deleteAIHelperOpenRouter(w http.ResponseWriter, r *http.Request, p Principal) {
	w.Header().Set("Cache-Control", "no-store")
	if _, err := s.Store.DB.ExecContext(r.Context(), `DELETE FROM ai_helper_settings WHERE account_id=$1`, p.AccountID); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("could not remove AI helper setting"))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
