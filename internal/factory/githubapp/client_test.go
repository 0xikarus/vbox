package githubapp

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func testKey(t *testing.T) (*rsa.PrivateKey, []byte) {
	t.Helper()
	k, e := rsa.GenerateKey(rand.Reader, 2048)
	if e != nil {
		t.Fatal(e)
	}
	return k, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(k)})
}
func checkJWT(t *testing.T, auth string, key *rsa.PrivateKey) {
	t.Helper()
	p := strings.Split(strings.TrimPrefix(auth, "Bearer "), ".")
	if len(p) != 3 {
		t.Error("expected JWT authentication")
		return
	}
	dec := base64.RawURLEncoding.DecodeString
	header, _ := dec(p[0])
	var h map[string]string
	_ = json.Unmarshal(header, &h)
	if h["alg"] != "RS256" {
		t.Error("wrong algorithm")
	}
	payload, _ := dec(p[1])
	var claims struct {
		Iss      string
		Iat, Exp int64
	}
	_ = json.Unmarshal(payload, &claims)
	now := time.Now().Unix()
	if claims.Iss != "123" || claims.Iat > now-55 || claims.Iat < now-70 || claims.Exp <= now || claims.Exp > now+600 {
		t.Error("invalid JWT claims")
	}
	sig, _ := dec(p[2])
	hash := sha256.Sum256([]byte(p[0] + "." + p[1]))
	if rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, hash[:], sig) != nil {
		t.Error("invalid JWT signature")
	}
}

func TestScopedClient(t *testing.T) {
	key, pemKey := testKey(t)
	var revoked, raceRevoked bool
	var writes, scopedMints, pageTwo, calls int
	sha := strings.Repeat("a", 40)
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Accept") != "application/vnd.github+json" || r.Header.Get("X-GitHub-Api-Version") != apiVersion || r.Header.Get("User-Agent") == "" {
			t.Error("missing API headers")
		}
		if r.URL.User != nil || strings.Contains(r.URL.RawQuery, "token") || r.Header.Get("Cookie") != "" {
			t.Error("credential boundary violated")
		}
		auth := r.Header.Get("Authorization")
		switch {
		case strings.HasPrefix(r.URL.Path, "/app/installations/"):
			if r.Method != "POST" || r.Header.Get("Content-Type") != "application/json" {
				t.Error("invalid mint request")
			}
			checkJWT(t, auth, key)
			var body struct {
				RepositoryIDs []int64           `json:"repository_ids"`
				Permissions   map[string]string `json:"permissions"`
			}
			if json.NewDecoder(r.Body).Decode(&body) != nil {
				t.Error("invalid body")
			}
			token := "metadata-a"
			if r.URL.Path == "/app/installations/22/access_tokens" {
				token = "metadata-b"
			} else if r.URL.Path != "/app/installations/11/access_tokens" {
				t.Error("unexpected installation")
			}
			if body.Permissions["metadata"] != "read" {
				t.Error("metadata permission missing")
			}
			if len(body.RepositoryIDs) > 0 {
				scopedMints++
				if len(body.RepositoryIDs) != 1 || body.RepositoryIDs[0] != 2 || len(body.Permissions) != 2 {
					t.Error("token not scoped to one repository")
				}
				token = "scoped"
				if body.Permissions["contents"] == "write" {
					writes++
				} else if body.Permissions["contents"] != "read" {
					t.Error("unexpected contents permission")
				}
			} else if len(body.Permissions) != 1 {
				t.Error("discovery token has excess permissions")
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"token": token, "expires_at": time.Now().Add(time.Hour)})
		case r.URL.Path == "/installation/repositories":
			if r.Method != "GET" || r.URL.Query().Get("per_page") != "100" {
				t.Error("invalid listing request")
			}
			repos := []repository{}
			switch auth {
			case "Bearer metadata-a":
				if r.URL.Query().Get("page") == "1" {
					repos = append(repos, repository{ID: 1, FullName: "org/one", DefaultBranch: "main"})
					w.Header().Set("Link", fmt.Sprintf(`<%s/installation/repositories?per_page=100&page=2>; rel="next", <%s/installation/repositories?per_page=100&page=2>; rel="last"`, server.URL, server.URL))
				} else {
					pageTwo++
					if !revoked {
						repos = append(repos, repository{ID: 2, FullName: "org/two", DefaultBranch: "main"})
					}
				}
			case "Bearer metadata-b":
				repos = append(repos, repository{ID: 3, FullName: "else/three", DefaultBranch: "main"})
			case "Bearer scoped":
				if !raceRevoked {
					repos = append(repos, repository{ID: 2, FullName: "org/two", DefaultBranch: "main"})
				}
			default:
				t.Error("JWT or unknown token sent to repository listing")
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"repositories": repos})
		case strings.HasPrefix(r.URL.Path, "/repos/org/two/commits/"):
			if auth != "Bearer scoped" {
				t.Error("commit request lacks restricted token")
			}
			if strings.HasSuffix(r.URL.Path, "missing") {
				w.WriteHeader(404)
				return
			}
			if r.URL.EscapedPath() != "/repos/org/two/commits/feature%2Ftest" && r.URL.Path != "/repos/org/two/commits/main" {
				t.Error("unexpected ref path")
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"sha": sha})
		default:
			t.Error("unexpected endpoint")
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	allow := map[string][]int64{"a": {11}, "b": {22}}
	c, e := New(Config{AppID: "123", PrivateKey: pemKey, Installations: allow, BaseURL: server.URL})
	if e != nil {
		t.Fatal(e)
	}
	allow["a"][0] = 22 // Client must own its allowlist snapshot.
	ctx := context.Background()
	list, e := c.List(ctx, "a")
	if e != nil || len(list) != 2 || pageTwo != 1 {
		t.Fatalf("pagination: count=%d err=%v", len(list), e)
	}
	before := calls
	if _, e = c.List(ctx, "unknown"); !errors.Is(e, ErrNotAllowed) || calls != before {
		t.Fatal("unknown account made requests")
	}
	if _, _, e = c.RepositoryToken(ctx, "b", "2", true); !errors.Is(e, ErrNotAllowed) || scopedMints != 0 {
		t.Fatal("cross-account access")
	}
	r, s, e := c.Resolve(ctx, "a", "2", "feature/test")
	if e != nil || s != sha || r.InstallationID != 11 {
		t.Fatalf("resolve: %v", e)
	}
	if _, s, e = c.Resolve(ctx, "a", "2", ""); e != nil || s != sha {
		t.Fatalf("default branch: %v", e)
	}
	if writes != 0 {
		t.Fatal("implicit write access")
	}
	token, expiry, e := c.RepositoryToken(ctx, "a", "2", true)
	if e != nil || token == "" || !expiry.After(time.Now()) || writes != 1 {
		t.Fatal("explicit write failed")
	}
	if _, _, e = c.Resolve(ctx, "a", "2", "missing"); e == nil {
		t.Fatal("missing ref accepted")
	}
	before = calls
	for _, ref := range []string{"../main", "main?token=x", "a\n", "a@{b", "a.lock", "a//b"} {
		if _, _, e = c.Resolve(ctx, "a", "2", ref); e == nil {
			t.Error("bad ref accepted")
		}
	}
	if calls != before {
		t.Error("invalid refs made requests")
	}
	revoked = true
	before = scopedMints
	if _, _, e = c.RepositoryToken(ctx, "a", "2", false); !errors.Is(e, ErrNotAllowed) || scopedMints != before {
		t.Fatal("revocation ignored")
	}
	revoked = false
	raceRevoked = true
	if token, _, e = c.RepositoryToken(ctx, "a", "2", false); !errors.Is(e, ErrNotAllowed) || token != "" {
		t.Fatal("mint-time revocation ignored")
	}
}

func TestFailuresAndCredentialBoundaries(t *testing.T) {
	_, key := testKey(t)
	var leaked atomic.Int32
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked.Add(1) }))
	defer sink.Close()
	for _, mode := range []string{"redirect-jwt", "redirect-token", "link-host", "link-path", "link-loop", "expired", "near-expiry", "empty-token", "401", "403", "404", "429", "500", "503", "malformed", "bad-sha", "scope-broadened"} {
		t.Run(mode, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasPrefix(r.URL.Path, "/app/") {
					if mode == "redirect-jwt" {
						http.Redirect(w, r, sink.URL, 307)
						return
					}
					expiry := time.Now().Add(time.Hour)
					token := "secret-fixture"
					if mode == "expired" {
						expiry = time.Now().Add(-time.Second)
					}
					if mode == "near-expiry" {
						expiry = time.Now().Add(30 * time.Second)
					}
					if mode == "empty-token" {
						token = ""
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"token": token, "expires_at": expiry})
					return
				}
				switch mode {
				case "redirect-token":
					http.Redirect(w, r, sink.URL, 302)
					return
				case "link-host":
					w.Header().Set("Link", `<`+sink.URL+`/installation/repositories?page=2&per_page=100>; rel="next"`)
				case "link-path":
					w.Header().Set("Link", `</other?page=2&per_page=100>; rel="next"`)
				case "link-loop":
					w.Header().Set("Link", `</installation/repositories?page=1&per_page=100>; rel="next"`)
				case "401", "403", "404", "429", "500", "503":
					var status int
					fmt.Sscan(mode, &status)
					w.WriteHeader(status)
					fmt.Fprint(w, "secret-fixture")
					return
				case "malformed":
					fmt.Fprint(w, `{"secret-fixture":`)
					return
				case "bad-sha":
					if strings.HasPrefix(r.URL.Path, "/repos/") {
						fmt.Fprint(w, `{"sha":"main"}`)
						return
					}
				}
				repos := []repository{{ID: 2, FullName: "org/two", DefaultBranch: "main"}}
				if mode == "scope-broadened" {
					repos = append(repos, repository{ID: 3, FullName: "org/three"})
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"repositories": repos})
			}))
			defer server.Close()
			custom := server.Client()
			custom.CheckRedirect = func(*http.Request, []*http.Request) error { return nil }
			c, e := New(Config{AppID: "123", PrivateKey: key, Installations: map[string][]int64{"a": {11}}, BaseURL: server.URL, HTTP: custom})
			if e != nil {
				t.Fatal(e)
			}
			_, _, e = c.Resolve(context.Background(), "a", "2", "main")
			if e == nil || strings.Contains(e.Error(), "secret-fixture") || strings.Contains(e.Error(), server.URL) {
				t.Fatal("failure not safely propagated")
			}
			if mode == "429" || mode == "500" || mode == "503" {
				var ae *APIError
				if !errors.As(e, &ae) {
					t.Error("transient status not preserved")
				}
			}
		})
	}
	if leaked.Load() != 0 {
		t.Fatal("credentials crossed redirect or pagination boundary")
	}
}

func TestConfigAndCancellation(t *testing.T) {
	k, key := testKey(t)
	for _, cfg := range []Config{{}, {AppID: "1", PrivateKey: []byte("bad")}, {AppID: "1", PrivateKey: key, BaseURL: "http://github.com"}, {AppID: "1", PrivateKey: key, BaseURL: "https://user:secret@github.com"}, {AppID: "1", PrivateKey: key, Installations: map[string][]int64{"a": {-1}}}} {
		if _, e := New(cfg); e == nil {
			t.Error("invalid configuration accepted")
		}
	}
	der, e := x509.MarshalPKCS8PrivateKey(k)
	if e != nil {
		t.Fatal(e)
	}
	c, e := New(Config{AppID: "1", PrivateKey: pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), Installations: map[string][]int64{"a": {11}}})
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e = c.List(ctx, "a"); !errors.Is(e, context.Canceled) {
		t.Fatalf("cancellation: %v", e)
	}
}

func TestMintFailures(t *testing.T) {
	_, key := testKey(t)
	for _, status := range []int{401, 403, 404, 422, 429, 502, 503} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if r.URL.Path != "/app/installations/11/access_tokens" {
					t.Error("continued after mint failure")
				}
				w.WriteHeader(status)
				fmt.Fprint(w, "private-key-or-token-must-not-escape")
			}))
			defer server.Close()
			c, e := New(Config{AppID: "123", PrivateKey: key, Installations: map[string][]int64{"a": {11}}, BaseURL: server.URL})
			if e != nil {
				t.Fatal(e)
			}
			token, expiry, e := c.RepositoryToken(context.Background(), "a", "2", true)
			var ae *APIError
			if !errors.As(e, &ae) || ae.StatusCode != status || token != "" || !expiry.IsZero() || requests != 1 {
				t.Fatal("mint failure not propagated safely")
			}
			if strings.Contains(e.Error(), "private-key") {
				t.Fatal("upstream body leaked")
			}
		})
	}
}
