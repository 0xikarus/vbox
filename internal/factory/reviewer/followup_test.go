package reviewer

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/0xikarus/vmbox-service/internal/factory/verification"
)

func repoGit(t *testing.T, ws string, args ...string) string {
	t.Helper()
	c := exec.Command("/usr/bin/git", args...)
	c.Dir = ws
	c.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + t.TempDir(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null"}
	b, e := c.CombinedOutput()
	if e != nil {
		t.Fatalf("git %v: %v %s", args, e, b)
	}
	return strings.TrimSpace(string(b))
}
func writeFixture(t *testing.T, path, content string) {
	t.Helper()
	if e := os.WriteFile(path, []byte(content), 0600); e != nil {
		t.Fatal(e)
	}
}
func commitFixture(t *testing.T, ws string) string {
	t.Helper()
	repoGit(t, ws, "add", ".")
	repoGit(t, ws, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-m", "fixture")
	return repoGit(t, ws, "rev-parse", "HEAD")
}

func TestSeparateVerificationWorkspace(t *testing.T) {
	for _, mode := range []string{"valid", "missing", "relative-root", "unclean-root", "wrong-box-path", "wrong-subdir", "absolute-check", "traversal", "unclean-check"} {
		t.Run(mode, func(t *testing.T) {
			req := fixture(t)
			req.VerificationWorkspace = "/independent-verifier/source"
			req.ApprovedFeature.Checks[0].Cwd = "pkg"
			req.CheckEvidence.Checks[0].Cwd = "/independent-verifier/source/pkg"
			switch mode {
			case "missing":
				req.VerificationWorkspace = ""
			case "relative-root":
				req.VerificationWorkspace = "source"
			case "unclean-root":
				req.VerificationWorkspace += "/."
			case "wrong-box-path":
				req.CheckEvidence.Checks[0].Cwd = filepath.Join(req.Workspace, "pkg")
			case "wrong-subdir":
				req.CheckEvidence.Checks[0].Cwd = "/independent-verifier/source"
			case "absolute-check":
				req.ApprovedFeature.Checks[0].Cwd = "/independent-verifier/source/pkg"
			case "traversal":
				req.ApprovedFeature.Checks[0].Cwd = "../pkg"
			case "unclean-check":
				req.ApprovedFeature.Checks[0].Cwd = "pkg/../pkg"
			}
			if e := validateEvidence(req); (e == nil) != (mode == "valid") {
				t.Fatal(mode, e)
			}
			if mode == "valid" {
				r, e := run(context.Background(), req, cli(t, req, "pass"))
				if e != nil || !r.Accepted {
					t.Fatal(r, e)
				}
			}
		})
	}
}

func TestIndexFlagsCannotMaskBytes(t *testing.T) {
	for _, flag := range []string{"--assume-unchanged", "--skip-worktree"} {
		for _, phase := range []string{"initial", "final"} {
			t.Run(flag+"/"+phase, func(t *testing.T) {
				req := fixture(t)
				repoGit(t, req.Workspace, "update-index", flag, "price.go")
				mode := "dirty"
				if phase == "initial" {
					writeFixture(t, filepath.Join(req.Workspace, "price.go"), "tampered\n")
					mode = "pass"
				}
				r, e := run(context.Background(), req, cli(t, req, mode))
				if e == nil || r.Accepted {
					t.Fatal(r, e)
				}
				if phase == "initial" && r.StartedAt != nil {
					t.Fatal("dirty source reached reviewer")
				}
				if phase == "final" && (r.StartedAt == nil || r.SourceUnchanged) {
					t.Fatal(r)
				}
				// The source index flags themselves must remain intact.
				entry := repoGit(t, req.Workspace, "ls-files", "-v", "price.go")
				if !strings.HasPrefix(entry, "h ") && !strings.HasPrefix(entry, "S ") {
					t.Fatal(entry)
				}
			})
		}
	}
}

func TestSyntheticLargeRepositoryDefectContext(t *testing.T) {
	req := fixture(t)
	repoGit(t, req.Workspace, "reset", "--hard", req.BaseSHA)
	writeFixture(t, filepath.Join(req.Workspace, "image.png"), "\x89PNG\x00"+strings.Repeat("x", 2<<20))
	writeFixture(t, filepath.Join(req.Workspace, "large.txt"), strings.Repeat("unchanged text\n", 100000))
	req.BaseSHA = commitFixture(t, req.Workspace)
	writeFixture(t, filepath.Join(req.Workspace, "price.go"), "package price\n\n// Discount returns the price after a percentage discount.\nfunc Discount(price, percent int) int { return price + price*percent/100 }\n")
	req.CandidateSHA = commitFixture(t, req.Workspace)
	var e error
	req.CheckEvidence, e = verification.Run(context.Background(), verification.Request{Workspace: req.Workspace, ExpectedSHA: req.CandidateSHA, OutputDir: t.TempDir(), Checks: req.ApprovedFeature.Checks})
	if e != nil || !req.CheckEvidence.AllPassed {
		t.Fatal(e, req.CheckEvidence)
	}
	exe := cli(t, req, "reject")
	capture := filepath.Join(t.TempDir(), "prompt")
	script, e := os.ReadFile(exe)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(exe, []byte(strings.Replace(string(script), "cat >/dev/null", "cat > '"+capture+"'", 1)), 0700); e != nil {
		t.Fatal(e)
	}
	r, e := run(context.Background(), req, exe)
	if e != nil || r.Review == nil || r.Accepted || !r.SourceUnchanged {
		t.Fatal(r, e)
	}
	prompt, e := os.ReadFile(capture)
	if e != nil {
		t.Fatal(e)
	}
	for _, want := range []string{"BASE INVENTORY", "CANDIDATE INVENTORY", "ACTUAL BASE-TO-CANDIDATE GIT DIFF", "-func Discount", "+func Discount", "return price + price*percent/100", "Discount(100,20) must return 80", `"image.png" CONTENT OMITTED: unchanged`, `"large.txt" CONTENT OMITTED: unchanged`, repoGit(t, req.Workspace, "rev-parse", "HEAD:image.png")} {
		if !strings.Contains(string(prompt), want) {
			t.Fatalf("reviewer prompt missing %q", want)
		}
	}
	if len(prompt) > maxSource || strings.Contains(string(prompt), strings.Repeat("unchanged text\n", 10)) {
		t.Fatal("unchanged contents loaded")
	}
}

func TestChangedUnsupportedContent(t *testing.T) {
	for _, mode := range []string{"binary-added", "binary-deleted", "large-added", "large-deleted", "aggregate"} {
		t.Run(mode, func(t *testing.T) {
			req := fixture(t)
			content := "binary\x00data"
			if strings.HasPrefix(mode, "large") {
				content = strings.Repeat("x", maxSource+1)
			}
			if mode == "aggregate" {
				content = strings.Repeat("x", maxSource/2)
			}
			writeFixture(t, filepath.Join(req.Workspace, "asset"), content)
			sha := commitFixture(t, req.Workspace)
			if strings.HasSuffix(mode, "deleted") {
				req.BaseSHA = sha
				if e := os.Remove(filepath.Join(req.Workspace, "asset")); e != nil {
					t.Fatal(e)
				}
				commitFixture(t, req.Workspace)
			}
			var bundle string
			candidate, _, e := inspect(context.Background(), req.Workspace, req.BaseSHA, &bundle)
			if !errors.Is(e, ErrIncomplete) || bundle != "" {
				t.Fatal("omitted changed content allowed", e)
			}
			// Synthetic evidence binding isolates the pre-launch incomplete-source gate.
			req.CandidateSHA = candidate
			req.CheckEvidence.ExpectedSHA = candidate
			req.CheckEvidence.SourceSHA = candidate
			req.CheckEvidence.FinalSourceSHA = candidate
			r, e := run(context.Background(), req, cli(t, req, "pass"))
			if !errors.Is(e, ErrIncomplete) || r.Accepted || r.Review != nil || r.StartedAt != nil {
				t.Fatal("incomplete source reached reviewer", r, e)
			}
			saved, e := os.ReadFile(req.ResultPath)
			if e != nil || !strings.Contains(string(saved), ErrIncomplete.Error()) {
				t.Fatal("missing persisted incomplete evidence", e)
			}
		})
	}
}
