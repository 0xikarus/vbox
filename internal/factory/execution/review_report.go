package execution

import (
	"fmt"
	"strings"

	"github.com/0xikarus/vmbox-service/internal/factory/reviewer"
)

// ReviewEvidence maps a trusted, attempt-scoped adapter report, not arbitrary
// agent JSON. Negative reviews remain negative even when the process exited 0.
// The caller authenticates transport and matches the persisted box/task identity.
func ReviewEvidence(process Process, candidate string, report reviewer.Result) (Review, error) {
	if !success(process) || !digest(candidate, 40) || report.ExitCode == nil || *report.ExitCode != *process.ExitCode || report.Signal != process.Signal || report.Error != "" || report.Unavailable || report.TimedOut || report.Cancelled || report.Truncated || !report.SourceUnchanged || report.SourceSHA != candidate || report.FinalSourceSHA != candidate || !digest(report.PromptSHA256, 64) {
		return Review{}, fmt.Errorf("review runtime or source evidence is incomplete")
	}
	if report.StartedAt == nil || report.FinishedAt == nil || report.Deadline == nil || report.StartedAt.IsZero() || report.FinishedAt.Before(*report.StartedAt) || report.Deadline.Before(*report.FinishedAt) {
		return Review{}, fmt.Errorf("review process timing is incomplete")
	}
	r := report.Review
	if r == nil || r.CandidateSHA != candidate || strings.TrimSpace(r.Summary) == "" || len(r.Summary) > 65536 || len(r.BlockingFindings) > 100 || r.BlockingFindings == nil || report.Accepted != (r.Approved && len(r.BlockingFindings) == 0) || (r.Approved && len(r.BlockingFindings) > 0) {
		return Review{}, fmt.Errorf("review decision is missing or contradictory")
	}
	for _, finding := range r.BlockingFindings {
		if strings.TrimSpace(finding) == "" || len(finding) > 65536 {
			return Review{}, fmt.Errorf("review finding is invalid")
		}
	}
	return Review{Process: process, CandidateSHA: candidate, Summary: r.Summary, BlockingFindings: append([]string{}, r.BlockingFindings...), Approved: r.Approved}, nil
}
