package execution

import (
	"strings"
	"testing"

	"github.com/0xikarus/vmbox-service/internal/factory"
)

// Synthetic receipts test acceptance rules only; real commands are the
// verification runner's separate responsibility.
func approved() factory.Work {
	check := factory.Check{Argv: []string{"go", "test", "./..."}, Cwd: ".", TimeoutSeconds: 60}
	features := []factory.Feature{{ID: "api", Title: "API", AcceptanceCriteria: []string{"works"}, Checks: []factory.Check{check}}, {ID: "ui", Title: "UI", DependsOn: []string{"api"}, AcceptanceCriteria: []string{"works"}, Checks: []factory.Check{check}}, {ID: "docs", Title: "Docs", AcceptanceCriteria: []string{"works"}, Checks: []factory.Check{check}}}
	plan := factory.Plan{Revision: 1, InputRevision: 1, Markdown: "Approved plan", BaseSHA: strings.Repeat("a", 40), Features: features}
	w := factory.Work{ID: strings.Repeat("b", 32), Revision: 2, State: "build_queued", RepositoryName: "owner/repo", BaseSHA: plan.BaseSHA, ApprovedPlanRevision: 1, Plans: []factory.Plan{plan}, MasterIssueURL: "https://github.com/owner/repo/issues/1", Features: append([]factory.Feature(nil), features...)}
	for i := range w.Features {
		w.Features[i].IssueURL = "https://github.com/owner/repo/issues/2"
	}
	return w
}
func process(box string) Process {
	zero := 0
	return Process{AttemptID: "attempt", BoxID: box, TaskID: "task", ExitCode: &zero}
}
func built() Build {
	return Build{Process: process("builder"), CandidateSHA: strings.Repeat("c", 40), Clean: true}
}
func verified(g Graph, id string) Verification {
	n, _ := g.node(id)
	zero := 0
	return Verification{Process: process("verifier"), CandidateSHA: built().CandidateSHA, SourceUnchanged: true, Checks: []CheckResult{{Check: n.Feature.Checks[0], ExitCode: &zero, LogSHA256: strings.Repeat("d", 64)}}}
}
func reviewed() Review {
	return Review{Process: process("reviewer"), CandidateSHA: built().CandidateSHA, Summary: "Independent findings", Approved: true}
}

func TestIndependentFeaturesAndDependencyGate(t *testing.T) {
	g, err := New(approved())
	if err != nil {
		t.Fatal(err)
	}
	ready := g.Ready()
	if len(ready) != 2 || ready[0].Feature.ID != "api" || ready[1].Feature.ID != "docs" {
		t.Fatal("DAG readiness incorrect")
	}
	if g.Begin("ui") == nil {
		t.Fatal("dependent started early")
	}
	if err = g.Begin("api"); err != nil {
		t.Fatal(err)
	}
	if err = g.Built("api", built()); err != nil {
		t.Fatal(err)
	}
	if g.Begin("ui") == nil || g.IntegrationReady() {
		t.Fatal("builder exit treated as completed feature")
	}
	if err = g.Verified("api", verified(g, "api")); err != nil {
		t.Fatal(err)
	}
	if err = g.Reviewed("api", reviewed()); err != nil {
		t.Fatal(err)
	}
	if err = g.Published("api", built().CandidateSHA, "https://github.com/owner/repo/pull/3"); err != nil {
		t.Fatal(err)
	}
	if err = g.Begin("ui"); err != nil {
		t.Fatal(err)
	}
	commits, err := g.DependencyCommits("ui")
	if err != nil || len(commits) != 1 || commits[0] != built().CandidateSHA {
		t.Fatal("dependency candidate missing")
	}
	if g.IntegrationReady() {
		t.Fatal("incomplete graph accepted")
	}
}

func TestApprovalAndPublicationMustMatch(t *testing.T) {
	w := approved()
	w.Features[0].Title = "Changed"
	if _, err := New(w); err == nil {
		t.Fatal("changed approved feature accepted")
	}
	w = approved()
	w.MasterIssueURL = "https://evil.example/owner/repo/issues/1"
	if _, err := New(w); err == nil {
		t.Fatal("foreign master accepted")
	}
	w = approved()
	w.State = "approved_queued"
	if _, err := New(w); err == nil {
		t.Fatal("unpublished work started")
	}
}

func TestIncompleteOrStaleCheckEvidenceRejected(t *testing.T) {
	mutations := []func(*Verification){
		func(v *Verification) { v.BoxID = "builder" },
		func(v *Verification) { v.CandidateSHA = strings.Repeat("e", 40) },
		func(v *Verification) { v.SourceUnchanged = false },
		func(v *Verification) { v.Checks = nil },
		func(v *Verification) { v.Checks[0].ExitCode = nil },
		func(v *Verification) { one := 1; v.Checks[0].ExitCode = &one },
		func(v *Verification) { v.Checks[0].Truncated = true },
		func(v *Verification) { v.Checks[0].Cancelled = true },
		func(v *Verification) { v.Checks[0].TimedOut = true },
		func(v *Verification) { v.Checks[0].LogSHA256 = "" },
		func(v *Verification) { v.Checks[0].Check.Argv = []string{"true"} },
	}
	for i, mutate := range mutations {
		g, _ := New(approved())
		g.Begin("api")
		g.Built("api", built())
		v := verified(g, "api")
		mutate(&v)
		if g.Verified("api", v) == nil {
			t.Errorf("case %d accepted", i)
		}
	}
}

func TestReviewAndPRMustIdentifyVerifiedCandidate(t *testing.T) {
	g, _ := New(approved())
	g.Begin("api")
	g.Built("api", built())
	g.Verified("api", verified(g, "api"))
	r := reviewed()
	r.BoxID = "builder"
	if g.Reviewed("api", r) == nil {
		t.Fatal("self review accepted")
	}
	r = reviewed()
	r.BlockingFindings = []string{"Broken test"}
	if g.Reviewed("api", r) == nil {
		t.Fatal("blocking findings ignored")
	}
	if err := g.Reviewed("api", reviewed()); err != nil {
		t.Fatal(err)
	}
	if g.Published("api", strings.Repeat("e", 40), "https://github.com/owner/repo/pull/3") == nil {
		t.Fatal("stale PR accepted")
	}
	if g.Published("api", built().CandidateSHA, "https://github.com/other/repo/pull/3") == nil {
		t.Fatal("foreign PR accepted")
	}
}
