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
)

// PullAuthority must come from operator policy, never from an agent report or
// browser-provided approval flag. Contents write/merge permission is not granted.
type PullAuthority struct {
	AccountID, RepositoryID string
	PullRequestsWrite       bool
}
type PullSpec struct{ Head, Base, CandidateSHA, Title, Body string }
type PullResult struct {
	Number       int64
	URL, HeadSHA string
}
type pullRecord struct {
	ID, Number         int64
	Title, Body, State string
	Head, Base         struct {
		Ref, SHA string
		Repo     struct {
			ID       int64
			FullName string `json:"full_name"`
		}
	}
}

// PublishPullRequest creates no branch and never merges. The trusted caller must
// first verify/review the exact candidate, push its branch, and persist operation
// intent. Ambiguous retries MUST be ReconcileOnly, as for issue publication.
func (c *Client) PublishPullRequest(ctx context.Context, a PullAuthority, op PublicationOperation, spec PullSpec) (PullResult, error) {
	base, _ := url.Parse(c.base)
	if c.base != "https://api.github.com" && base.Hostname() != "127.0.0.1" && base.Hostname() != "::1" {
		// Enterprise web URLs need explicit operator configuration; never fabricate
		// a github.com result for a different installation server.
		return PullResult{}, ErrInvalidPublication
	}
	if !a.PullRequestsWrite {
		return PullResult{}, ErrNotAllowed
	}
	if !validRef(spec.Head) || !strings.HasPrefix(spec.Head, "factory/") || !validRef(spec.Base) || spec.Head == spec.Base || !shaPattern.MatchString(spec.CandidateSHA) || strings.TrimSpace(spec.Title) == "" || len(spec.Title) > 256 || strings.TrimSpace(spec.Body) == "" || len(spec.Body) > 60000 || strings.Contains(spec.Body, markerPrefix) || strings.Contains(spec.Title, markerPrefix) || strings.TrimSpace(op.Key) == "" || len(op.Key) > 200 {
		return PullResult{}, ErrInvalidPublication
	}
	repo, token, err := c.publicationScope(ctx, a.AccountID, a.RepositoryID, map[string]string{"metadata": "read", "contents": "read", "pull_requests": "write"})
	if err != nil {
		return PullResult{}, err
	}
	root := "/repos/" + repo.FullName
	payload, _ := json.Marshal(spec)
	prefix := markerPrefix + fmt.Sprintf("%x", sha256.Sum256([]byte("pull\x00"+op.Key))) + ":"
	body := spec.Body + "\n\n" + prefix + fmt.Sprintf("%x", sha256.Sum256(payload)) + " -->"
	repoID, _ := strconv.ParseInt(a.RepositoryID, 10, 64)
	validate := func(p pullRecord) (PullResult, error) {
		if p.ID <= 0 || p.Number <= 0 || p.Title != spec.Title || p.Body != body || p.State != "open" || p.Head.Ref != spec.Head || p.Head.SHA != spec.CandidateSHA || p.Base.Ref != spec.Base || p.Head.Repo.ID != repoID || p.Base.Repo.ID != repoID || p.Head.Repo.FullName != repo.FullName || p.Base.Repo.FullName != repo.FullName {
			return PullResult{}, ErrPublicationConflict
		}
		return PullResult{Number: p.Number, URL: "https://github.com/" + repo.FullName + "/pull/" + strconv.FormatInt(p.Number, 10), HeadSHA: p.Head.SHA}, nil
	}
	scan := func() (PullResult, bool, error) {
		var found *pullRecord
		for page := 1; page <= publicationPageLimit; page++ {
			q := url.Values{"state": {"all"}, "per_page": {"100"}, "sort": {"created"}, "direction": {"asc"}, "page": {strconv.Itoa(page)}}
			var items []pullRecord
			headers, e := c.request(ctx, "GET", root+"/pulls?"+q.Encode(), token.Token, nil, &items)
			if e != nil {
				return PullResult{}, false, e
			}
			for _, p := range items {
				if !strings.Contains(p.Body, prefix) {
					continue
				}
				if found != nil {
					return PullResult{}, false, ErrPublicationConflict
				}
				if _, e = validate(p); e != nil {
					return PullResult{}, false, e
				}
				copy := p
				found = &copy
			}
			next, e := c.publicationNext(headers.Values("Link"), root+"/pulls", q, page)
			if e != nil {
				return PullResult{}, false, e
			}
			if !next {
				if found == nil {
					return PullResult{}, false, nil
				}
				// Fetch the individual PR again: listings may have stale head metadata.
				var current pullRecord
				if _, e = c.request(ctx, "GET", root+"/pulls/"+strconv.FormatInt(found.Number, 10), token.Token, nil, &current); e != nil {
					return PullResult{}, false, e
				}
				if current.ID != found.ID || current.Number != found.Number {
					return PullResult{}, false, ErrPublicationConflict
				}
				result, e := validate(current)
				return result, e == nil, e
			}
		}
		return PullResult{}, false, ErrPublicationUncertain
	}
	prior, found, err := scan()
	if err != nil || found {
		return prior, err
	}
	if op.ReconcileOnly {
		return PullResult{}, ErrPublicationUncertain
	}
	var head struct {
		SHA string `json:"sha"`
	}
	if _, err = c.request(ctx, "GET", root+"/commits/"+url.PathEscape("refs/heads/"+spec.Head), token.Token, nil, &head); err != nil {
		return PullResult{}, err
	}
	if head.SHA != spec.CandidateSHA {
		return PullResult{}, ErrPublicationConflict
	}
	var created pullRecord
	_, postErr := c.request(ctx, "POST", root+"/pulls", token.Token, map[string]any{"head": spec.Head, "base": spec.Base, "title": spec.Title, "body": body, "maintainer_can_modify": false}, &created)
	// Always re-read GitHub after POST; never accept a response URL or rely on
	// the pre-POST head check surviving a concurrent branch update.
	result, found, readErr := scan()
	if readErr == nil && found {
		return result, nil
	}
	return PullResult{}, errors.Join(ErrPublicationUncertain, postErr, readErr)
}
