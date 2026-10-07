package controller

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// CaptchaSolverSetting is the account-level toggle for automatic captcha
// solving. The API key itself never leaves the store except to the solver
// client; status responses only report whether one is saved.
type CaptchaSolverSetting struct {
	Provider   string    `json:"provider"`
	Enabled    bool      `json:"enabled"`
	Configured bool      `json:"configured"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

var captchaSolverAPIKeyPattern = regexp.MustCompile(`^[a-zA-Z0-9]{8,128}$`)

// CaptchaSolver returns the account's setting and, separately, the decrypted
// API key. The key is empty when no credential is stored.
func (s *Store) CaptchaSolver(ctx context.Context, accountID string) (CaptchaSolverSetting, string, error) {
	setting := CaptchaSolverSetting{Provider: "2captcha"}
	var encrypted string
	err := s.DB.QueryRowContext(ctx, `SELECT provider,encrypted_secret,enabled,updated_at FROM captcha_solver_settings WHERE account_id=$1`, accountID).Scan(&setting.Provider, &encrypted, &setting.Enabled, &setting.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return setting, "", nil
	}
	if err != nil {
		return setting, "", err
	}
	setting.Configured = true
	if s.Envelope == nil {
		return setting, "", fmt.Errorf("controller encryption key is not configured")
	}
	plain, err := s.Envelope.Open(accountID, encrypted)
	if err != nil {
		return setting, "", fmt.Errorf("decrypt captcha solver credential: %w", err)
	}
	var secret struct {
		APIKey string `json:"apiKey"`
	}
	if json.Unmarshal(plain, &secret) != nil || strings.TrimSpace(secret.APIKey) == "" {
		return setting, "", fmt.Errorf("captcha solver credential is malformed; save it again")
	}
	return setting, strings.TrimSpace(secret.APIKey), nil
}

// PutCaptchaSolver stores or replaces the owner's 2captcha credential. An
// empty API key keeps the stored credential so the toggle can change without
// re-entering the key.
func (s *Store) PutCaptchaSolver(ctx context.Context, p Principal, apiKey string, enabled bool) (CaptchaSolverSetting, error) {
	if p.Role != "owner" {
		return CaptchaSolverSetting{}, fmt.Errorf("only an account owner may manage captcha solving")
	}
	apiKey = strings.TrimSpace(apiKey)
	if apiKey != "" && !captchaSolverAPIKeyPattern.MatchString(apiKey) {
		return CaptchaSolverSetting{}, fmt.Errorf("2captcha API keys are 8–128 letters or digits")
	}
	if s.Envelope == nil {
		return CaptchaSolverSetting{}, fmt.Errorf("controller encryption key is not configured")
	}
	encrypted := ""
	if apiKey == "" {
		var existing string
		err := s.DB.QueryRowContext(ctx, `SELECT encrypted_secret FROM captcha_solver_settings WHERE account_id=$1`, p.AccountID).Scan(&existing)
		if errors.Is(err, sql.ErrNoRows) {
			return CaptchaSolverSetting{}, fmt.Errorf("a 2captcha API key is required")
		}
		if err != nil {
			return CaptchaSolverSetting{}, err
		}
		encrypted = existing
	} else {
		sealed, err := s.Envelope.Seal(p.AccountID, []byte(`{"apiKey":`+jsonQuote(apiKey)+`}`))
		if err != nil {
			return CaptchaSolverSetting{}, err
		}
		encrypted = sealed
	}
	setting := CaptchaSolverSetting{Provider: "2captcha", Enabled: enabled, Configured: true}
	err := s.DB.QueryRowContext(ctx, `INSERT INTO captcha_solver_settings(account_id,provider,encrypted_secret,enabled) VALUES($1,'2captcha',$2,$3)
		ON CONFLICT(account_id) DO UPDATE SET encrypted_secret=excluded.encrypted_secret,enabled=excluded.enabled,updated_at=now()
		RETURNING updated_at`, p.AccountID, encrypted, enabled).Scan(&setting.UpdatedAt)
	if err != nil {
		return CaptchaSolverSetting{}, err
	}
	_, _ = s.DB.ExecContext(ctx, `INSERT INTO audit_log(account_id,user_id,action,target_type,target_id,detail) VALUES($1,$2,'captcha_solver.put','captcha_solver',$3,jsonb_build_object('enabled',$4::boolean))`, p.AccountID, p.UserID, "2captcha", enabled)
	return setting, nil
}

// DeleteCaptchaSolver removes the stored credential and disables auto-solving.
func (s *Store) DeleteCaptchaSolver(ctx context.Context, p Principal) error {
	if p.Role != "owner" {
		return fmt.Errorf("only an account owner may manage captcha solving")
	}
	result, err := s.DB.ExecContext(ctx, `DELETE FROM captcha_solver_settings WHERE account_id=$1`, p.AccountID)
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return fmt.Errorf("captcha solver is not configured")
	}
	_, _ = s.DB.ExecContext(ctx, `INSERT INTO audit_log(account_id,user_id,action,target_type,target_id) VALUES($1,$2,'captcha_solver.delete','captcha_solver','2captcha')`, p.AccountID, p.UserID)
	return nil
}

func jsonQuote(value string) string {
	data, err := json.Marshal(value)
	if err != nil {
		return `""`
	}
	return string(data)
}
