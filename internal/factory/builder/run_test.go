package builder

import (
	"context"
	"encoding/json"
	"image"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func gitTest(t *testing.T, dir string, args ...string) string {
	t.Helper()
	c := exec.Command("git", append([]string{"-C", dir}, args...)...)
	b, e := c.CombinedOutput()
	if e != nil {
		t.Fatalf("git %v: %v %s", args, e, b)
	}
	return strings.TrimSpace(string(b))
}
func request(t *testing.T) Request {
	t.Helper()
	d := t.TempDir()
	gitTest(t, d, "init", "-q")
	gitTest(t, d, "config", "user.name", "Builder Test")
	gitTest(t, d, "config", "user.email", "builder@example.invalid")
	os.WriteFile(filepath.Join(d, "math.js"), []byte("exports.double = n => n;\n"), 0600)
	gitTest(t, d, "add", ".")
	gitTest(t, d, "commit", "-qm", "baseline")
	return Request{Agent: "codex", Workspace: d, Prompt: "Make double(n) return twice n.", Branch: "factory/test", BaseSHA: gitTest(t, d, "rev-parse", "HEAD"), ResultPath: filepath.Join(t.TempDir(), "result.json")}
}
func controlled(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "fixture-cli")
	os.WriteFile(p, []byte("#!/bin/sh\nset -e\ncat >/dev/null\n"+body), 0700)
	return p
}

const commitFixture = "printf 'exports.double = n => 2*n;\\n' > math.js\ngit add math.js\ngit commit -qm implemented\n"

func TestControlledEvidence(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		valid      bool
		exit       int
	}{{"commit", commitFixture, true, 0}, {"prose", "echo 'implemented and all tests passed'", false, 0}, {"dirty", commitFixture + "echo x > stray", false, 0}, {"failure", commitFixture + "exit 7", false, 7}, {"wrong-branch", commitFixture + "git checkout -qb factory/other", false, 0}, {"detached", commitFixture + "git checkout -q --detach", false, 0}, {"unrelated", "git checkout -q --orphan factory/other\n" + commitFixture, false, 0}, {"large", commitFixture + "head -c 1100000 /dev/zero", true, 0}} {
		t.Run(tc.name, func(t *testing.T) {
			r := request(t)
			got, e := run(context.Background(), r, controlled(t, tc.body))
			if (e == nil) != tc.valid || (got.CandidateSHA != "") != tc.valid || got.ExitCode == nil || *got.ExitCode != tc.exit {
				t.Fatalf("%+v %v", got, e)
			}
			if tc.name == "large" && !got.Truncated {
				t.Fatal("not bounded")
			}
			b, e := os.ReadFile(r.ResultPath)
			if e != nil {
				t.Fatal(e)
			}
			var saved Result
			if json.Unmarshal(b, &saved) != nil || saved.Summary != got.Summary || len(b) > 2048 {
				t.Fatal("bad persistence")
			}
			if strings.Contains(string(b), "all tests passed") {
				t.Fatal("prose leaked")
			}
		})
	}
}
func TestControlledPreconditions(t *testing.T) {
	for _, kind := range []string{"dirty", "ignored", "head", "branch", "result", "inside", "claude-image", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			r := request(t)
			switch kind {
			case "dirty":
				os.WriteFile(filepath.Join(r.Workspace, "x"), []byte("x"), 0600)
			case "ignored":
				os.WriteFile(filepath.Join(r.Workspace, ".git", "info", "exclude"), []byte("x\n"), 0600)
				os.WriteFile(filepath.Join(r.Workspace, "x"), []byte("x"), 0600)
			case "head":
				r.BaseSHA = strings.Repeat("0", 40)
			case "branch":
				gitTest(t, r.Workspace, "branch", r.Branch)
			case "result":
				os.WriteFile(r.ResultPath, []byte("preserve"), 0600)
			case "inside":
				r.ResultPath = filepath.Join(r.Workspace, "result")
			case "claude-image":
				r.Agent = "claude"
				r.Images = []string{"/missing"}
			case "symlink":
				p := filepath.Join(t.TempDir(), "link")
				os.Symlink(r.Workspace, p)
				r.Workspace = p
			}
			if _, e := run(context.Background(), r, "must-not-run"); e == nil {
				t.Fatal("accepted")
			}
			if kind == "result" {
				b, _ := os.ReadFile(r.ResultPath)
				if string(b) != "preserve" {
					t.Fatal("overwritten")
				}
			}
		})
	}
}
func TestControlledStartAndSignal(t *testing.T) {
	for _, body := range []string{"missing", "kill -TERM $$"} {
		r := request(t)
		exe := "/does/not/exist"
		if body != "missing" {
			exe = controlled(t, body)
		}
		got, e := run(context.Background(), r, exe)
		if e == nil || got.ExitCode != nil {
			t.Fatalf("%+v %v", got, e)
		}
		if body != "missing" && got.Signal != 15 {
			t.Fatal(got)
		}
	}
}
func TestControlledCancellationOwnsChildren(t *testing.T) {
	r := request(t)
	marker := filepath.Join(t.TempDir(), "escaped")
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	got, e := run(ctx, r, controlled(t, "(sleep 1; touch '"+marker+"') &\nwait\n"))
	if e == nil || got.Signal != 9 || got.CandidateSHA != "" {
		t.Fatalf("%+v %v", got, e)
	}
	time.Sleep(1100 * time.Millisecond)
	if _, e = os.Stat(marker); !os.IsNotExist(e) {
		t.Fatal("child survived")
	}
}
func TestControlledArguments(t *testing.T) {
	for _, agent := range []string{"codex", "claude"} {
		r := request(t)
		r.Agent = agent
		capture := filepath.Join(t.TempDir(), "args")
		_, _ = run(context.Background(), r, controlled(t, "printf '%s\\n' \"$@\" > '"+capture+"'\n"+commitFixture))
		b, _ := os.ReadFile(capture)
		s := string(b)
		for _, bad := range []string{"--model", "dangerously", "bypassPermissions", "read-only", "\nplan\n"} {
			if strings.Contains(s, bad) {
				t.Fatal(s)
			}
		}
		want := "--approve-for-me"
		if agent == "claude" {
			want = "acceptEdits"
		}
		if !strings.Contains(s, want) {
			t.Fatal(s)
		}
	}
}
func TestAtomicNoReplace(t *testing.T) {
	d := t.TempDir()
	f, e := openPath(d, true)
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	os.WriteFile(filepath.Join(d, "r"), []byte("old"), 0600)
	if persist(f, "r", []byte("new")) == nil {
		t.Fatal("replaced")
	}
}
func TestLiveImplementation(t *testing.T) {
	agent := os.Getenv("BUILDER_LIVE_AGENT")
	if agent == "" {
		t.Skip("opt-in actual installed CLI; controlled fixtures are separate")
	}
	r := request(t)
	r.Agent = agent
	r.Prompt = "Approved tiny feature: fix math.js double(n) so it returns twice its numeric argument. Preserve the function name. Acceptance: requiring the module and calling double(0), double(3), double(-4), double(1.5) yields 0, 6, -8, 3. Make the code change, run a small exploratory check, and commit it. Do not add dependencies."
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	got, e := Run(ctx, r)
	if e != nil {
		t.Fatalf("REAL %s exit=%v result=%+v error=%v", agent, exitValue(got), got, e)
	}
	c := exec.Command("node", "-e", "const f=require('./math.js').double; require('node:assert/strict').deepEqual([0,3,-4,1.5].map(f),[0,6,-8,3])")
	c.Dir = r.Workspace
	if b, e := c.CombinedOutput(); e != nil {
		t.Fatalf("independent acceptance failed: %v %s", e, b)
	}
	src := gitTest(t, r.Workspace, "show", got.CandidateSHA+":math.js")
	t.Logf("REAL %s exit=%v result=%+v\nauthored source:\n%s", agent, exitValue(got), got, src)
}

func exitValue(r Result) any {
	if r.ExitCode == nil {
		return nil
	}
	return *r.ExitCode
}

func TestControlledRetryRejected(t *testing.T) {
	r := request(t)
	if _, e := run(context.Background(), r, controlled(t, commitFixture)); e != nil {
		t.Fatal(e)
	}
	before, _ := os.ReadFile(r.ResultPath)
	if _, e := run(context.Background(), r, "must-not-run"); e == nil {
		t.Fatal("retry accepted")
	}
	after, _ := os.ReadFile(r.ResultPath)
	if string(before) != string(after) {
		t.Fatal("evidence replaced")
	}
}

func TestControlledImageInput(t *testing.T) {
	r := request(t)
	p := filepath.Join(t.TempDir(), "input.png")
	f, e := os.Create(p)
	if e != nil {
		t.Fatal(e)
	}
	if e = png.Encode(f, image.NewRGBA(image.Rect(0, 0, 2, 2))); e != nil {
		t.Fatal(e)
	}
	f.Close()
	r.Images = []string{p}
	body := `image_path=""
while [ "$#" -gt 0 ]; do
 if [ "$1" = "--image" ]; then shift; image_path="$1"; fi
 shift
done
[ -f "$image_path" ] || exit 31
` + commitFixture
	if got, e := run(context.Background(), r, controlled(t, body)); e != nil || got.CandidateSHA == "" {
		t.Fatalf("%+v %v", got, e)
	}
	r = request(t)
	os.WriteFile(p, []byte("corrupt"), 0600)
	r.Images = []string{p}
	if _, e := run(context.Background(), r, "must-not-run"); e == nil {
		t.Fatal("invalid image accepted")
	}
}
