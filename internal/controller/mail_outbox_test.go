package controller

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestApprovedMailUsesResendAPIWithStableIdempotency(t *testing.T) {
	t.Setenv("VMBOX_RESEND_API_KEY", "synthetic-test-key")
	var calls int
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != http.MethodPost || r.URL.Path != "/emails" || r.Header.Get("Authorization") != "Bearer synthetic-test-key" || r.Header.Get("Idempotency-Key") != "draft-1" {
			t.Errorf("unexpected Resend request: method=%s path=%s", r.Method, r.URL.Path)
		}
		var body struct {
			From    string   `json:"from"`
			To      []string `json:"to"`
			Subject string   `json:"subject"`
			Text    string   `json:"text"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body.From != "box@example.test" || !reflect.DeepEqual(body.To, []string{"recipient@example.test"}) || body.Subject != "Approved" || body.Text != "Reviewed text" {
			t.Errorf("wrong approved mail body: %+v", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resend-123"}`))
	}))
	defer remote.Close()
	s := &Server{ResendURL: remote.URL + "/emails", HTTP: remote.Client()}
	id, err := s.sendApprovedMail(context.Background(), "draft-1", "box@example.test", []string{"recipient@example.test"}, "Approved", "Reviewed text")
	if err != nil || id != "resend-123" || calls != 1 {
		t.Fatalf("Resend result id=%q err=%v calls=%d", id, err, calls)
	}
}
