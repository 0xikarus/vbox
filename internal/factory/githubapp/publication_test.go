package githubapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/0xikarus/vmbox-service/internal/factory"
)

type publicationFixture struct {
	c                      *Client
	server                 *httptest.Server
	issues, comments       []publicationRecord
	posts, calls           int
	postMode               string
	permission             string
	extraRepo, revoked, pr bool
	listStatus             int
	link                   string
	pages                  int
}

func newPublicationFixture(t *testing.T) *publicationFixture {
	t.Helper()
	key, pem := testKey(t)
	f := &publicationFixture{permission: "write", issues: []publicationRecord{}, comments: []publicationRecord{}}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.calls++
		encode := func(v any) {
			t.Helper()
			if err := json.NewEncoder(w).Encode(v); err != nil {
				t.Error(err)
			}
		}
		switch r.URL.Path {
		case "/app/installations/11/access_tokens":
			checkJWT(t, r.Header.Get("Authorization"), key)
			var in struct {
				IDs         []int64           `json:"repository_ids"`
				Permissions map[string]string `json:"permissions"`
			}
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
				t.Error(err)
			}
			token := "discovery-secret"
			permissions := map[string]string{"metadata": "read"}
			if len(in.IDs) != 0 {
				if len(in.IDs) != 1 || in.IDs[0] != 2 || len(in.Permissions) != 2 || in.Permissions["issues"] != "write" || in.Permissions["metadata"] != "read" {
					t.Errorf("wrong issue token scope: %+v", in)
				}
				token = "issues-secret"
				permissions["issues"] = f.permission
			} else if len(in.Permissions) != 1 || in.Permissions["metadata"] != "read" {
				t.Error("broad discovery permission")
			}
			encode(map[string]any{"token": token, "expires_at": time.Now().Add(time.Hour), "permissions": permissions})
		case "/installation/repositories":
			repos := []repository{{ID: 2, FullName: "org/repo"}}
			if r.Header.Get("Authorization") == "Bearer issues-secret" {
				if f.extraRepo {
					repos = append(repos, repository{ID: 3, FullName: "org/other"})
				}
				if f.revoked {
					repos = []repository{}
				}
			} else if r.Header.Get("Authorization") != "Bearer discovery-secret" {
				t.Error("wrong discovery token")
			}
			encode(map[string]any{"repositories": repos})
		default:
			if r.Header.Get("Authorization") != "Bearer issues-secret" {
				t.Error("publication without issues token")
			}
			if r.Method == "GET" {
				if f.listStatus != 0 {
					w.Header().Set("Location", f.link)
					w.WriteHeader(f.listStatus)
					fmt.Fprint(w, "issues-secret private upstream body")
					return
				}
				if f.link != "" {
					w.Header().Set("Link", f.link)
				}
				if f.pages > 0 && r.URL.Query().Get("page") != "" {
					page, _ := strconv.Atoi(r.URL.Query().Get("page"))
					if page < f.pages {
						q := r.URL.Query()
						q.Set("page", strconv.Itoa(page+1))
						w.Header().Set("Link", "<"+f.server.URL+r.URL.Path+"?"+q.Encode()+">; rel=\"next\"")
						encode([]publicationRecord{})
						return
					}
				}
				switch r.URL.Path {
				case "/repos/org/repo/issues":
					if r.URL.Query().Get("state") != "all" || r.URL.Query().Get("per_page") != "100" || r.URL.Query().Get("direction") != "asc" {
						t.Error("incomplete issue scan")
					}
					encode(f.issues)
				case "/repos/org/repo/issues/comments":
					encode(f.comments)
				case "/repos/org/repo/issues/7", "/repos/org/repo/issues/8":
					n := int64(7)
					if strings.HasSuffix(r.URL.Path, "8") {
						n = 8
					}
					target := publicationRecord{Publication: Publication{ID: 70, Number: n}}
					if f.pr {
						target.PullRequest = json.RawMessage(`{"url":"https://api.github.com/repos/org/repo/pulls/7"}`)
					}
					encode(target)
				default:
					t.Errorf("unexpected GET %s", r.URL.Path)
					w.WriteHeader(404)
				}
				return
			}
			if r.Method != "POST" || (r.URL.Path != "/repos/org/repo/issues" && r.URL.Path != "/repos/org/repo/issues/7/comments" && r.URL.Path != "/repos/org/repo/issues/8/comments") {
				t.Errorf("unexpected write %s %s", r.Method, r.URL.Path)
				w.WriteHeader(404)
				return
			}
			f.posts++
			var in struct{ Title, Body string }
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
				t.Error(err)
			}
			record := publicationRecord{Publication: Publication{ID: int64(100 + f.posts), Number: int64(10 + f.posts)}, Title: in.Title, Body: in.Body}
			if strings.HasSuffix(r.URL.Path, "/comments") {
				record.Number = 0
				record.IssueURL = f.server.URL + strings.TrimSuffix(r.URL.Path, "/comments")
				if f.postMode != "absent" {
					f.comments = append(f.comments, record)
				}
			} else if f.postMode != "absent" {
				f.issues = append(f.issues, record)
			}
			switch f.postMode {
			case "drop", "absent":
				conn, _, err := w.(http.Hijacker).Hijack()
				if err != nil {
					t.Error(err)
					return
				}
				_ = conn.Close()
			case "malformed":
				w.WriteHeader(201)
				fmt.Fprint(w, `{"id":`)
			case "500":
				w.WriteHeader(500)
				fmt.Fprint(w, "issues-secret private upstream body")
			default:
				w.WriteHeader(201)
				encode(record)
			}
		}
	}))
	t.Cleanup(f.server.Close)
	var err error
	f.c, err = New(Config{AppID: "123", PrivateKey: pem, Installations: map[string][]int64{"account": {11}}, BaseURL: f.server.URL})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func approvedWork() factory.Work {
	feature := factory.Feature{ID: "one", Title: "First feature", Description: "Implement first feature", AcceptanceCriteria: []string{"works"}, Files: []string{"internal/a.go"}, Checks: []factory.Check{{Argv: []string{"go", "test", "./..."}, Cwd: ".", TimeoutSeconds: 60}}}
	second := feature
	second.ID = "two"
	second.Title = "Second feature"
	second.DependsOn = []string{"one"}
	p := factory.Plan{InputRevision: 3, Revision: 1, Markdown: "Approved plan", BaseSHA: strings.Repeat("a", 40), Features: []factory.Feature{feature, second}}
	return factory.Work{CreateWork: factory.CreateWork{RepositoryID: "2"}, ID: "work-1", Revision: 4, State: "approved_queued", BaseSHA: p.BaseSHA, Plans: []factory.Plan{p}, ApprovedPlanRevision: 1, Features: p.Features}
}

var publicationAuthority = WriteAuthority{AccountID: "account", RepositoryID: "2", IssuesWrite: true}

func TestPublicationRecoveryAndConflict(t *testing.T) {
	for _, kind := range []string{"master", "feature", "comment"} {
		for _, mode := range []string{"success", "drop", "malformed", "500"} {
			t.Run(kind+"/"+mode, func(t *testing.T) {
				f := newPublicationFixture(t)
				f.postMode = mode
				w := approvedWork()
				publish := func(op PublicationOperation) (Publication, error) {
					switch kind {
					case "master":
						return f.c.PublishMasterIssue(context.Background(), publicationAuthority, op, w)
					case "feature":
						return f.c.PublishFeatureIssue(context.Background(), publicationAuthority, op, w, "two", 7, map[string]int64{"one": 8})
					default:
						return f.c.PublishIssueComment(context.Background(), publicationAuthority, op, 7, w.Plans[0].Markdown)
					}
				}
				op := PublicationOperation{Key: "stable-secret-key"}
				got, err := publish(op)
				if err != nil || got.ID == 0 || f.posts != 1 {
					t.Fatalf("first: %+v %v posts=%d", got, err, f.posts)
				}
				op.ReconcileOnly = true
				again, err := publish(op)
				if err != nil || again != got || f.posts != 1 {
					t.Fatalf("recovery: %+v %v posts=%d", again, err, f.posts)
				}
				body := ""
				if kind == "comment" {
					body = f.comments[0].Body
				} else {
					body = f.issues[0].Body
				}
				if strings.Contains(body, op.Key) {
					t.Error("raw operation key published")
				}
				if kind == "feature" && (!strings.Contains(body, "Depends on one: #8") || !strings.Contains(body, "acceptanceCriteria") || !strings.Contains(body, "timeoutSeconds")) {
					t.Error("missing feature dependency or check specification")
				}
				w.Plans[0].Markdown += " changed"
				if kind == "feature" {
					w.Plans[0].Features[1].Title += " changed"
				}
				if _, err = publish(op); !errors.Is(err, ErrPublicationConflict) || f.posts != 1 {
					t.Fatalf("conflict: %v posts=%d", err, f.posts)
				}
			})
		}
	}
}

func TestPublicationAuthorityAndApproval(t *testing.T) {
	f := newPublicationFixture(t)
	for _, a := range []WriteAuthority{{}, {AccountID: "account", RepositoryID: "2"}, {AccountID: "unknown", RepositoryID: "2", IssuesWrite: true}, {AccountID: "account", RepositoryID: "3", IssuesWrite: true}, {AccountID: "account", RepositoryID: "02", IssuesWrite: true}} {
		if _, err := f.c.PublishMasterIssue(context.Background(), a, PublicationOperation{Key: "x"}, approvedWork()); !errors.Is(err, ErrNotAllowed) {
			t.Fatalf("authority: %v", err)
		}
	}
	for _, change := range []func(*factory.Work){
		func(w *factory.Work) { w.ApprovedPlanRevision = 0 },
		func(w *factory.Work) { w.ApprovedPlanRevision = 2 },
		func(w *factory.Work) { w.Plans[0].InputRevision = w.Revision },
		func(w *factory.Work) { w.BaseSHA = strings.Repeat("b", 40) },
		func(w *factory.Work) { w.Plans[0].Questions = []string{"unanswered"} },
		func(w *factory.Work) { w.Plans[0].Features[0].DependsOn = []string{"two"} },
	} {
		w := approvedWork()
		change(&w)
		before := f.calls
		if _, err := f.c.PublishMasterIssue(context.Background(), publicationAuthority, PublicationOperation{Key: "x"}, w); !errors.Is(err, ErrInvalidPublication) || f.calls != before {
			t.Fatalf("approval: %v", err)
		}
	}
	for _, permissions := range []string{"", "read"} {
		f.permission = permissions
		if _, err := f.c.PublishIssueComment(context.Background(), publicationAuthority, PublicationOperation{Key: "x"}, 7, "body"); !errors.Is(err, ErrNotAllowed) {
			t.Fatalf("permission: %v", err)
		}
	}
	f.permission = "write"
	f.extraRepo = true
	if _, err := f.c.PublishIssueComment(context.Background(), publicationAuthority, PublicationOperation{Key: "x"}, 7, "body"); !errors.Is(err, ErrNotAllowed) {
		t.Fatalf("scope: %v", err)
	}
	f.extraRepo = false
	f.revoked = true
	if _, err := f.c.PublishIssueComment(context.Background(), publicationAuthority, PublicationOperation{Key: "x"}, 7, "body"); !errors.Is(err, ErrNotAllowed) {
		t.Fatalf("revocation: %v", err)
	}
	if f.posts != 0 {
		t.Fatal("unauthorized write")
	}
}

func TestPublicationUnknownAndRedaction(t *testing.T) {
	f := newPublicationFixture(t)
	f.postMode = "absent"
	op := PublicationOperation{Key: "x"}
	_, err := f.c.PublishIssueComment(context.Background(), publicationAuthority, op, 7, "body")
	if !errors.Is(err, ErrPublicationUncertain) || strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), f.server.URL) {
		t.Fatalf("unknown: %v", err)
	}
	op.ReconcileOnly = true
	_, err = f.c.PublishIssueComment(context.Background(), publicationAuthority, op, 7, "body")
	if !errors.Is(err, ErrPublicationUncertain) || f.posts != 1 {
		t.Fatalf("unknown retry: %v posts=%d", err, f.posts)
	}
	f.listStatus = 403
	_, err = f.c.PublishIssueComment(context.Background(), publicationAuthority, op, 7, "body")
	var api *APIError
	if !errors.As(err, &api) || api.StatusCode != 403 || strings.Contains(err.Error(), "secret") {
		t.Fatalf("API redaction: %v", err)
	}
}

func TestPublicationRejectsDuplicateEditedAndMovedMarkers(t *testing.T) {
	for _, mode := range []string{"duplicate", "edit", "move", "kind", "pr"} {
		t.Run(mode, func(t *testing.T) {
			f := newPublicationFixture(t)
			op := PublicationOperation{Key: "x"}
			if _, err := f.c.PublishIssueComment(context.Background(), publicationAuthority, op, 7, "body"); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "duplicate":
				f.comments = append(f.comments, f.comments[0])
			case "edit":
				f.comments[0].Body = "edited\n" + f.comments[0].Body
			case "move":
				f.comments[0].IssueURL = f.server.URL + "/repos/org/repo/issues/8"
			case "kind":
				f.issues = append(f.issues, f.comments[0])
				f.comments = []publicationRecord{}
			case "pr":
				f.issues = append(f.issues, f.comments[0])
				f.issues[0].PullRequest = json.RawMessage(`{}`)
				f.comments = []publicationRecord{}
			}
			if _, err := f.c.PublishIssueComment(context.Background(), publicationAuthority, op, 7, "body"); !errors.Is(err, ErrPublicationConflict) || f.posts != 1 {
				t.Fatalf("conflict: %v", err)
			}
		})
	}
}

func TestPublicationInputAndPRTarget(t *testing.T) {
	f := newPublicationFixture(t)
	for _, op := range []PublicationOperation{{}, {Key: strings.Repeat("a", 201)}} {
		if _, err := f.c.PublishIssueComment(context.Background(), publicationAuthority, op, 7, "body"); !errors.Is(err, ErrInvalidPublication) {
			t.Fatal(err)
		}
	}
	for _, body := range []string{"", strings.Repeat("a", 60001), markerPrefix + "spoof"} {
		if _, err := f.c.PublishIssueComment(context.Background(), publicationAuthority, PublicationOperation{Key: "x"}, 7, body); !errors.Is(err, ErrInvalidPublication) {
			t.Fatal(err)
		}
	}
	for _, deps := range []map[string]int64{nil, {"other": 8}, {"one": 0}, {"one": 7}, {"one": 8, "extra": 9}} {
		if _, err := f.c.PublishFeatureIssue(context.Background(), publicationAuthority, PublicationOperation{Key: "x"}, approvedWork(), "two", 7, deps); !errors.Is(err, ErrInvalidPublication) {
			t.Fatal(err)
		}
	}
	f.pr = true
	if _, err := f.c.PublishIssueComment(context.Background(), publicationAuthority, PublicationOperation{Key: "x"}, 7, "body"); !errors.Is(err, ErrInvalidPublication) || f.posts != 0 {
		t.Fatalf("PR target: %v", err)
	}
}

func TestPublicationPaginationAndRedirects(t *testing.T) {
	f := newPublicationFixture(t)
	query := "direction=asc&page=1&per_page=100&sort=created&state=all"
	for _, link := range []string{
		`<https://evil.invalid/repos/org/repo/issues?direction=asc&page=2&per_page=100&sort=created&state=all>; rel="next"`,
		`<` + f.server.URL + `/repos/org/other/issues?page=2&per_page=100>; rel="next"`,
		`<` + f.server.URL + `/repos/org/repo/issues?` + query + `>; rel="next"`,
		`<` + f.server.URL + `/repos/org/repo/issues?page=3&per_page=100>; rel="next"`,
		`broken; rel="next"`,
	} {
		f.link = link
		if _, err := f.c.PublishMasterIssue(context.Background(), publicationAuthority, PublicationOperation{Key: "x"}, approvedWork()); err == nil || f.posts != 0 {
			t.Fatalf("unsafe pagination: %v", err)
		}
	}
	f.link = ""
	f.listStatus = 302
	if _, err := f.c.PublishMasterIssue(context.Background(), publicationAuthority, PublicationOperation{Key: "x"}, approvedWork()); err == nil || f.posts != 0 {
		t.Fatalf("redirect: %v", err)
	}
}

func TestPublicationBoundedCompleteScan(t *testing.T) {
	f := newPublicationFixture(t)
	op := PublicationOperation{Key: "page-two"}
	got, err := f.c.PublishMasterIssue(context.Background(), publicationAuthority, op, approvedWork())
	if err != nil {
		t.Fatal(err)
	}
	f.pages = 2
	again, err := f.c.PublishMasterIssue(context.Background(), publicationAuthority, op, approvedWork())
	if err != nil || got != again || f.posts != 1 {
		t.Fatalf("second page recovery: %+v %v", again, err)
	}
	f.pages = publicationPageLimit + 1
	before := f.calls
	_, err = f.c.PublishMasterIssue(context.Background(), publicationAuthority, PublicationOperation{Key: "unseen"}, approvedWork())
	if err == nil || !strings.Contains(err.Error(), "pagination limit") || f.posts != 1 || f.calls-before != publicationPageLimit+4 {
		t.Fatalf("bounded scan: %v calls=%d", err, f.calls-before)
	}
	f.pages = 0
	f.issues = nil
	_, err = f.c.PublishMasterIssue(context.Background(), publicationAuthority, PublicationOperation{Key: "unseen"}, approvedWork())
	if err == nil || f.posts != 1 {
		t.Fatalf("null listing allowed write: %v", err)
	}
}

func TestPublicationRedirectDoesNotForwardToken(t *testing.T) {
	f := newPublicationFixture(t)
	f.listStatus = 307
	f.link = f.server.URL + "/redirect-target"
	before := f.calls
	_, err := f.c.PublishMasterIssue(context.Background(), publicationAuthority, PublicationOperation{Key: "x"}, approvedWork())
	var api *APIError
	if !errors.As(err, &api) || api.StatusCode != 307 || f.calls-before != 5 || f.posts != 0 {
		t.Fatalf("redirect followed: %v calls=%d", err, f.calls-before)
	}
}
