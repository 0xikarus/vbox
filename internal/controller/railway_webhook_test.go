package controller

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestRailwayWebhookHintsPostgres(t *testing.T) {
	dsn := os.Getenv("VMBOX_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires disposable PostgreSQL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	store, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	store.DB.SetMaxOpenConns(1)
	t.Cleanup(func() { store.Close() })
	schema := "railway_webhook_" + strings.ReplaceAll(uuid(), "-", "")
	if _, err = store.DB.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = store.DB.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE") })
	if _, err = store.DB.ExecContext(ctx, "SET search_path TO "+schema); err != nil {
		t.Fatal(err)
	}
	if err = store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	principal, err := store.Bootstrap(ctx, "webhook-acceptance", "owner", uuid())
	if err != nil {
		t.Fatal(err)
	}
	credentialID, slotID := uuid(), uuid()
	if _, err = store.DB.ExecContext(ctx, `INSERT INTO provider_credentials(id,account_id,provider,name,encrypted_value,config) VALUES($1,$2,'railway','primary','test-only', '{"projectId":"project","environmentId":"environment"}')`, credentialID, principal.AccountID); err != nil {
		t.Fatal(err)
	}
	if _, err = store.DB.ExecContext(ctx, `INSERT INTO compute_slots(id,account_id,provider,provider_credential,ordinal,state,service_id,deployment_instance_id,health) VALUES($1,$2,'railway','primary',1,'occupied','service','old-deployment','healthy')`, slotID, principal.AccountID); err != nil {
		t.Fatal(err)
	}

	secret := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x42}, 32))
	receiver, err := NewRailwayWebhookReceiver(store, secret)
	if err != nil {
		t.Fatal(err)
	}
	payload := railwayWebhookPayload(t, "Deployment.failed", "event-one", "project", "environment", "service", "new-deployment", "2026-09-14T12:00:00Z")
	deliver := func(candidate string, body []byte) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(http.MethodPost, "/v1/railway-webhooks/redacted", bytes.NewReader(body))
		request.SetPathValue("secret", candidate)
		response := httptest.NewRecorder()
		receiver.ServeHTTP(response, request)
		return response
	}
	if response := deliver("wrong-secret", payload); response.Code != http.StatusNotFound {
		t.Fatalf("wrong secret status=%d body=%q", response.Code, response.Body.String())
	}
	if response := deliver(secret, bytes.Repeat([]byte("x"), railwayWebhookBodyLimit+1)); response.Code != http.StatusBadRequest {
		t.Fatalf("oversized status=%d", response.Code)
	}
	if response := deliver(secret, railwayWebhookPayload(t, "Volume.nearLimit", "ignored", "project", "environment", "service", "deployment", "2026-09-14T12:00:00Z")); response.Code != http.StatusNoContent {
		t.Fatalf("non-deployment status=%d", response.Code)
	}
	if response := deliver(secret, railwayWebhookPayload(t, "Deployment.failed", "wrong-scope", "other-project", "environment", "service", "new-deployment", "2026-09-14T12:00:01Z")); response.Code != http.StatusAccepted {
		t.Fatalf("out-of-scope status=%d", response.Code)
	}
	if response := deliver(secret, payload); response.Code != http.StatusAccepted {
		t.Fatalf("accepted status=%d body=%q", response.Code, response.Body.String())
	}
	if response := deliver(secret, payload); response.Code != http.StatusAccepted {
		t.Fatalf("duplicate status=%d", response.Code)
	}
	second := railwayWebhookPayload(t, "Deployment.success", "event-two", "project", "environment", "service", "new-deployment", "2026-09-14T12:00:02Z")
	if response := deliver(secret, second); response.Code != http.StatusAccepted {
		t.Fatalf("second event status=%d", response.Code)
	}

	var hints int
	if err := store.DB.QueryRowContext(ctx, `SELECT count(*) FROM railway_refresh_hints`).Scan(&hints); err != nil || hints != 2 {
		t.Fatalf("durable dedupe count=%d err=%v", hints, err)
	}
	var state, health, deployment string
	if err := store.DB.QueryRowContext(ctx, `SELECT state,health,deployment_instance_id FROM compute_slots WHERE id=$1`, slotID).Scan(&state, &health, &deployment); err != nil {
		t.Fatal(err)
	}
	if state != "occupied" || health != "healthy" || deployment != "old-deployment" {
		t.Fatalf("webhook asserted infrastructure state: %q %q %q", state, health, deployment)
	}

	// A controller that dies after claiming leaves recoverable work. Simulate
	// expiry in this isolated database instead of waiting for the two-minute lease.
	if _, err = store.DB.ExecContext(ctx, `UPDATE railway_refresh_hints SET state='claimed',claim_owner='dead-controller',claim_expires_at=now()-interval '1 second' WHERE event_type='Deployment.failed'`); err != nil {
		t.Fatal(err)
	}
	claimed, err := receiver.claimRailwayRefreshHints(ctx, 64)
	if err != nil || len(claimed) != 1 || len(claimed[0].eventHashes) != 2 {
		t.Fatalf("coalesced claims=%+v err=%v", claimed, err)
	}
	if claimed[0].AccountID != principal.AccountID || claimed[0].SlotID != slotID || claimed[0].ProviderCredential != "primary" || claimed[0].ProjectID != "project" || claimed[0].EnvironmentID != "environment" || claimed[0].ServiceID != "service" {
		t.Fatalf("wrong refresh target: %+v", claimed[0])
	}
	if err := receiver.finishRailwayRefreshHint(ctx, claimed[0], false); err != nil {
		t.Fatal(err)
	}
	var retryDelaySeconds float64
	if err := store.DB.QueryRowContext(ctx, `SELECT EXTRACT(EPOCH FROM (min(next_attempt_at)-now())) FROM railway_refresh_hints WHERE state='pending'`).Scan(&retryDelaySeconds); err != nil {
		t.Fatal(err)
	}
	if retryDelaySeconds < 1 || retryDelaySeconds > 6 {
		t.Fatalf("retry delay outside bounded jitter: %.3fs", retryDelaySeconds)
	}
	if _, err = store.DB.ExecContext(ctx, `UPDATE railway_refresh_hints SET next_attempt_at=now()`); err != nil {
		t.Fatal(err)
	}
	claimed, err = receiver.claimRailwayRefreshHints(ctx, 64)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("retry claim=%+v err=%v", claimed, err)
	}
	if err := receiver.finishRailwayRefreshHint(ctx, claimed[0], true); err != nil {
		t.Fatal(err)
	}
	if response := deliver(secret, payload); response.Code != http.StatusAccepted {
		t.Fatalf("processed retry status=%d", response.Code)
	}
	if err := store.DB.QueryRowContext(ctx, `SELECT count(*) FROM railway_refresh_hints`).Scan(&hints); err != nil || hints != 2 {
		t.Fatalf("processed delivery dedupe count=%d err=%v", hints, err)
	}
}

func TestRailwayWebhookAuthenticationAndRateLimit(t *testing.T) {
	secret := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x24}, 32))
	if _, err := NewRailwayWebhookReceiver(nil, secret); err == nil {
		t.Fatal("nil store accepted")
	}
	if _, err := NewRailwayWebhookReceiver(&Store{}, "short"); err == nil {
		t.Fatal("weak secret accepted")
	}
	receiver, err := NewRailwayWebhookReceiver(&Store{}, secret)
	if err != nil {
		t.Fatal(err)
	}
	receiver.limit = 1
	receiver.now = func() time.Time { return time.Unix(100, 0) }
	body := railwayWebhookPayload(t, "Volume.nearLimit", "ignored", "project", "environment", "service", "deployment", "2026-09-14T12:00:00Z")
	deliver := func() *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, "/redacted", bytes.NewReader(body))
		request.SetPathValue("secret", secret)
		response := httptest.NewRecorder()
		receiver.ServeHTTP(response, request)
		return response
	}
	if response := deliver(); response.Code != http.StatusNoContent {
		t.Fatalf("first delivery status=%d", response.Code)
	}
	if response := deliver(); response.Code != http.StatusTooManyRequests || response.Header().Get("Retry-After") != "60" {
		t.Fatalf("rate limit status=%d retry=%q", response.Code, response.Header().Get("Retry-After"))
	}
}

func railwayWebhookPayload(t *testing.T, eventType, detailID, project, environment, service, deployment, timestamp string) []byte {
	t.Helper()
	payload := map[string]any{
		"type":      eventType,
		"details":   map[string]any{"id": detailID, "status": "SUCCESS"},
		"resource":  map[string]any{"project": map[string]any{"id": project}, "environment": map[string]any{"id": environment}, "service": map[string]any{"id": service}, "deployment": map[string]any{"id": deployment}},
		"severity":  "WARNING",
		"timestamp": timestamp,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
