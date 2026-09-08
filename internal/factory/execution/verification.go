package execution

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/0xikarus/vmbox-service/internal/factory"
	"github.com/0xikarus/vmbox-service/internal/factory/verification"
)

// VerificationEvidence maps a report collected from an independent verifier's
// scoped result channel. It reconstructs acceptance from each actual check;
// AllPassed is required but never sufficient. The caller must preserve the
// manifest and both log files as authenticated artifacts before releasing a box.
func VerificationEvidence(process Process, workspace, candidate string, approved []factory.Check, report verification.Report) (Verification, error) {
	fail := func() (Verification, error) {
		return Verification{}, fmt.Errorf("verification report does not prove the approved checks")
	}
	if !success(process) || !filepath.IsAbs(workspace) || filepath.Clean(workspace) != workspace || !digest(candidate, 40) || len(approved) == 0 || !report.AllPassed || report.Error != "" || report.ExpectedSHA != candidate || report.SourceSHA != candidate || report.FinalSourceSHA != candidate || !report.SourceClean || len(report.Checks) != len(approved) {
		return fail()
	}
	v := Verification{Process: process, CandidateSHA: candidate, SourceUnchanged: true}
	for i, expected := range approved {
		c := report.Checks[i]
		cwd := expected.Cwd
		if !filepath.IsAbs(cwd) {
			cwd = filepath.Join(workspace, cwd)
		}
		rel, err := filepath.Rel(workspace, cwd)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return fail()
		}
		if !reflect.DeepEqual(c.Argv, expected.Argv) || c.Cwd != cwd || c.TimeoutSeconds != expected.TimeoutSeconds || !c.Executed || c.StartedAt == nil || c.FinishedAt == nil || c.FinishedAt.Before(*c.StartedAt) || c.ExitCode == nil || *c.ExitCode != 0 || c.Signal != 0 || c.Timeout || c.Cancel || c.Error != "" {
			return fail()
		}
		for j, log := range []verification.Log{c.Stdout, c.Stderr} {
			kind := "stdout"
			if j == 1 {
				kind = "stderr"
			}
			if log.File != fmt.Sprintf("%03d.%s.log", i, kind) || !digest(log.SHA256, 64) || log.Bytes < 0 || log.Bytes > verification.LogLimit || log.Truncated {
				return fail()
			}
		}
		b, _ := json.Marshal([]verification.Log{c.Stdout, c.Stderr})
		h := sha256.Sum256(b)
		v.Checks = append(v.Checks, CheckResult{Check: expected, ExitCode: c.ExitCode, LogSHA256: hex.EncodeToString(h[:])})
	}
	return v, nil
}
