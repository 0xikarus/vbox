package githubapp

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/0xikarus/vmbox-service/internal/factory"
)

var (
	ErrPublicationConflict  = errors.New("githubapp: publication operation conflicts with existing payload")
	ErrPublicationUncertain = errors.New("githubapp: publication outcome unknown; reconcile before another write")
	ErrInvalidPublication   = errors.New("githubapp: invalid publication")
)

// WriteAuthority is a trusted caller assertion, separate from plan approval.
// It authorizes issue writes to exactly one repository for one controller account.
// Never construct it directly from untrusted request fields.
type WriteAuthority struct {
	AccountID    string
	RepositoryID string
	IssuesWrite  bool
}

// PublicationOperation keys are stable and unique across all publication kinds
// within a repository. ReconcileOnly MUST be set for an unresolved prior attempt,
// including after a caller crash. An absent marker does not prove no write occurred.
type PublicationOperation struct {
	Key           string
	ReconcileOnly bool
}

type Publication struct {
	ID     int64 `json:"id"`
	Number int64 `json:"number"` // Issue number; zero for comments.
}

type publicationRecord struct {
	Publication
	Title       string          `json:"title"`
	Body        string          `json:"body"`
	IssueURL    string          `json:"issue_url"`
	PullRequest json.RawMessage `json:"pull_request,omitempty"`
}

func isPullRequest(r publicationRecord) bool {
	return len(r.PullRequest) != 0 && string(r.PullRequest) != "null"
}

type publicationPayload struct {
	Kind  string
	Title string
	Body  string
	Issue int64
}

func approvedPlan(a WriteAuthority, w factory.Work) (factory.Plan, error) {
	if !a.IssuesWrite || a.AccountID == "" || a.RepositoryID == "" || a.RepositoryID != w.RepositoryID {
		return factory.Plan{}, ErrNotAllowed
	}
	if w.ID == "" || w.ApprovedPlanRevision < 1 || len(w.Plans) == 0 {
		return factory.Plan{}, ErrInvalidPublication
	}
	p := w.Plans[len(w.Plans)-1]
	if p.Revision != w.ApprovedPlanRevision || p.BaseSHA != w.BaseSHA || !shaPattern.MatchString(p.BaseSHA) || p.InputRevision < 1 || p.InputRevision >= w.Revision || p.ValidateApproval() != nil {
		return factory.Plan{}, ErrInvalidPublication
	}
	return p, nil
}

// PublishMasterIssue publishes the approved immutable plan, including its DAG and
// verification checks. Caller must load Work from trusted durable approval state.
func (c *Client) PublishMasterIssue(ctx context.Context, a WriteAuthority, op PublicationOperation, w factory.Work) (Publication, error) {
	p, err := approvedPlan(a, w)
	if err != nil {
		return Publication{}, err
	}
	spec, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return Publication{}, ErrInvalidPublication
	}
	body := fmt.Sprintf("Work: %s\nApproved plan revision: %d\nBase SHA: %s\n\n%s\n\n## Approved plan specification\n\n%s", w.ID, p.Revision, p.BaseSHA, p.Markdown, string(spec))
	return c.publish(ctx, a, op, publicationPayload{Kind: "master", Title: "Factory plan: " + w.ID, Body: body})
}

// PublishFeatureIssue publishes an approved feature with repository-local master
// and dependency references. Dependencies must have been published first.
func (c *Client) PublishFeatureIssue(ctx context.Context, a WriteAuthority, op PublicationOperation, w factory.Work, featureID string, master int64, dependencies map[string]int64) (Publication, error) {
	p, err := approvedPlan(a, w)
	if err != nil {
		return Publication{}, err
	}
	if master <= 0 {
		return Publication{}, ErrInvalidPublication
	}
	for _, f := range p.Features {
		if f.ID != featureID {
			continue
		}
		if len(dependencies) != len(f.DependsOn) {
			return Publication{}, ErrInvalidPublication
		}
		body := fmt.Sprintf("Work: %s\nApproved plan revision: %d\nBase SHA: %s\nMaster: #%d\n", w.ID, p.Revision, p.BaseSHA, master)
		for _, dep := range f.DependsOn {
			if dependencies[dep] <= 0 || dependencies[dep] == master {
				return Publication{}, ErrInvalidPublication
			}
			body += fmt.Sprintf("Depends on %s: #%d\n", dep, dependencies[dep])
		}
		spec, _ := json.MarshalIndent(f, "", "  ")
		body += "\n## Approved feature specification\n\n" + string(spec)
		return c.publish(ctx, a, op, publicationPayload{Kind: "feature", Title: f.Title, Body: body})
	}
	return Publication{}, ErrInvalidPublication
}

// PublishIssueComment only accepts issue numbers, never pull requests or URLs.
func (c *Client) PublishIssueComment(ctx context.Context, a WriteAuthority, op PublicationOperation, issue int64, body string) (Publication, error) {
	if issue <= 0 {
		return Publication{}, ErrInvalidPublication
	}
	return c.publish(ctx, a, op, publicationPayload{Kind: "comment", Issue: issue, Body: body})
}

func (c *Client) issueScope(ctx context.Context, a WriteAuthority) (factory.Repository, accessToken, error) {
	fail := func(err error) (factory.Repository, accessToken, error) {
		return factory.Repository{}, accessToken{}, err
	}
	id, err := strconv.ParseInt(a.RepositoryID, 10, 64)
	if !a.IssuesWrite || a.AccountID == "" || err != nil || id <= 0 || strconv.FormatInt(id, 10) != a.RepositoryID {
		return fail(ErrNotAllowed)
	}
	repos, err := c.List(ctx, a.AccountID)
	if err != nil {
		return fail(err)
	}
	for _, r := range repos {
		if r.ID != a.RepositoryID {
			continue
		}
		jwt, err := c.jwt()
		if err != nil {
			return fail(err)
		}
		var token struct {
			accessToken
			Permissions map[string]string `json:"permissions"`
		}
		_, err = c.request(ctx, "POST", fmt.Sprintf("/app/installations/%d/access_tokens", r.InstallationID), jwt,
			map[string]any{"repository_ids": []int64{id}, "permissions": map[string]string{"metadata": "read", "issues": "write"}}, &token)
		if err != nil {
			return fail(err)
		}
		if token.Token == "" || !token.ExpiresAt.After(time.Now().Add(time.Minute)) || token.Permissions["issues"] != "write" {
			return fail(ErrNotAllowed)
		}
		visible, err := c.repositories(ctx, token.accessToken, r.InstallationID)
		if err != nil {
			return fail(err)
		}
		if len(visible) != 1 || visible[0].ID != a.RepositoryID {
			return fail(ErrNotAllowed)
		}
		// Reject dot segments even if an upstream response passes the name regexp.
		for _, part := range strings.Split(visible[0].FullName, "/") {
			if part == "." || part == ".." {
				return fail(ErrNotAllowed)
			}
		}
		return visible[0], token.accessToken, nil
	}
	return fail(ErrNotAllowed)
}

const markerPrefix = "<!-- vmbox-factory-publication:v1:"
const publicationPageLimit = 100

func (c *Client) publish(ctx context.Context, a WriteAuthority, op PublicationOperation, p publicationPayload) (Publication, error) {
	if strings.TrimSpace(op.Key) == "" || len(op.Key) > 200 || strings.TrimSpace(p.Body) == "" || len(p.Body) > 60000 || len(p.Title) > 256 || strings.Contains(p.Body, markerPrefix) || strings.Contains(p.Title, markerPrefix) {
		return Publication{}, ErrInvalidPublication
	}
	repo, token, err := c.issueScope(ctx, a)
	if err != nil {
		return Publication{}, err
	}
	root := "/repos/" + repo.FullName + "/issues"
	key := fmt.Sprintf("%x", sha256.Sum256([]byte(op.Key)))
	data, _ := json.Marshal(p)
	prefix := markerPrefix + key + ":"
	body := p.Body + "\n\n" + prefix + fmt.Sprintf("%x", sha256.Sum256(data)) + " -->"
	path := root
	request := map[string]string{"title": p.Title, "body": body}
	if p.Kind == "comment" {
		path += "/" + strconv.FormatInt(p.Issue, 10)
		var target publicationRecord
		if _, err := c.request(ctx, "GET", path, token.Token, nil, &target); err != nil {
			return Publication{}, err
		}
		if target.ID <= 0 || target.Number != p.Issue || isPullRequest(target) {
			return Publication{}, ErrInvalidPublication
		}
		path += "/comments"
		delete(request, "title")
	}
	scan := func() (Publication, bool, error) { return c.findPublication(ctx, token, root, prefix, body, p) }
	prior, found, err := scan()
	if err != nil || found {
		return prior, err
	}
	if op.ReconcileOnly {
		return Publication{}, ErrPublicationUncertain
	}
	var result publicationRecord
	_, postErr := c.request(ctx, "POST", path, token.Token, request, &result)
	if postErr == nil && result.ID > 0 && result.Body == body && ((p.Kind == "comment" && result.IssueURL == c.base+root+"/"+strconv.FormatInt(p.Issue, 10)) || (p.Kind != "comment" && result.Number > 0 && result.Title == p.Title && !isPullRequest(result))) {
		return result.Publication, nil
	}
	// Never repeat a POST. A read can recover a committed write whose response was
	// lost, truncated, or a proxy error. Failed/absent reconciliation stays unknown.
	prior, found, err = scan()
	if err != nil {
		return Publication{}, errors.Join(ErrPublicationUncertain, err, postErr)
	}
	if found {
		return prior, nil
	}
	return Publication{}, errors.Join(ErrPublicationUncertain, postErr)
}

// Scan both repository-wide collections so keys cannot silently move between
// issue kinds or comment targets. Closed issues are included; PRs are excluded.
func (c *Client) findPublication(ctx context.Context, token accessToken, root, prefix, body string, p publicationPayload) (Publication, bool, error) {
	var found Publication
	for _, comments := range []bool{false, true} {
		path := root
		query := url.Values{"per_page": {"100"}, "sort": {"created"}, "direction": {"asc"}}
		if comments {
			path += "/comments"
		} else {
			query.Set("state", "all")
		}
		for page := 1; ; page++ {
			if page > publicationPageLimit {
				return Publication{}, false, errors.New("githubapp: publication pagination limit exceeded")
			}
			if !token.ExpiresAt.After(time.Now().Add(time.Minute)) {
				return Publication{}, false, errors.New("githubapp: installation token expired")
			}
			query.Set("page", strconv.Itoa(page))
			var records []publicationRecord
			headers, err := c.request(ctx, "GET", path+"?"+query.Encode(), token.Token, nil, &records)
			if err != nil {
				return Publication{}, false, err
			}
			if records == nil || len(records) > 100 {
				return Publication{}, false, errors.New("githubapp: invalid publication listing")
			}
			for _, r := range records {
				if !strings.Contains(r.Body, prefix) {
					continue
				}
				if r.ID <= 0 || found.ID != 0 || r.Body != body || comments != (p.Kind == "comment") || (!comments && (r.Title != p.Title || r.Number <= 0 || isPullRequest(r))) || (comments && r.IssueURL != c.base+root+"/"+strconv.FormatInt(p.Issue, 10)) {
					return Publication{}, false, ErrPublicationConflict
				}
				found = r.Publication
			}
			next, err := c.publicationNext(headers.Values("Link"), path, query, page)
			if err != nil {
				return Publication{}, false, err
			}
			if !next {
				break
			}
		}
	}
	return found, found.ID != 0, nil
}

func (c *Client) publicationNext(links []string, path string, query url.Values, page int) (bool, error) {
	next := false
	for _, link := range strings.Split(strings.Join(links, ","), ",") {
		if strings.TrimSpace(link) == "" {
			continue
		}
		parts := strings.Split(link, ";")
		if len(parts) != 2 {
			return false, errors.New("githubapp: invalid pagination")
		}
		switch strings.TrimSpace(parts[1]) {
		case `rel="first"`, `rel="last"`, `rel="prev"`:
			continue
		case `rel="next"`:
		default:
			return false, errors.New("githubapp: invalid pagination")
		}
		raw := strings.TrimSpace(parts[0])
		if !strings.HasPrefix(raw, "<") || !strings.HasSuffix(raw, ">") {
			return false, errors.New("githubapp: invalid pagination")
		}
		u, err := url.Parse(raw[1 : len(raw)-1])
		base, _ := url.Parse(c.base + path)
		if err != nil {
			return false, errors.New("githubapp: invalid pagination")
		}
		u = base.ResolveReference(u)
		expected := query.Encode()
		q, err := url.ParseQuery(u.RawQuery)
		if err != nil {
			return false, errors.New("githubapp: invalid pagination")
		}
		q.Set("page", strconv.Itoa(page))
		if next || u.Scheme != base.Scheme || u.Host != base.Host || u.Path != base.Path || u.User != nil || u.Fragment != "" || u.Query().Get("page") != strconv.Itoa(page+1) || len(u.Query()["page"]) != 1 || q.Encode() != expected {
			return false, errors.New("githubapp: invalid pagination")
		}
		next = true
	}
	return next, nil
}
