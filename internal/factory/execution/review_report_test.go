package execution

import (
	"strings"
	"testing"
	"time"

	"github.com/0xikarus/vmbox-service/internal/factory/reviewer"
)

func reviewReport() reviewer.Result {
	start := time.Now().UTC()
	finish := start.Add(time.Second)
	deadline := start.Add(time.Minute)
	zero := 0
	sha := strings.Repeat("c", 40)
	return reviewer.Result{ExitCode: &zero, StartedAt: &start, FinishedAt: &finish, Deadline: &deadline, SourceSHA: sha, FinalSourceSHA: sha, SourceUnchanged: true, PromptSHA256: strings.Repeat("d", 64), Accepted: true, Review: &reviewer.Review{CandidateSHA: sha, Summary: "Reviewed source and independent checks", Approved: true, BlockingFindings: []string{}}}
}

func TestReviewReportKeepsNegativeDecisionWithExitZero(t *testing.T) {
	report := reviewReport()
	report.Accepted = false
	report.Review.Approved = false
	report.Review.BlockingFindings = []string{"price.go:4 adds the discount instead of subtracting it"}
	r, err := ReviewEvidence(process("reviewer"), report.SourceSHA, report)
	if err != nil || r.Approved || r.ExitCode == nil || *r.ExitCode != 0 || len(r.BlockingFindings) != 1 {
		t.Fatal("negative review changed", r, err)
	}
	g, _ := New(approved())
	g.Begin("api")
	g.Built("api", built())
	g.Verified("api", verified(g, "api"))
	if g.Reviewed("api", r) == nil {
		t.Fatal("negative review accepted by graph")
	}
	if g.Nodes[0].State != "needs_review" {
		t.Fatal("negative review advanced feature")
	}
}

func TestReviewReportRejectsIncompleteOrContradictoryEvidence(t *testing.T) {
	for _, mutate := range []func(*reviewer.Result){
		func(r *reviewer.Result) { r.ExitCode = nil },
		func(r *reviewer.Result) { r.SourceUnchanged = false },
		func(r *reviewer.Result) { r.FinalSourceSHA = strings.Repeat("e", 40) },
		func(r *reviewer.Result) { r.Truncated = true },
		func(r *reviewer.Result) { r.TimedOut = true },
		func(r *reviewer.Result) { r.Cancelled = true },
		func(r *reviewer.Result) { r.Error = "failed" },
		func(r *reviewer.Result) { r.FinishedAt = nil },
		func(r *reviewer.Result) { r.Deadline = r.StartedAt },
		func(r *reviewer.Result) { r.PromptSHA256 = "" },
		func(r *reviewer.Result) { r.Review = nil },
		func(r *reviewer.Result) { r.Review.BlockingFindings = []string{"blocking defect"} },
		func(r *reviewer.Result) { r.Accepted = false },
	} {
		r := reviewReport()
		mutate(&r)
		if _, err := ReviewEvidence(process("reviewer"), strings.Repeat("c", 40), r); err == nil {
			t.Fatal("incomplete report accepted")
		}
	}
	r := reviewReport()
	if _, err := ReviewEvidence(process("reviewer"), r.SourceSHA, r); err != nil {
		t.Fatal(err)
	}
}
