package reviewer

import (
	"context"
	"encoding/json"
	"github.com/0xikarus/vmbox-service/internal/factory"
	"github.com/0xikarus/vmbox-service/internal/factory/verification"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func fixture(t *testing.T) Request {
	t.Helper()
	root := t.TempDir()
	ws := filepath.Join(root, "source")
	out := filepath.Join(root, "results")
	os.Mkdir(ws, 0700)
	os.Mkdir(out, 0700)
	git := func(args ...string) string {
		t.Helper()
		c := exec.Command("/usr/bin/git", args...)
		c.Dir = ws
		c.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + root, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null"}
		b, e := c.CombinedOutput()
		if e != nil {
			t.Fatalf("git: %v %s", e, b)
		}
		return strings.TrimSpace(string(b))
	}
	git("init", "--template=")
	os.WriteFile(filepath.Join(ws, "price.go"), []byte("package price\n\n// Discount returns the price after a percentage discount.\nfunc Discount(price, percent int) int { return price - price*percent/100 }\n"), 0600)
	git("add", ".")
	git("-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-m", "correct baseline")
	base := git("rev-parse", "HEAD")
	os.WriteFile(filepath.Join(ws, "price.go"), []byte("package price\n\n// Discount returns the price after a percentage discount.\nfunc Discount(price, percent int) int { return price + price*percent/100 }\n"), 0600)
	git("add", ".")
	git("-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-m", "candidate discount feature")
	sha := git("rev-parse", "HEAD")
	check := factory.Check{Argv: []string{"/usr/bin/test", "-f", "price.go"}, Cwd: ".", TimeoutSeconds: 5}
	evidence, e := verification.Run(context.Background(), verification.Request{Workspace: ws, ExpectedSHA: sha, OutputDir: out, Checks: []factory.Check{check}})
	if e != nil || !evidence.AllPassed {
		t.Fatal(evidence, e)
	}
	return Request{Agent: "codex", Workspace: ws, BaseSHA: base, CandidateSHA: sha, ResultPath: filepath.Join(out, "review.json"), ApprovedFeature: factory.Feature{ID: "discount", Title: "Percentage discount", Description: "Compute a discounted price for an integer percentage from 0 through 100.", AcceptanceCriteria: []string{"Discount(100,20) must return 80; zero percent preserves price and 100 percent returns zero."}, Checks: []factory.Check{check}}, CheckEvidence: evidence}
}

// Synthetic CLI fixtures exercise adapter failure semantics; these are not AI reviews.
func cli(t *testing.T, req Request, mode string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "synthetic-cli")
	review := `{"candidateSha":"` + req.CandidateSHA + `","summary":"Synthetic fixture review summary for adapter testing.","blockingFindings":[],"approved":true}`
	body := "#!/bin/sh\nwhile [ $# -gt 0 ]; do if [ \"$1\" = --output-last-message ]; then shift; dest=$1; fi; shift; done\ncat >/dev/null\n"
	switch mode {
	case "pass":
		body += "printf '%s' '" + review + "' > \"$dest\"\n"
	case "reject":
		body += "printf '%s' '" + strings.Replace(strings.Replace(review, `[]`, `["price.go:4 adds the discount and increases the price."]`, 1), `true`, `false`, 1) + "' > \"$dest\"\n"
	case "missing":
	case "malformed":
		body += "printf '{' > \"$dest\"\n"
	case "oversized":
		body += "head -c 70000 /dev/zero > \"$dest\"\n"
	case "log-truncation":
		body += "head -c 2200000 /dev/zero\n"
	case "exit":
		body += "exit 7\n"
	case "signal":
		body += "kill -TERM $$\n"
	case "sleep":
		body += "sleep 60\n"
	case "dirty":
		body += "echo tampered >> price.go\nprintf '%s' '" + review + "' > \"$dest\"\n"
	case "head":
		body += "/usr/bin/git update-ref HEAD " + req.BaseSHA + "\nprintf '%s' '" + review + "' > \"$dest\"\n"
	case "unavailable":
		body += "echo 'Not logged in' >&2\nexit 1\n"
	case "env":
		body += "if env | /usr/bin/grep -E 'GITHUB_TOKEN|ANTHROPIC_API_KEY|OPENAI_API_KEY|CONTROLLER_TOKEN|AWS_SECRET|SSH_AUTH_SOCK'; then exit 9; fi\nprintf '%s' '" + review + "' > \"$dest\"\n"
	}
	if e := os.WriteFile(p, []byte(body), 0700); e != nil {
		t.Fatal(e)
	}
	return p
}
func TestSyntheticCLI(t *testing.T) {
	for _, mode := range []string{"pass", "reject", "missing", "malformed", "oversized", "log-truncation", "exit", "signal", "sleep", "dirty", "head", "unavailable", "env"} {
		t.Run(mode, func(t *testing.T) {
			req := fixture(t)
			for _, k := range []string{"GITHUB_TOKEN", "ANTHROPIC_API_KEY", "OPENAI_API_KEY", "CONTROLLER_TOKEN", "AWS_SECRET_ACCESS_KEY", "SSH_AUTH_SOCK"} {
				t.Setenv(k, "synthetic-secret")
			}
			ctx := context.Background()
			if mode == "sleep" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, 200*time.Millisecond)
				defer cancel()
			}
			r, e := run(ctx, req, cli(t, req, mode))
			want := mode == "pass" || mode == "env"
			if r.Accepted != want {
				t.Fatalf("accepted=%v err=%v", r.Accepted, e)
			}
			if mode == "pass" || mode == "reject" || mode == "env" {
				if e != nil {
					t.Fatal(e)
				}
			} else if e == nil {
				t.Fatal("missing rejection error")
			}
			if mode == "reject" && (r.ExitCode == nil || *r.ExitCode != 0 || r.Review == nil || r.Review.Approved) {
				t.Fatal(r)
			}
			if mode == "exit" && (r.ExitCode == nil || *r.ExitCode != 7) {
				t.Fatal(r)
			}
			if mode == "signal" && (r.Signal != int(syscall.SIGTERM) || r.ExitCode != nil) {
				t.Fatal(r)
			}
			if mode == "sleep" && (!r.Cancelled || r.Signal != 9) {
				t.Fatal(r)
			}
			if mode == "unavailable" && !r.Unavailable {
				t.Fatal(r)
			}
			b, e := os.ReadFile(req.ResultPath)
			if e != nil {
				t.Fatal(e)
			}
			var saved Result
			if json.Unmarshal(b, &saved) != nil || saved.Accepted != r.Accepted {
				t.Fatal("bad persisted evidence")
			}
			st, _ := os.Stat(req.ResultPath)
			if st.Mode().Perm() != 0600 {
				t.Fatal("result not private")
			}
			if _, e = run(context.Background(), req, cli(t, req, "pass")); e == nil {
				t.Fatal("overwrote result")
			}
		})
	}
}
func TestStrictDocument(t *testing.T) {
	sha := strings.Repeat("a", 40)
	valid := `{"candidateSha":"` + sha + `","summary":"A substantive review summary.","blockingFindings":[],"approved":false}`
	for _, b := range []string{"", valid[:len(valid)-1], valid + valid, strings.Replace(valid, `false`, `null`, 1), strings.Replace(valid, `[]`, `null`, 1), strings.Replace(valid, `"approved":false`, `"approved":false,"approved":true`, 1), strings.Replace(valid, sha, strings.Repeat("b", 40), 1), strings.Replace(valid, `"approved":false`, `"other":false`, 1)} {
		if _, e := decode([]byte(b), sha); e == nil {
			t.Fatalf("accepted %s", b)
		}
	}
	if _, e := decode([]byte(valid), sha); e != nil {
		t.Fatal(e)
	}
}
func TestUnsafeInput(t *testing.T) {
	for _, mode := range []string{"dirty", "inside", "symlink", "public", "sha", "evidence", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			req := fixture(t)
			ctx := context.Background()
			switch mode {
			case "dirty":
				os.WriteFile(filepath.Join(req.Workspace, "untracked"), []byte("dirty"), 0600)
			case "inside":
				req.ResultPath = filepath.Join(req.Workspace, "review.json")
			case "symlink":
				p := filepath.Join(t.TempDir(), "link")
				os.Symlink(filepath.Dir(req.ResultPath), p)
				req.ResultPath = filepath.Join(p, "review.json")
			case "public":
				os.Chmod(filepath.Dir(req.ResultPath), 0755)
			case "sha":
				req.CandidateSHA = "HEAD"
			case "evidence":
				req.CheckEvidence = verification.Report{}
			case "cancel":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			r, e := run(ctx, req, cli(t, req, "pass"))
			if e == nil || r.Accepted || r.StartedAt != nil {
				t.Fatal(r, e)
			}
		})
	}
}

func TestLiveReview(t *testing.T) {
	agent := os.Getenv("FACTORY_LIVE_REVIEW")
	if agent == "" {
		t.Skip("opt-in authenticated live CLI review")
	}
	req := fixture(t)
	req.Agent = agent
	r, e := Run(context.Background(), req)
	b, _ := json.MarshalIndent(r, "", "  ")
	t.Logf("Actual %s result: %s; error=%v", agent, b, e)
	if agent == "claude" && r.Unavailable {
		t.Log("Claude unavailable; no successful review claimed")
		return
	}
	if e != nil {
		t.Fatal(e)
	}
	if r.ExitCode == nil || *r.ExitCode != 0 || r.Review == nil || r.Accepted || r.Review.Approved || len(r.Review.BlockingFindings) == 0 || !r.SourceUnchanged {
		t.Fatal("real reviewer failed to reject defective committed source")
	}
	// Findings are logged for human inspection, not matched to canned wording.
}

func TestSyntheticOwnedGroupCleanup(t *testing.T) {
	for _, mode := range []string{"deadline", "normal-exit"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			pidFile := filepath.Join(dir, "child.pid")
			sibling := exec.Command("/bin/sleep", "30")
			if e := sibling.Start(); e != nil {
				t.Fatal(e)
			}
			defer func() { sibling.Process.Kill(); sibling.Wait() }()
			script := "sleep 30 &\necho $! > \"$1\"\n"
			if mode == "deadline" {
				script += "wait\n"
			}
			ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
			defer cancel()
			cmd := exec.CommandContext(ctx, "/bin/sh", "-c", script, "fixture", pidFile)
			// An inherited stdout pipe must not let an orphan hold Wait open.
			var out boundedBuffer
			out.limit = 100
			cmd.Stdout = &out
			var r Result
			e := execute(context.Background(), ctx, cmd, &r)
			if mode == "deadline" {
				if e == nil || !r.TimedOut || r.Cancelled || r.Signal != 9 {
					t.Fatal(r, e)
				}
			} else if e != nil || r.ExitCode == nil || *r.ExitCode != 0 {
				t.Fatal(r, e)
			}
			if e := sibling.Process.Signal(syscall.Signal(0)); e != nil {
				t.Fatal("unrelated sibling killed")
			}
			b, e := os.ReadFile(pidFile)
			if e != nil {
				t.Fatal(e)
			}
			stat, e := os.ReadFile("/proc/" + strings.TrimSpace(string(b)) + "/stat")
			if e == nil {
				end := strings.LastIndex(string(stat), ")")
				if end < 0 || !strings.HasPrefix(string(stat)[end+1:], " Z") {
					t.Fatalf("owned child survived: %s", stat)
				}
			}
		})
	}
}

func TestEvidenceMismatch(t *testing.T) {
	for _, mode := range []string{"argv", "exit", "truncated", "sha", "allpassed"} {
		t.Run(mode, func(t *testing.T) {
			req := fixture(t)
			switch mode {
			case "argv":
				req.CheckEvidence.Checks[0].Argv = []string{"true"}
			case "exit":
				v := 7
				req.CheckEvidence.Checks[0].ExitCode = &v
			case "truncated":
				req.CheckEvidence.Checks[0].Stdout.Truncated = true
			case "sha":
				req.CheckEvidence.FinalSourceSHA = req.BaseSHA
			case "allpassed":
				req.CheckEvidence.AllPassed = false
			}
			if e := validateEvidence(req); e == nil {
				t.Fatal("invalid verification accepted")
			}
		})
	}
}

func TestSyntheticClaudeEnvelope(t *testing.T) {
	for _, mode := range []string{"valid", "missing", "error", "oversized"} {
		t.Run(mode, func(t *testing.T) {
			req := fixture(t)
			req.Agent = "claude"
			review := Review{CandidateSHA: req.CandidateSHA, Summary: "Synthetic Claude fixture with substantive summary.", BlockingFindings: []string{"price.go:4: incorrect discount arithmetic in synthetic fixture."}, Approved: false}
			envelope := map[string]any{"type": "result", "subtype": "success", "is_error": false, "structured_output": review}
			switch mode {
			case "missing":
				delete(envelope, "structured_output")
			case "error":
				envelope["is_error"] = true
			case "oversized":
				envelope["padding"] = strings.Repeat("x", maxEnvelope)
			}
			b, _ := json.Marshal(envelope)
			p := filepath.Join(t.TempDir(), "claude-fixture")
			os.WriteFile(p, []byte("#!/bin/sh\ncat >/dev/null\ncat <<'FIXTURE'\n"+string(b)+"\nFIXTURE\n"), 0700)
			r, e := run(context.Background(), req, p)
			if mode == "valid" {
				if e != nil || r.Review == nil || r.Accepted {
					t.Fatal(r, e)
				}
			} else if e == nil || r.Accepted {
				t.Fatal(r, e)
			}
			if r.ExitCode == nil || *r.ExitCode != 0 {
				t.Fatal("lost actual exit zero")
			}
		})
	}
}

func TestSourceAuditIgnoresRepositoryConfig(t *testing.T) {
	req := fixture(t)
	marker := filepath.Join(filepath.Dir(req.ResultPath), "hook-ran")
	hook := filepath.Join(t.TempDir(), "hook")
	os.WriteFile(hook, []byte("#!/bin/sh\ntouch '"+marker+"'\n"), 0700)
	os.WriteFile(filepath.Join(req.Workspace, ".git/config"), []byte("[core]\n bare = false\n fsmonitor = "+hook+"\n[diff]\n external = "+hook+"\n"), 0600)
	r, e := run(context.Background(), req, cli(t, req, "pass"))
	if e != nil || !r.Accepted {
		t.Fatal(r, e)
	}
	if _, e = os.Stat(marker); !os.IsNotExist(e) {
		t.Fatal("repository hook executed")
	}
}

func TestSourceLimit(t *testing.T) {
	req := fixture(t)
	// Full-source input is deliberately bounded; no silently partial review.
	os.WriteFile(filepath.Join(req.Workspace, "large.txt"), []byte(strings.Repeat("x", maxSource+1)), 0600)
	for _, args := range [][]string{{"add", "large.txt"}, {"-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-m", "oversized fixture"}} {
		c := exec.Command("/usr/bin/git", args...)
		c.Dir = req.Workspace
		if b, e := c.CombinedOutput(); e != nil {
			t.Fatalf("%v %s", e, b)
		}
	}
	var source string
	if _, _, e := inspect(context.Background(), req.Workspace, req.BaseSHA, &source); e == nil {
		t.Fatal("oversized source accepted")
	}
}
