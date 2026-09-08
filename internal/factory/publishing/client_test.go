package publishing

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/0xikarus/vmbox-service/internal/factory/githubapp"
)

// Exercise the actual client, including its marker scans and payload matching,
// against loopback HTTP only. A committed comment is hidden after a lost response
// until a later reconciliation, so the coordinator cannot infer completion.
func TestRealClientLostCommentResponse(t *testing.T) {
	db := database(t)
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	issues, comments := []map[string]any{}, []map[string]any{}
	posts := 0
	visible := false
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		encode := func(v any) {
			if err := json.NewEncoder(w).Encode(v); err != nil {
				t.Error(err)
			}
		}
		switch r.URL.Path {
		case "/app/installations/11/access_tokens":
			encode(map[string]any{"token": "fixture-only", "expires_at": time.Now().Add(time.Hour), "permissions": map[string]string{"metadata": "read", "issues": "write"}})
		case "/installation/repositories":
			encode(map[string]any{"repositories": []map[string]any{{"id": 2, "full_name": "org/repo", "default_branch": "main"}}})
		case "/repos/org/repo/issues", "/repos/org/repo/issues/1/comments":
			if r.Method == "GET" {
				if strings.HasSuffix(r.URL.Path, "comments") {
					encode(comments)
				} else {
					encode(issues)
				}
				return
			}
			if r.Method != "POST" {
				t.Error("unexpected method", r.Method)
				w.WriteHeader(405)
				return
			}
			var active int
			if err := db.QueryRow(`SELECT count(*) FROM factory_publication_operations WHERE state='attempted' AND intent IS NOT NULL AND lease IS NOT NULL`).Scan(&active); err != nil || active != 1 {
				t.Error("POST without durable intent", active, err)
			}
			posts++
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Error(err)
			}
			payload["id"] = 100 + posts
			if strings.HasSuffix(r.URL.Path, "comments") {
				payload["issue_url"] = server.URL + "/repos/org/repo/issues/1"
				comments = append(comments, payload)
				// The server committed the comment, but the immediate reconciliation cannot
				// see it. HTTP 500 is not proof that the POST had no effect.
				w.WriteHeader(500)
				return
			}
			payload["number"] = posts
			issues = append(issues, payload)
			encode(payload)
		case "/repos/org/repo/issues/comments":
			if visible {
				encode(comments)
			} else {
				encode([]map[string]any{})
			}
		case "/repos/org/repo/issues/1":
			encode(issues[0])
		default:
			t.Error("unexpected fixture path", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(server.Close)
	client, err := githubapp.New(githubapp.Config{AppID: "123", PrivateKey: pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}), Installations: map[string][]int64{"account": {11}}, BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	c := New(db, client, Policy{AccountID: "account", IssuesWrite: true})
	if err = c.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	work := seed(t, db, "account", "approved_queued")
	for i := 0; i < 3; i++ {
		if err = c.Step(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 2; i++ {
		if err = c.Step(context.Background()); !errors.Is(err, githubapp.ErrPublicationUncertain) {
			t.Fatal(err)
		}
		current, _ := getWork(t, db, work.ID)
		if current.State != "publishing_issues" || current.Error == "" {
			t.Fatal("comment ambiguity falsely completed")
		}
		ready(t, db)
	}
	mu.Lock()
	visible = true
	mu.Unlock()
	if err = c.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	current, _ := getWork(t, db, work.ID)
	if current.State != "build_queued" {
		t.Fatal("failed to reconcile real payload", current.State)
	}
	mu.Lock()
	defer mu.Unlock()
	if posts != 4 {
		t.Fatal("duplicate GitHub POST", posts)
	}
}
