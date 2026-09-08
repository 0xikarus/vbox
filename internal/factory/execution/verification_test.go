package execution

import (
	"strings"
	"testing"
	"time"

	"github.com/0xikarus/vmbox-service/internal/factory/verification"
)

func TestVerificationMappingDoesNotTrustAllPassed(t *testing.T) {
	g, _ := New(approved())
	checks := g.Nodes[0].Feature.Checks
	sha := built().CandidateSHA
	zero := 0
	now := time.Now()
	base := verification.Report{ExpectedSHA: sha, SourceSHA: sha, FinalSourceSHA: sha, SourceClean: true, AllPassed: true, Checks: []verification.CheckReport{{Argv: checks[0].Argv, Cwd: "/checkout", TimeoutSeconds: checks[0].TimeoutSeconds, Executed: true, StartedAt: &now, FinishedAt: &now, ExitCode: &zero, Stdout: verification.Log{File: "000.stdout.log", SHA256: strings.Repeat("a", 64)}, Stderr: verification.Log{File: "000.stderr.log", SHA256: strings.Repeat("b", 64)}}}}
	if _, err := VerificationEvidence(process("verifier"), "/checkout", sha, checks, base); err != nil {
		t.Fatal(err)
	}
	for i, mutate := range []func(*verification.Report){
		func(r *verification.Report) { r.FinalSourceSHA = strings.Repeat("e", 40) },
		func(r *verification.Report) { r.Checks[0].Executed = false },
		func(r *verification.Report) { r.Checks[0].Stdout.Truncated = true },
		func(r *verification.Report) { r.Checks[0].Stdout.File = "../../secret" },
		func(r *verification.Report) { r.Checks[0].StartedAt = nil },
		func(r *verification.Report) { r.Checks[0].Cwd = "/elsewhere" },
		func(r *verification.Report) { r.Checks[0].Argv = []string{"true"} },
		func(r *verification.Report) { r.Checks[0].ExitCode = nil },
	} {
		r := base
		r.Checks = append([]verification.CheckReport(nil), base.Checks...)
		mutate(&r)
		if _, err := VerificationEvidence(process("verifier"), "/checkout", sha, checks, r); err == nil {
			t.Errorf("tampered report %d accepted", i)
		}
	}
}
