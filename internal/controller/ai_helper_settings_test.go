package controller

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/0xikarus/vmbox-service/internal/secrets"
)

func TestAIHelperSettingValidation(t *testing.T) {
	for _, tc := range []struct {
		key, model, wantModel string
		valid                 bool
	}{
		{"sk-or-v1-example", "", "openrouter/auto", true},
		{"sk-or-v1-example", "openrouter/anthropic/test", "anthropic/test", true},
		{"sk-or-v1-example", "openrouter/auto", "openrouter/auto", true},
		{"", "anthropic/test", "anthropic/test", true},
		{"short", "auto", "", false},
		{"sk-or-v1-example\n", "auto", "", false},
		{"sk-or-v1-example", "model with spaces", "", false},
	} {
		setting := aiHelperOpenRouterSetting{Key: tc.key, Model: tc.model}
		err := validateAIHelperSetting(&setting)
		if (err == nil) != tc.valid || tc.valid && setting.Model != tc.wantModel {
			t.Errorf("model %q: got %q, %v", tc.model, setting.Model, err)
		}
	}
}

func TestAIHelperSettingPostgres(t *testing.T) {
	dsn := os.Getenv("VMBOX_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires disposable PostgreSQL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.DB.SetMaxOpenConns(1)
	ns := "ai_helper_test_" + strings.ReplaceAll(uuid(), "-", "")
	if _, err = s.DB.ExecContext(ctx, "CREATE SCHEMA "+ns); err != nil {
		t.Fatal(err)
	}
	defer s.DB.ExecContext(context.Background(), "DROP SCHEMA "+ns+" CASCADE")
	if _, err = s.DB.ExecContext(ctx, "SET search_path TO "+ns); err != nil {
		t.Fatal(err)
	}
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	s.Envelope, err = secrets.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.Bootstrap(ctx, "ai-test-a", "owner-a", uuid())
	if err != nil {
		t.Fatal(err)
	}
	q, err := s.Bootstrap(ctx, "ai-test-b", "owner-b", uuid())
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SaveAIHelperOpenRouter(ctx, p, aiHelperOpenRouterSetting{Key: "sk-or-v1-synthetic-secret", Model: "anthropic/test"}); err != nil {
		t.Fatal(err)
	}
	var sealed string
	if err = s.DB.QueryRowContext(ctx, `SELECT encrypted_key FROM ai_helper_settings WHERE account_id=$1`, p.AccountID).Scan(&sealed); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(sealed, "synthetic-secret") {
		t.Fatal("plaintext key stored")
	}
	if _, err = s.Envelope.Open(aiHelperKeyScope(q.AccountID), sealed); err == nil {
		t.Fatal("cross-account key decrypted")
	}
	if got, err := s.AIHelperOpenRouter(ctx, p); err != nil || got.Key != "sk-or-v1-synthetic-secret" || got.Model != "anthropic/test" {
		t.Fatalf("roundtrip: %+v, %v", got.Model, err)
	}
	if err = s.SaveAIHelperOpenRouter(ctx, p, aiHelperOpenRouterSetting{Model: "google/test"}); err != nil {
		t.Fatal(err)
	}
	if got, err := s.AIHelperOpenRouter(ctx, p); err != nil || got.Key != "sk-or-v1-synthetic-secret" || got.Model != "google/test" {
		t.Fatalf("model-only update lost key: %q, %q, %v", got.Key, got.Model, err)
	}
	if err = s.SaveAIHelperOpenRouter(ctx, q, aiHelperOpenRouterSetting{Model: "google/test"}); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("model-only update created a keyless setting: %v", err)
	}
	if _, err = s.AIHelperOpenRouter(ctx, q); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("cross-account read: %v", err)
	}
}
