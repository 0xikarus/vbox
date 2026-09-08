package reviewer

import (
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/0xikarus/vmbox-service/internal/factory/verification"
)

// Verify the supplied report's shape and binding. Artifact authentication remains
// the coordinator's job; neither this function nor an AI can prove provenance.
func validateEvidence(req Request) error {
	r := req.CheckEvidence
	if !r.AllPassed || r.Error != "" || r.ExpectedSHA != req.CandidateSHA || r.SourceSHA != req.CandidateSHA || r.FinalSourceSHA != req.CandidateSHA || !r.SourceClean || len(r.Checks) != len(req.ApprovedFeature.Checks) {
		return errors.New("complete passing candidate-bound verification required")
	}
	for i, c := range r.Checks {
		approved := req.ApprovedFeature.Checks[i]
		cwd := approved.Cwd
		if !filepath.IsAbs(cwd) {
			cwd = filepath.Join(req.Workspace, cwd)
		}
		if approved.Cwd == "" || !within(req.Workspace, cwd) || len(approved.Argv) == 0 || approved.TimeoutSeconds < 1 || approved.TimeoutSeconds > 3600 || !reflect.DeepEqual(c.Argv, approved.Argv) || c.Cwd != cwd || c.TimeoutSeconds != approved.TimeoutSeconds || !c.Executed || c.ExitCode == nil || *c.ExitCode != 0 || c.Signal != 0 || c.Timeout || c.Cancel || c.Error != "" || c.StartedAt == nil || c.FinishedAt == nil || c.FinishedAt.Before(*c.StartedAt) {
			return errors.New("approved check evidence mismatch or incomplete")
		}
		for j, l := range []verification.Log{c.Stdout, c.Stderr} {
			kind := "stdout"
			if j == 1 {
				kind = "stderr"
			}
			digest, e := hex.DecodeString(l.SHA256)
			if e != nil || len(digest) != 32 || strings.ToLower(l.SHA256) != l.SHA256 || l.File != fmt.Sprintf("%03d.%s.log", i, kind) || l.Bytes < 0 || l.Bytes > verification.LogLimit || l.Truncated {
				return errors.New("check log evidence incomplete")
			}
		}
	}
	return nil
}
