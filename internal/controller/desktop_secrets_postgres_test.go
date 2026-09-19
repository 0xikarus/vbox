package controller

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/0xikarus/vmbox-service/internal/secrets"
)

func TestDesktopSecretsPostgres(t *testing.T) {
	dsn := os.Getenv("VMBOX_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires explicitly disposable PostgreSQL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.DB.SetMaxOpenConns(1)
	ns := "desktop_secret_test_" + strings.ReplaceAll(uuid(), "-", "")
	if _, err = s.DB.ExecContext(ctx, "CREATE SCHEMA "+ns); err != nil {
		t.Fatal(err)
	}
	defer s.DB.ExecContext(context.Background(), "DROP SCHEMA "+ns+" CASCADE")
	if _, err = s.DB.ExecContext(ctx, "SET search_path TO "+ns); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err = s.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
	}
	s.Envelope, err = secrets.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.Bootstrap(ctx, "desktop-test", "test-owner", uuid())
	if err != nil {
		t.Fatal(err)
	}
	q, err := s.Bootstrap(ctx, "desktop-other", "other-owner", uuid())
	if err != nil {
		t.Fatal(err)
	}
	box := uuid()
	if _, err = s.DB.ExecContext(ctx, `INSERT INTO logical_boxes(id,account_id,owner_user_id,name,provider,state,volume_id,volume_name) VALUES($1,$2,$3,'test-agent','railway','hibernated','test-volume','test-volume')`, box, p.AccountID, p.UserID); err != nil {
		t.Fatal(err)
	}
	value, created, err := s.EnsureDesktopSecret(ctx, p, box, "signup", "https://example.com", nil)
	if err != nil || !created || value.Status != "pending" {
		t.Fatalf("create failed: %v", err)
	}
	var first string
	if err = s.DB.QueryRowContext(ctx, `SELECT encrypted_value FROM desktop_secrets WHERE box_id=$1`, box).Scan(&first); err != nil {
		t.Fatal(err)
	}
	plain, err := s.Envelope.Open(desktopSecretScope(p.AccountID, box, "signup", "https://example.com"), first)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(plain)
	if len(plain) != 24 {
		t.Fatal("wrong generated length")
	}
	if _, created, err = s.EnsureDesktopSecret(ctx, p, box, "signup", "https://example.com", nil); err != nil || created {
		t.Fatalf("retry not idempotent: %v", err)
	}
	var second string
	if err = s.DB.QueryRowContext(ctx, `SELECT encrypted_value FROM desktop_secrets WHERE box_id=$1`, box).Scan(&second); err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatal("retry replaced encrypted password")
	}
	if _, _, err = s.EnsureDesktopSecret(ctx, p, box, "signup", "https://other.example", nil); err == nil {
		t.Fatal("origin conflict accepted")
	}
	if _, _, err = s.EnsureDesktopSecret(ctx, q, box, "signup", "https://example.com", nil); err == nil {
		t.Fatal("cross-account creation accepted")
	}
	if _, err = s.ListDesktopSecrets(ctx, q, box); err == nil {
		t.Fatal("cross-account list accepted")
	}
	listed, err := s.ListDesktopSecrets(ctx, p, box)
	if err != nil {
		t.Fatal(err)
	}
	public, _ := json.Marshal(listed)
	if strings.Contains(string(public), string(plain)) || strings.Contains(string(public), first) {
		t.Fatal("secret exposed in metadata")
	}

	policy := secrets.PasswordPolicy{Length: 40, Alphabet: "abcdefghijklmnopqrstuvwxyz0123456789"}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, created, err = s.ensureDesktopSecret(ctx, tx, p, box, "custom", "https://example.com", nil, policy)
	if err != nil || !created {
		tx.Rollback()
		t.Fatalf("custom policy save: %v", err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var customCipher string
	if err = s.DB.QueryRowContext(ctx, `SELECT encrypted_value FROM desktop_secrets WHERE box_id=$1 AND secret_key='custom'`, box).Scan(&customCipher); err != nil {
		t.Fatal(err)
	}
	custom, err := s.Envelope.Open(desktopSecretScope(p.AccountID, box, "custom", "https://example.com"), customCipher)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(custom)
	if len(custom) != 40 {
		t.Fatal("custom password length was lost")
	}
	for _, b := range custom {
		if !strings.ContainsRune(policy.Alphabet, rune(b)) {
			t.Fatal("custom alphabet was lost")
		}
	}
	_, created, err = s.ensureDesktopSecret(ctx, s.DB, p, box, "custom", "https://example.com", nil, secrets.PasswordPolicy{Length: 24})
	if err != nil || created {
		t.Fatal("changed policy rotated existing reference")
	}
	var retained string
	if err = s.DB.QueryRowContext(ctx, `SELECT encrypted_value FROM desktop_secrets WHERE box_id=$1 AND secret_key='custom'`, box).Scan(&retained); err != nil || retained != customCipher {
		t.Fatal("retry replaced custom secret")
	}

	testDesktopPrivateDataRoutes(t, s, p, q, box)
	frameID := uuid()
	frameBytes := []byte{0xff, 0xd8, 0xff, 0xd9}
	if _, err = s.DB.ExecContext(ctx, `INSERT INTO desktop_replay_frames(id,account_id,box_id,captured_at,width,height,data) VALUES($1,$2,$3,now()-interval '1 minute',1,1,$4)`, frameID, p.AccountID, box, frameBytes); err != nil {
		t.Fatalf("desktop replay frame was not retained: %v", err)
	}
	testDesktopReplayRoutes(t, s, p, q, box, frameID, frameBytes)

	fence := strings.Repeat("a", 64)
	if _, err = s.DB.ExecContext(ctx, `UPDATE logical_boxes SET state='running',fencing_token=$2 WHERE id=$1`, box, fence); err != nil {
		t.Fatal(err)
	}
	token, err := s.IssueDesktopAgentToken(ctx, p, box, fence)
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(s, nil)
	authorize := func(token string, want int) {
		t.Helper()
		request := httptest.NewRequest("POST", "/", nil).WithContext(ctx)
		request.SetPathValue("id", "untrusted-box")
		request.Header.Set("Authorization", "DesktopAgent "+token)
		response := httptest.NewRecorder()
		server.desktopAgentAuth(func(w http.ResponseWriter, r *http.Request, principal Principal) {
			if r.PathValue("id") != box || principal.AccountID != p.AccountID || principal.Role != "desktop-agent" {
				t.Error("scope not preserved")
			}
			w.WriteHeader(204)
		})(response, request)
		if response.Code != want {
			t.Fatalf("authorization status: got %d want %d", response.Code, want)
		}
	}
	authorize(token, 204)
	rotated, err := s.IssueDesktopAgentToken(ctx, p, box, fence)
	if err != nil {
		t.Fatal(err)
	}
	authorize(token, 401)
	authorize(rotated, 204)
	if _, err = s.DB.ExecContext(ctx, `UPDATE logical_boxes SET fencing_token=$2 WHERE id=$1`, box, strings.Repeat("b", 64)); err != nil {
		t.Fatal(err)
	}
	authorize(rotated, 401)
	if _, err = s.DB.ExecContext(ctx, `DELETE FROM logical_boxes WHERE id=$1`, box); err != nil {
		t.Fatal(err)
	}
	var tokens int
	if err = s.DB.QueryRowContext(ctx, `SELECT count(*) FROM desktop_agent_tokens WHERE box_id=$1`, box).Scan(&tokens); err != nil || tokens != 0 {
		t.Fatal("deleted box retained agent credential")
	}
	var count int
	if err = s.DB.QueryRowContext(ctx, `SELECT count(*) FROM desktop_secrets WHERE box_id=$1`, box).Scan(&count); err != nil || count != 0 {
		t.Fatal("box deletion left secret bindings")
	}
}
