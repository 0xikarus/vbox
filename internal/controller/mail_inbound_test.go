package controller

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func signedMailRequest(method, path string, body []byte, timestamp, secret string) *http.Request {
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(timestamp + "."))
	_, _ = mac.Write(body)
	req.Header.Set("X-Vbox-Timestamp", timestamp)
	req.Header.Set("X-Vbox-Signature", hex.EncodeToString(mac.Sum(nil)))
	return req
}

func TestInboundMailHMACMatchesWorkerWireFormat(t *testing.T) {
	now := time.Unix(1791000000, 0)
	body := []byte("{\"to\":\"box@example.test\"}")
	ts := fmt.Sprint(now.Unix())
	req := signedMailRequest(http.MethodPost, "/v1/inbound-mail/recipient", body, ts, "synthetic-secret")
	if err := verifyInboundMailHMAC(now, "synthetic-secret", ts, req.Header.Get("X-Vbox-Signature"), body); err != nil {
		t.Fatalf("Worker ts + dot + raw body signature rejected: %v", err)
	}
	if err := verifyInboundMailHMAC(now.Add(301*time.Second), "synthetic-secret", ts, req.Header.Get("X-Vbox-Signature"), body); err == nil {
		t.Fatal("expired signature accepted")
	}
	if err := verifyInboundMailHMAC(now, "synthetic-secret", ts, req.Header.Get("X-Vbox-Signature"), append(body, '!')); err == nil {
		t.Fatal("body tampering accepted")
	}
	if err := verifyInboundMailHMAC(now, "synthetic-secret", ts, strings.Repeat("0", 64), body); err == nil {
		t.Fatal("bad signature accepted")
	}
}

func TestInboundRecipientUsesWorkerSignedJSONWithoutIdempotency(t *testing.T) {
	t.Setenv("VMBOX_MAIL_DOMAIN", "example.test")
	t.Setenv("VMBOX_INBOUND_MAIL_SECRET", "synthetic-secret")
	store, mock := testStore(t)
	body := []byte(`{"to":"box@example.test"}`)
	req := signedMailRequest(http.MethodPost, "/v1/inbound-mail/recipient", body, fmt.Sprint(time.Now().Unix()), "synthetic-secret")
	req.Header.Set("Content-Type", "application/json")
	mock.ExpectQuery("SELECT a.account_id::text,a.id::text,a.owning_box_id::text").WithArgs("box@example.test").WillReturnRows(sqlmock.NewRows([]string{"account", "address", "box", "name", "enabled", "state"}).AddRow("account-a", panelTestBox, panelTestBox, "Builder", true, "running"))
	response := httptest.NewRecorder()
	(&Server{Store: store}).inboundMailRecipient(response, req)
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"accept":true`) {
		t.Fatalf("recipient status=%d body=%s", response.Code, response.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestInboundMailDuplicateKeyReturnsAcceptedWithoutSecondRow(t *testing.T) {
	t.Setenv("VMBOX_MAIL_DOMAIN", "example.test")
	t.Setenv("VMBOX_INBOUND_MAIL_SECRET", "synthetic-secret")
	body := []byte("From: sender@example.test\r\nSubject: Hi\r\n\r\nHello")
	hash := sha256.Sum256(body)
	key := hex.EncodeToString(hash[:])
	for _, tc := range []struct {
		name, storedHash string
		status           int
	}{{"duplicate", key, 202}, {"collision", strings.Repeat("0", 64), 409}} {
		t.Run(tc.name, func(t *testing.T) {
			store, mock := testStore(t)
			mock.ExpectQuery("SELECT raw_sha256,envelope_to,id::text FROM mail_messages").WithArgs("box@example.test:" + key).
				WillReturnRows(sqlmock.NewRows([]string{"raw_sha256", "envelope_to", "id"}).AddRow(tc.storedHash, "box@example.test", "00000000-0000-4000-8000-000000000001"))
			req := signedMailRequest(http.MethodPost, "/v1/inbound-mail", body, fmt.Sprint(time.Now().Unix()), "synthetic-secret")
			req.Header.Set("X-Vbox-Rcpt", "box@example.test")
			req.Header.Set("X-Vbox-From", "sender@example.test")
			req.Header.Set("X-Vbox-Idempotency", key)
			response := httptest.NewRecorder()
			(&Server{Store: store}).inboundMailMessage(response, req)
			if response.Code != tc.status {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
