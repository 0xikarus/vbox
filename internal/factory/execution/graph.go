// Package execution defines feature-stage acceptance independently of agent prose.
// Inputs must be loaded by the trusted coordinator from approved work and scoped
// runtime evidence, never accepted directly from a browser or a builder report.
package execution

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"reflect"
	"strconv"
	"strings"

	"github.com/0xikarus/vmbox-service/internal/factory"
)

type Process struct {
	AttemptID string `json:"attemptId"`
	BoxID     string `json:"boxId"`
	TaskID    string `json:"taskId"`
	ExitCode  *int   `json:"exitCode"`
	Signal    int    `json:"signal"`
}
type Build struct {
	Process
	CandidateSHA string `json:"candidateSha"`
	Clean        bool   `json:"clean"`
}
type CheckResult struct {
	Check     factory.Check `json:"check"`
	ExitCode  *int          `json:"exitCode"`
	Signal    int           `json:"signal"`
	TimedOut  bool          `json:"timedOut"`
	Cancelled bool          `json:"cancelled"`
	Truncated bool          `json:"truncated"`
	LogSHA256 string        `json:"logSha256"`
}
type Verification struct {
	Process
	CandidateSHA    string        `json:"candidateSha"`
	Checks          []CheckResult `json:"checks"`
	SourceUnchanged bool          `json:"sourceUnchanged"`
}
type Review struct {
	Process
	CandidateSHA     string   `json:"candidateSha"`
	Summary          string   `json:"summary"`
	BlockingFindings []string `json:"blockingFindings"`
	Approved         bool     `json:"approved"`
}
type Node struct {
	Feature      factory.Feature `json:"feature"`
	Branch       string          `json:"branch"`
	State        string          `json:"state"`
	Build        *Build          `json:"build,omitempty"`
	Verification *Verification   `json:"verification,omitempty"`
	Review       *Review         `json:"review,omitempty"`
	PRURL        string          `json:"prUrl,omitempty"`
	Attempt      *StageAttempt   `json:"attempt,omitempty"`
}
type StageAttempt struct {
	ID      string   `json:"id"`
	Stage   string   `json:"stage"`
	BoxID   string   `json:"boxId,omitempty"`
	TaskID  string   `json:"taskId,omitempty"`
	State   string   `json:"state"`
	Process *Process `json:"process,omitempty"`
	Failure string   `json:"failure,omitempty"`
}
type Graph struct {
	WorkID       string `json:"workId"`
	Repository   string `json:"repository"`
	PlanRevision int    `json:"planRevision"`
	BaseSHA      string `json:"baseSha"`
	Nodes        []Node `json:"nodes"`
}

func New(w factory.Work) (Graph, error) {
	if w.State != "build_queued" || w.ApprovedPlanRevision < 1 || len(w.Plans) == 0 || !digest(w.ID, 32) || !digest(w.BaseSHA, 40) {
		return Graph{}, fmt.Errorf("published approved work required")
	}
	p := w.Plans[len(w.Plans)-1]
	if p.ValidateApproval() != nil || p.Revision != w.ApprovedPlanRevision || p.InputRevision < 1 || p.InputRevision >= w.Revision || p.BaseSHA != w.BaseSHA || len(w.Features) != len(p.Features) {
		return Graph{}, fmt.Errorf("approval no longer matches feature graph")
	}
	if !githubURL(w.MasterIssueURL, w.RepositoryName, "issues") {
		return Graph{}, fmt.Errorf("published master issue required")
	}
	g := Graph{WorkID: w.ID, Repository: w.RepositoryName, PlanRevision: p.Revision, BaseSHA: w.BaseSHA}
	for i, f := range p.Features {
		published := w.Features[i]
		// Publication may enrich only evidence/state fields, not approved checks,
		// ownership, dependency IDs or acceptance criteria.
		published.State = f.State
		published.IssueURL = f.IssueURL
		published.PRURL = f.PRURL
		published.BoxID = f.BoxID
		if !reflect.DeepEqual(published, f) || !githubURL(w.Features[i].IssueURL, w.RepositoryName, "issues") {
			return Graph{}, fmt.Errorf("feature changed after approval or issue missing")
		}
		h := sha256.Sum256([]byte(f.ID))
		g.Nodes = append(g.Nodes, Node{Feature: w.Features[i], Branch: fmt.Sprintf("factory/%s/p%d/%x", w.ID, p.Revision, h[:8]), State: "queued"})
	}
	return g, nil
}

// Ready returns only dependency-satisfied features, in approved order. The
// executor must prepare a source containing every returned dependency commit.
func (g Graph) Ready() []Node {
	ready := []Node{}
	for _, n := range g.Nodes {
		if n.State != "queued" {
			continue
		}
		ok := true
		for _, id := range n.Feature.DependsOn {
			d, e := g.node(id)
			if e != nil || d.State != "pr_ready" {
				ok = false
				break
			}
		}
		if ok {
			ready = append(ready, n)
		}
	}
	return ready
}
func (g Graph) DependencyCommits(id string) ([]string, error) {
	n, err := g.node(id)
	if err != nil {
		return nil, err
	}
	commits := []string{}
	for _, dep := range n.Feature.DependsOn {
		d, e := g.node(dep)
		if e != nil || d.State != "pr_ready" || d.Build == nil {
			return nil, fmt.Errorf("dependency not ready")
		}
		commits = append(commits, d.Build.CandidateSHA)
	}
	return commits, nil
}
func (g *Graph) Begin(id string) error {
	for _, n := range g.Ready() {
		if n.Feature.ID == id {
			target, _ := g.node(id)
			target.State = "building"
			return nil
		}
	}
	return fmt.Errorf("feature not ready to build")
}
func (g *Graph) Built(id string, b Build) error {
	n, err := g.node(id)
	if err != nil || n.State != "building" {
		return fmt.Errorf("feature not building")
	}
	if !success(b.Process) || !digest(b.CandidateSHA, 40) || !b.Clean {
		return fmt.Errorf("builder has no clean successful candidate")
	}
	n.Build = &b
	n.State = "needs_verification"
	return nil
}
func (g *Graph) Verified(id string, v Verification) error {
	n, err := g.node(id)
	if err != nil || (n.State != "needs_verification" && n.State != "verifying") || n.Build == nil {
		return fmt.Errorf("feature not awaiting verification")
	}
	if !success(v.Process) || v.BoxID == n.Build.BoxID || v.CandidateSHA != n.Build.CandidateSHA || !v.SourceUnchanged || len(v.Checks) != len(n.Feature.Checks) {
		return fmt.Errorf("verification identity or source mismatch")
	}
	for i, c := range v.Checks {
		if !reflect.DeepEqual(c.Check, n.Feature.Checks[i]) || c.ExitCode == nil || *c.ExitCode != 0 || c.Signal != 0 || c.TimedOut || c.Cancelled || c.Truncated || !digest(c.LogSHA256, 64) {
			return fmt.Errorf("required check lacks passing complete evidence")
		}
	}
	n.Verification = &v
	n.State = "needs_review"
	return nil
}
func (g *Graph) Reviewed(id string, r Review) error {
	n, err := g.node(id)
	if err != nil || (n.State != "needs_review" && n.State != "reviewing") || n.Build == nil {
		return fmt.Errorf("feature not awaiting independent review")
	}
	if !success(r.Process) || r.BoxID == n.Build.BoxID || r.CandidateSHA != n.Build.CandidateSHA || strings.TrimSpace(r.Summary) == "" || !r.Approved || len(r.BlockingFindings) > 0 {
		return fmt.Errorf("independent review did not approve candidate")
	}
	n.Review = &r
	n.State = "needs_pr"
	return nil
}
func (g *Graph) Published(id, sha, pr string) error {
	n, err := g.node(id)
	if err != nil || n.State != "needs_pr" || n.Build == nil {
		return fmt.Errorf("feature not ready for PR")
	}
	if sha != n.Build.CandidateSHA || !githubURL(pr, g.Repository, "pull") {
		return fmt.Errorf("PR does not identify the reviewed repository candidate")
	}
	n.PRURL = pr
	n.State = "pr_ready"
	return nil
}

// IntegrationReady is NOT product completion. The combined candidate still needs
// independent in-box verification/review against all approved acceptance checks.
func (g Graph) IntegrationReady() bool {
	if len(g.Nodes) == 0 {
		return false
	}
	for _, n := range g.Nodes {
		if n.State != "pr_ready" {
			return false
		}
	}
	return true
}
func (g Graph) Digest() string {
	b, _ := json.Marshal(g)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func (g Graph) node(id string) (*Node, error) {
	for i := range g.Nodes {
		if g.Nodes[i].Feature.ID == id {
			return &g.Nodes[i], nil
		}
	}
	return nil, fmt.Errorf("unknown feature")
}
func success(p Process) bool {
	return p.AttemptID != "" && p.BoxID != "" && p.TaskID != "" && p.ExitCode != nil && *p.ExitCode == 0 && p.Signal == 0
}
func digest(value string, n int) bool {
	if len(value) != n || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
func githubURL(raw, repo, kind string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host != "github.com" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	prefix := "/" + repo + "/" + kind + "/"
	if !strings.HasPrefix(u.Path, prefix) {
		return false
	}
	n, err := strconv.ParseInt(strings.TrimPrefix(u.Path, prefix), 10, 64)
	return err == nil && n > 0
}
