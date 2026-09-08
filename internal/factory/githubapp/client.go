// Package githubapp provides account-allowlisted GitHub App repository access.
package githubapp

import (
	"bytes"
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
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/0xikarus/vmbox-service/internal/factory"
)

const apiVersion = "2026-03-10"

var ErrNotAllowed = errors.New("githubapp: repository or account not allowed")

type Config struct {
	AppID      string
	PrivateKey []byte
	// Installations is a trusted controller-account allowlist, copied by New.
	Installations map[string][]int64
	HTTP          *http.Client
	BaseURL       string
}

type Client struct {
	appID         string
	key           *rsa.PrivateKey
	installations map[string][]int64
	http          *http.Client
	base          string
}

var _ factory.RepositoryBackend = (*Client)(nil)

// New accepts PKCS#1 or PKCS#8 PEM RSA keys. HTTP transports are trusted;
// redirects and cookie jars are disabled on a private copy of the HTTP client.
func New(cfg Config) (*Client, error) {
	if strings.TrimSpace(cfg.AppID) == "" {
		return nil, errors.New("githubapp: app ID required")
	}
	block, rest := pem.Decode(cfg.PrivateKey)
	if block == nil || len(bytes.TrimSpace(rest)) != 0 {
		return nil, errors.New("githubapp: invalid PEM key")
	}
	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		parsed, e := x509.ParsePKCS8PrivateKey(block.Bytes)
		if e != nil {
			return nil, errors.New("githubapp: invalid RSA key")
		}
		key, _ = parsed.(*rsa.PrivateKey)
	}
	if key == nil || key.N.BitLen() < 2048 {
		return nil, errors.New("githubapp: RSA key must be at least 2048 bits")
	}
	if key.Validate() != nil {
		return nil, errors.New("githubapp: invalid RSA key")
	}
	base := cfg.BaseURL
	if base == "" {
		base = "https://api.github.com"
	}
	u, err := url.Parse(base)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return nil, errors.New("githubapp: invalid base URL")
	}
	// Plain HTTP is only for loopback test servers.
	ip := net.ParseIP(u.Hostname())
	if u.Scheme != "https" && !(u.Scheme == "http" && ip != nil && ip.IsLoopback()) {
		return nil, errors.New("githubapp: HTTPS required")
	}
	h := http.Client{Timeout: 30 * time.Second}
	if cfg.HTTP != nil {
		h = *cfg.HTTP
		if h.Timeout == 0 {
			h.Timeout = 30 * time.Second
		}
	}
	h.Jar = nil
	h.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	c := &Client{appID: cfg.AppID, key: key, http: &h, base: strings.TrimRight(u.String(), "/"), installations: map[string][]int64{}}
	for account, ids := range cfg.Installations {
		if strings.TrimSpace(account) == "" {
			return nil, errors.New("githubapp: empty account")
		}
		seen := map[int64]bool{}
		for _, id := range ids {
			if id <= 0 {
				return nil, errors.New("githubapp: invalid installation ID")
			}
			if !seen[id] {
				c.installations[account] = append(c.installations[account], id)
				seen[id] = true
			}
		}
	}
	return c, nil
}

func (c *Client) jwt() (string, error) {
	now := time.Now()
	payload, _ := json.Marshal(map[string]any{"iss": c.appID, "iat": now.Add(-time.Minute).Unix(), "exp": now.Add(9 * time.Minute).Unix()})
	enc := base64.RawURLEncoding.EncodeToString
	unsigned := enc([]byte(`{"alg":"RS256","typ":"JWT"}`)) + "." + enc(payload)
	digest := sha256.Sum256([]byte(unsigned))
	sig, err := rsa.SignPKCS1v15(rand.Reader, c.key, crypto.SHA256, digest[:])
	if err != nil {
		return "", errors.New("githubapp: signing failed")
	}
	return unsigned + "." + enc(sig), nil
}

// APIError deliberately excludes response bodies, URLs and credentials.
// Callers may retry transient statuses (429, 5xx) with their own bounded policy.
type APIError struct{ StatusCode int }

func (e *APIError) Error() string { return fmt.Sprintf("githubapp: HTTP status %d", e.StatusCode) }

func (c *Client) request(ctx context.Context, method, path, token string, body, out any) (http.Header, error) {
	var data []byte
	var err error
	if body != nil {
		data, err = json.Marshal(body)
		if err != nil {
			return nil, errors.New("githubapp: invalid request")
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, bytes.NewReader(data))
	if err != nil {
		return nil, errors.New("githubapp: invalid request")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", apiVersion)
	req.Header.Set("User-Agent", "vmbox-factory-githubapp")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("githubapp: transport failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &APIError{StatusCode: resp.StatusCode}
	}
	data, err = io.ReadAll(io.LimitReader(resp.Body, (8<<20)+1))
	if err != nil || len(data) > 8<<20 {
		return nil, errors.New("githubapp: invalid response")
	}
	if err = json.Unmarshal(data, out); err != nil {
		return nil, errors.New("githubapp: invalid JSON response")
	}
	return resp.Header, nil
}

type accessToken struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

func (c *Client) mint(ctx context.Context, installation, repository int64, write bool) (accessToken, error) {
	jwt, err := c.jwt()
	if err != nil {
		return accessToken{}, err
	}
	permissions := map[string]string{"metadata": "read"}
	body := map[string]any{"permissions": permissions}
	if repository != 0 {
		body["repository_ids"] = []int64{repository}
		permissions["contents"] = "read"
		if write {
			permissions["contents"] = "write"
		}
	}
	var result accessToken
	_, err = c.request(ctx, "POST", fmt.Sprintf("/app/installations/%d/access_tokens", installation), jwt, body, &result)
	if err != nil {
		return accessToken{}, err
	}
	if result.Token == "" || !result.ExpiresAt.After(time.Now().Add(time.Minute)) {
		return accessToken{}, errors.New("githubapp: missing or expiring installation token")
	}
	return result, nil
}

type repository struct {
	ID            int64  `json:"id"`
	FullName      string `json:"full_name"`
	DefaultBranch string `json:"default_branch"`
}

var repoName = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

func (c *Client) repositories(ctx context.Context, token accessToken, installation int64) ([]factory.Repository, error) {
	out := []factory.Repository{}
	for page := 1; page <= 10000; {
		if !token.ExpiresAt.After(time.Now().Add(time.Minute)) {
			return nil, errors.New("githubapp: installation token expired")
		}
		var result struct {
			Repositories []repository `json:"repositories"`
		}
		path := "/installation/repositories"
		headers, err := c.request(ctx, "GET", path+"?per_page=100&page="+strconv.Itoa(page), token.Token, nil, &result)
		if err != nil {
			return nil, err
		}
		for _, r := range result.Repositories {
			if r.ID <= 0 || !repoName.MatchString(r.FullName) {
				return nil, errors.New("githubapp: invalid repository response")
			}
			out = append(out, factory.Repository{ID: strconv.FormatInt(r.ID, 10), FullName: r.FullName, DefaultBranch: r.DefaultBranch, InstallationID: installation})
		}
		next := 0
		for _, link := range strings.Split(strings.Join(headers.Values("Link"), ","), ",") {
			parts := strings.Split(link, ";")
			isNext := false
			for _, p := range parts[1:] {
				if strings.TrimSpace(p) == `rel="next"` {
					isNext = true
				}
			}
			if !isNext {
				continue
			}
			raw := strings.TrimSpace(parts[0])
			if !strings.HasPrefix(raw, "<") || !strings.HasSuffix(raw, ">") {
				return nil, errors.New("githubapp: invalid pagination")
			}
			u, e := url.Parse(raw[1 : len(raw)-1])
			base, _ := url.Parse(c.base + path)
			if e != nil {
				return nil, errors.New("githubapp: invalid pagination")
			}
			u = base.ResolveReference(u)
			q := u.Query()
			n, e := strconv.Atoi(q.Get("page"))
			if e != nil || n <= page || n > 10000 || next != 0 || u.Scheme != base.Scheme || u.Host != base.Host || u.Path != base.Path || u.User != nil || u.Fragment != "" || len(q) != 2 || len(q["page"]) != 1 || len(q["per_page"]) != 1 || q.Get("per_page") != "100" {
				return nil, errors.New("githubapp: invalid pagination")
			}
			next = n
		}
		if next == 0 {
			return out, nil
		}
		page = next
	}
	return nil, errors.New("githubapp: pagination limit exceeded")
}

// List uses fresh metadata-only installation tokens; visibility is never cached.
func (c *Client) List(ctx context.Context, accountID string) ([]factory.Repository, error) {
	ids := c.installations[accountID]
	if len(ids) == 0 {
		return nil, ErrNotAllowed
	}
	out := []factory.Repository{}
	seen := map[string]bool{}
	for _, id := range ids {
		token, err := c.mint(ctx, id, 0, false)
		if err != nil {
			return nil, err
		}
		repos, err := c.repositories(ctx, token, id)
		if err != nil {
			return nil, err
		}
		for _, r := range repos {
			if !seen[r.ID] {
				out = append(out, r)
				seen[r.ID] = true
			}
		}
	}
	return out, nil
}

func (c *Client) scoped(ctx context.Context, accountID, repositoryID string, write bool) (factory.Repository, accessToken, error) {
	id, err := strconv.ParseInt(repositoryID, 10, 64)
	if err != nil || id <= 0 || strconv.FormatInt(id, 10) != repositoryID {
		return factory.Repository{}, accessToken{}, ErrNotAllowed
	}
	repos, err := c.List(ctx, accountID)
	if err != nil {
		return factory.Repository{}, accessToken{}, err
	}
	for _, r := range repos {
		if r.ID != repositoryID {
			continue
		}
		token, err := c.mint(ctx, r.InstallationID, id, write)
		if err != nil {
			return factory.Repository{}, accessToken{}, err
		}
		// Recheck with the narrowed token, including revocation between discovery and mint.
		visible, err := c.repositories(ctx, token, r.InstallationID)
		if err != nil {
			return factory.Repository{}, accessToken{}, err
		}
		if len(visible) != 1 || visible[0].ID != repositoryID {
			return factory.Repository{}, accessToken{}, ErrNotAllowed
		}
		return visible[0], token, nil
	}
	return factory.Repository{}, accessToken{}, ErrNotAllowed
}

// RepositoryToken grants contents:read by default, or contents:write only when
// explicitly requested. It never grants workflow, administration or PR writes.
// Tokens are not cached. Callers must honor expiresAt and keep the token secret.
func (c *Client) RepositoryToken(ctx context.Context, accountID, repositoryID string, write bool) (string, time.Time, error) {
	_, token, err := c.scoped(ctx, accountID, repositoryID, write)
	return token.Token, token.ExpiresAt, err
}

var shaPattern = regexp.MustCompile(`^[0-9a-fA-F]{40}$`)

func validRef(ref string) bool {
	if ref == "" || len(ref) > 1024 || strings.ContainsAny(ref, " ~^:?*[\\\x00\r\n\t\x7f") || strings.Contains(ref, "..") || strings.Contains(ref, "@{") || ref == "@" {
		return false
	}
	for _, part := range strings.Split(ref, "/") {
		if part == "" || strings.HasPrefix(part, ".") || strings.HasSuffix(part, ".") || strings.HasSuffix(part, ".lock") {
			return false
		}
	}
	for _, r := range ref {
		if r < 32 {
			return false
		}
	}
	return true
}

// Resolve returns an immutable commit SHA. An empty ref selects the current
// default branch. GitHub validates that the ref exists within the scoped repo.
func (c *Client) Resolve(ctx context.Context, accountID, repositoryID, ref string) (factory.Repository, string, error) {
	if ref != "" && !validRef(ref) {
		return factory.Repository{}, "", errors.New("githubapp: invalid ref")
	}
	r, token, err := c.scoped(ctx, accountID, repositoryID, false)
	if err != nil {
		return factory.Repository{}, "", err
	}
	if ref == "" {
		ref = r.DefaultBranch
	}
	if !validRef(ref) {
		return factory.Repository{}, "", errors.New("githubapp: invalid ref")
	}
	var result struct {
		SHA string `json:"sha"`
	}
	_, err = c.request(ctx, "GET", "/repos/"+r.FullName+"/commits/"+url.PathEscape(ref), token.Token, nil, &result)
	if err != nil {
		return factory.Repository{}, "", err
	}
	if !shaPattern.MatchString(result.SHA) {
		return factory.Repository{}, "", errors.New("githubapp: invalid commit SHA")
	}
	return r, strings.ToLower(result.SHA), nil
}
