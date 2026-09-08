package githubapp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// HTTP fixtures prove request scoping/reconciliation, not live App acceptance.
func TestPullRequestPublication(t *testing.T) {
	for _, mode := range []string{"normal", "lost-response", "wrong-head", "changed-after-post", "reconcile-only", "permission-denied", "foreign-repository", "closed"} {
		t.Run(mode, func(t *testing.T) {
			_, pem := testKey(t)
			sha := strings.Repeat("a", 40)
			posts := 0
			var saved *pullRecord
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				encode := func(v any) {
					if err := json.NewEncoder(w).Encode(v); err != nil {
						t.Error(err)
					}
				}
				switch r.URL.Path {
				case "/app/installations/11/access_tokens":
					var in struct {
						IDs         []int64           `json:"repository_ids"`
						Permissions map[string]string `json:"permissions"`
					}
					if json.NewDecoder(r.Body).Decode(&in) != nil {
						t.Error("invalid grant request")
					}
					token := "discovery"
					permissions := map[string]string{"metadata": "read"}
					if len(in.IDs) > 0 {
						if len(in.IDs) != 1 || in.IDs[0] != 2 || len(in.Permissions) != 3 || in.Permissions["contents"] != "read" || in.Permissions["pull_requests"] != "write" || in.Permissions["metadata"] != "read" {
							t.Error("excessive or wrong PR scope")
						}
						token = "pull"
						permissions = in.Permissions
						if mode == "permission-denied" {
							permissions["pull_requests"] = "read"
						}
					}
					encode(map[string]any{"token": token, "expires_at": time.Now().Add(time.Hour), "permissions": permissions})
				case "/installation/repositories":
					repos := []repository{{ID: 2, FullName: "org/repo"}}
					if mode == "foreign-repository" && r.Header.Get("Authorization") == "Bearer pull" {
						repos = append(repos, repository{ID: 3, FullName: "org/other"})
					}
					encode(map[string]any{"repositories": repos})
				default:
					if r.Header.Get("Authorization") != "Bearer pull" {
						t.Error("wrong publishing token")
					}
					if strings.HasPrefix(r.URL.Path, "/repos/org/repo/commits/") {
						value := sha
						if mode == "wrong-head" {
							value = strings.Repeat("b", 40)
						}
						encode(map[string]string{"sha": value})
						return
					}
					if r.URL.Path == "/repos/org/repo/pulls" && r.Method == "POST" {
						posts++
						var in struct {
							Head, Base, Title, Body string
							Modify                  bool `json:"maintainer_can_modify"`
						}
						if json.NewDecoder(r.Body).Decode(&in) != nil || in.Modify {
							t.Error("invalid creation")
						}
						p := pullRecord{ID: 30, Number: 3, Title: in.Title, Body: in.Body, State: "open"}
						p.Head.Ref = in.Head
						p.Head.SHA = sha
						p.Base.Ref = in.Base
						p.Head.Repo.ID = 2
						p.Head.Repo.FullName = "org/repo"
						p.Base.Repo = p.Head.Repo
						if mode == "changed-after-post" {
							p.Head.SHA = strings.Repeat("b", 40)
						}
						if mode == "closed" {
							p.State = "closed"
						}
						saved = &p
						if mode == "lost-response" {
							w.WriteHeader(502)
							return
						}
						encode(p)
						return
					}
					if r.URL.Path == "/repos/org/repo/pulls/3" && saved != nil {
						encode(saved)
						return
					}
					if r.URL.Path == "/repos/org/repo/pulls" && r.Method == "GET" {
						if r.URL.Query().Get("state") != "all" {
							t.Error("closed PRs omitted")
						}
						items := []pullRecord{}
						if saved != nil {
							items = append(items, *saved)
						}
						encode(items)
						return
					}
					t.Error("unexpected request", r.Method, r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer server.Close()
			client, err := New(Config{AppID: "1", PrivateKey: pem, Installations: map[string][]int64{"account": {11}}, BaseURL: server.URL})
			if err != nil {
				t.Fatal(err)
			}
			a := PullAuthority{AccountID: "account", RepositoryID: "2", PullRequestsWrite: true}
			spec := PullSpec{Head: "factory/feature", Base: "main", CandidateSHA: sha, Title: "Feature", Body: "Verified feature evidence"}
			op := PublicationOperation{Key: "work:feature:pr", ReconcileOnly: mode == "reconcile-only"}
			got, err := client.PublishPullRequest(context.Background(), a, op, spec)
			if mode == "normal" || mode == "lost-response" {
				if err != nil || got.Number != 3 || got.HeadSHA != sha || got.URL != "https://github.com/org/repo/pull/3" {
					t.Fatal(got, err)
				}
				op.ReconcileOnly = true
				if _, err = client.PublishPullRequest(context.Background(), a, op, spec); err != nil {
					t.Fatal(err)
				}
				if posts != 1 {
					t.Fatal("duplicate PR")
				}
				spec.Body = "changed"
				if _, err = client.PublishPullRequest(context.Background(), a, op, spec); !errors.Is(err, ErrPublicationConflict) {
					t.Fatal("changed operation accepted", err)
				}
			} else {
				if err == nil {
					t.Fatal("unsafe publication accepted")
				}
				if mode != "changed-after-post" && mode != "closed" && posts != 0 {
					t.Fatal("unexpected write")
				}
			}
		})
	}
}
