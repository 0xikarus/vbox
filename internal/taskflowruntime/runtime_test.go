package taskflowruntime

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/0xikarus/vmbox-service/internal/factory/resultinbox"
	"github.com/0xikarus/vmbox-service/internal/procexec"
	"github.com/0xikarus/vmbox-service/internal/provider"
	"github.com/0xikarus/vmbox-service/internal/taskflow"
	"github.com/0xikarus/vmbox-service/internal/transport"
)

func writeTest(t *testing.T, p string, b []byte, mode os.FileMode) {
	t.Helper()
	if e := os.WriteFile(p, b, mode); e != nil {
		t.Fatal(e)
	}
}
func newID() string { b := make([]byte, 16); _, _ = rand.Read(b); return hex.EncodeToString(b) }

func TestTypedResults(t *testing.T) {
	good := `{"text":"A plan","plan":{"revision":1,"summary":"Research a family trip","questions":[],"assignments":[{"id":"a","title":"Budget","instruction":"Compare travel costs","acceptanceCriteria":["State assumptions"],"dependsOn":[]},{"id":"b","title":"Itinerary","instruction":"Produce itinerary","acceptanceCriteria":["Fits budget"],"dependsOn":["a"]}]}}`
	if _, e := ValidateResult([]byte(good), "plan", 1); e != nil {
		t.Fatal(e)
	}
	for _, bad := range []string{strings.Replace(good, `"revision":1`, `"revision":2`, 1), strings.Replace(good, `"dependsOn":[]`, `"dependsOn":["b"]`, 1), strings.Replace(good, `"id":"b"`, `"id":"a"`, 1), strings.Replace(good, `["State assumptions"]`, `[]`, 1), strings.Replace(good, `"questions":[]`, `"questions":["Which city?"]`, 1), strings.Replace(good, `"text":"A plan"`, `"text":"A plan","text":"duplicate"`, 1), good + ` {}`} {
		if _, e := ValidateResult([]byte(bad), "plan", 1); e == nil {
			t.Fatalf("accepted %s", bad)
		}
	}
	for _, x := range []struct {
		stage, body string
		ok          bool
	}{{"work", `{"text":"Useful answer"}`, true}, {"work", `{"text":"Answer","verdict":"accepted"}`, false}, {"synthesize", `{"text":"Answer","verdict":"accepted"}`, true}, {"synthesize", `{"text":"Answer"}`, false}, {"work", `{"text":"Answer","unknown":1}`, false}, {"plan", `{"text":"Need detail","plan":{"revision":1,"summary":"Clarification","questions":["Budget?"],"assignments":[]}}`, true}} {
		_, e := ValidateResult([]byte(x.body), x.stage, 1)
		if (e == nil) != x.ok {
			t.Fatalf("%s: %v", x.body, e)
		}
	}
}

// SSH fixture executes the production framed staging program in a real shell,
// substituting only its fixed destination root and workload-user transition.
type sshFixture struct {
	t       *testing.T
	root    string
	corrupt bool
}

func (f *sshFixture) Run(context.Context, []string, io.Reader, io.Writer, io.Writer) (procexec.Result, error) {
	f.t.Fatal("capturing SSH used")
	return procexec.Result{}, nil
}
func (f *sshFixture) RunAttached(ctx context.Context, args []string, in io.Reader, out, errout io.Writer) (procexec.Result, error) {
	f.t.Helper()
	if args[0] != "ssh" || args[len(args)-2] != "box@example.test" || out != io.Discard || errout != io.Discard {
		f.t.Fatal("wrong SSH transport")
	}
	command := args[len(args)-1]
	quoted := []string{}
	for _, arg := range provider.AsWorkloadUser(nil) {
		quoted = append(quoted, "'"+strings.ReplaceAll(arg, "'", "'\"'\"'")+"'")
	}
	prefix := strings.Join(quoted, " ") + " "
	if !strings.HasPrefix(command, prefix) {
		f.t.Fatal("missing workload identity")
	}
	if strings.Contains(command, "private-job-value") || strings.Contains(command, "fixture-image") {
		f.t.Fatal("private data in argv")
	}
	command = strings.ReplaceAll(strings.TrimPrefix(command, prefix), taskRoot, f.root)
	if f.corrupt {
		b, _ := io.ReadAll(in)
		b[len(b)-1] ^= 1
		in = bytes.NewReader(b)
	}
	cmd := exec.CommandContext(ctx, "/bin/bash", "--noprofile", "--norc", "-c", command)
	cmd.Stdin = in
	cmd.Stdout = out
	cmd.Stderr = errout
	e := cmd.Run()
	if e != nil {
		return procexec.Result{ExitCode: 1}, e
	}
	return procexec.Result{}, nil
}

func TestPrivateSSHStaging(t *testing.T) {
	base, e := os.MkdirTemp("/data/workspace", "task-stage-fixture-")
	if e != nil {
		t.Fatal(e)
	}
	defer os.RemoveAll(base)
	binary := filepath.Join(base, "runner")
	writeTest(t, binary, []byte("fixture-binary"), 0700)
	f := &sshFixture{t: t, root: filepath.Join(base, "tasks")}
	s := Stager{SSH: transport.SSH{Runner: f}, BinaryPath: binary}
	in := Input{Connection: provider.Connection{Transport: "openssh", Endpoint: "box@example.test"}, AttemptID: newID(), Job: []byte(`{"value":"private-job-value"}`), Images: []Image{{ID: newID(), Data: []byte("fixture-image")}}}
	for i := 0; i < 2; i++ {
		if e = s.Stage(context.Background(), in); e != nil {
			t.Fatal(e)
		}
	}
	job := filepath.Join(f.root, "attempts", in.AttemptID, "job.json")
	b, e := os.ReadFile(job)
	if e != nil || !bytes.Equal(b, in.Job) {
		t.Fatal("job changed", e)
	}
	st, _ := os.Stat(job)
	if st.Mode().Perm() != 0600 {
		t.Fatal("job permissions")
	}
	in.Job = []byte(`{"value":"changed"}`)
	if e = s.Stage(context.Background(), in); e == nil {
		t.Fatal("restaged changed attempt")
	}
	in.AttemptID = newID()
	f.corrupt = true
	if e = s.Stage(context.Background(), in); e == nil {
		t.Fatal("corruption accepted")
	}
	if _, e = os.Stat(filepath.Join(f.root, "attempts", in.AttemptID)); !os.IsNotExist(e) {
		t.Fatal("partial published")
	}
}

// This is a real compiled wrapper/real OS process fixture, not a live model trial.
// The fake CLI exercises dedicated output files, exit/signal and secret denial.
func TestCompiledWrapperTLSAndNoReplay(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "vmbox-task-runner")
	goBin := filepath.Join(os.Getenv("GOROOT"), "bin", "go")
	if os.Getenv("GOROOT") == "" {
		var e error
		goBin, e = exec.LookPath("go")
		if e != nil {
			t.Fatal("run tests with toolchain bin on PATH")
		}
	}
	build := exec.Command(goBin, "build", "-o", binary, "../../cmd/vmbox-task-runner")
	if b, e := build.CombinedOutput(); e != nil {
		t.Fatalf("build: %v %s", e, b)
	}
	for _, mode := range []string{"success", "nonzero", "signal", "invalid", "claude", "startup"} {
		t.Run(mode, func(t *testing.T) {
			id := newID()
			dir := taskRoot + "/attempts/" + id
			if e := os.MkdirAll(dir+"/workspace", 0700); e != nil {
				t.Fatal(e)
			}
			defer os.RemoveAll(dir)
			home := t.TempDir()
			if e := os.Mkdir(home+"/bin", 0700); e != nil {
				t.Fatal(e)
			}
			secret := strings.Repeat("s", 43)
			// HOME remains the normal selected-profile location; the test only substitutes
			// a synthetic home and CLI. No host login files or credentials are inspected.
			script := fmt.Sprintf(`#!/bin/bash
set -eu
printf 'invoked\n' >> "$HOME/calls"
[[ -z "${CONTROLLER_TOKEN:-}" && -z "${OPENAI_API_KEY:-}" && -z "${DELIVERY_TOKEN:-}" ]] || exit 71
if cat '%s/job.json' >/dev/null 2>&1; then exit 72; fi
cat > "$HOME/prompt"
if grep -q '%s' "$HOME/prompt"; then exit 73; fi
case '%s' in
 nonzero) exit 23;;
 signal) kill -TERM $$;;
 claude) printf '%%s' '{"type":"result","subtype":"success","is_error":false,"structured_output":{"text":"fixture answer"}}'; exit 0;;
esac
out=
while (($#)); do case "$1" in --output-last-message) out=$2;shift;; esac;shift;done
[[ -n "$out" ]] || exit 74
if [[ '%s' == invalid ]]; then printf 'not json' > "$out"; else printf '%%s' '{"text":"fixture answer"}' > "$out"; fi
`, dir, secret, mode, mode)
			writeTest(t, home+"/bin/codex", []byte(script), 0700)
			writeTest(t, home+"/bin/claude", []byte(script), 0700)
			if mode == "startup" {
				if e := os.Chmod(home+"/bin/codex", 0600); e != nil {
					t.Fatal(e)
				}
			}
			var mu sync.Mutex
			var received [][]byte
			allow := false
			attempts := 0
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/result" || r.Header.Get("Authorization") != "Bearer "+secret {
					t.Error("callback scope")
					w.WriteHeader(401)
					return
				}
				b, _ := io.ReadAll(r.Body)
				if _, e := resultinbox.Decode(b); e != nil {
					t.Error(e)
					w.WriteHeader(400)
					return
				}
				mu.Lock()
				defer mu.Unlock()
				attempts++
				if !allow {
					w.WriteHeader(503)
					return
				}
				received = append(received, b)
				w.WriteHeader(204)
			}))
			defer server.Close()
			ca := filepath.Join(t.TempDir(), "ca.pem")
			writeTest(t, ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600)
			agent := "codex"
			if mode == "claude" {
				agent = "claude"
			}
			job := Job{Version: 1, AttemptID: id, Request: Request{Agent: agent, Stage: "work", Revision: 1, Workspace: dir + "/workspace", ResultPath: dir + "/result.json", Prompt: "Write a useful travel recommendation using the provided budget."}, ReceiptPath: dir + "/receipt.json", DeliveryURL: server.URL + "/result", DeliveryToken: secret}
			body, _ := json.Marshal(job)
			writeTest(t, dir+"/job.json", body, 0600)
			launch := func(ctx context.Context) *exec.Cmd {
				cmd := exec.CommandContext(ctx, binary)
				cmd.Stdin = bytes.NewReader(body)
				cmd.Env = append(os.Environ(), "HOME="+home, "PATH="+home+"/bin:/usr/bin:/bin", "SSL_CERT_FILE="+ca, "CONTROLLER_TOKEN=must-not-inherit", "OPENAI_API_KEY=must-not-inherit", "DELIVERY_TOKEN=must-not-inherit")
				return cmd
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			cmd := launch(ctx)
			var diagnostic bytes.Buffer
			cmd.Stderr = &diagnostic
			if e := cmd.Start(); e != nil {
				t.Fatal(e)
			}
			deadline := time.Now().Add(4 * time.Second)
			seen := false
			for time.Now().Before(deadline) {
				mu.Lock()
				seen = attempts > 0
				mu.Unlock()
				if seen {
					break
				}
				time.Sleep(20 * time.Millisecond)
			}
			if !seen {
				cancel()
				_ = cmd.Wait()
				t.Fatalf("no callback; stderr=%s", diagnostic.String())
			}
			if _, e := os.Stat(dir + "/receipt.json"); e != nil {
				t.Fatal("callback preceded durable receipt", e)
			}
			// Stop delivery only after the report is durable. Restart the exact job and
			// accept its saved bytes; there must still be exactly one CLI invocation.
			cancel()
			_ = cmd.Wait()
			mu.Lock()
			allow = true
			mu.Unlock()
			ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel2()
			if b, e := launch(ctx2).CombinedOutput(); e != nil {
				t.Fatalf("redelivery: %v %s", e, b)
			}
			calls, _ := os.ReadFile(home + "/calls")
			expectedCalls := "invoked\n"
			if mode == "startup" {
				expectedCalls = ""
			}
			if string(calls) != expectedCalls {
				t.Fatalf("reexecution: %q", calls)
			}
			// An ambiguous crash after the start marker must never restart paid work.
			if mode == "success" {
				if e := os.Remove(dir + "/receipt.json"); e != nil {
					t.Fatal(e)
				}
				if b, e := launch(ctx2).CombinedOutput(); e == nil || !strings.Contains(string(b), "was started") {
					t.Fatalf("missing receipt replayed: %v %s", e, b)
				}
				calls, _ = os.ReadFile(home + "/calls")
				if string(calls) != expectedCalls {
					t.Fatal("ambiguous outcome reran agent")
				}
			}
			mu.Lock()
			defer mu.Unlock()
			if len(received) != 1 {
				t.Fatalf("receipts %d", len(received))
			}
			env, e := resultinbox.Decode(received[0])
			if e != nil {
				t.Fatal(e)
			}
			var report Report
			if e = strictJSON(env.Document, &report); e != nil {
				t.Fatal(e)
			}
			switch mode {
			case "startup":
				if report.AgentEvidence || report.Failure == "" {
					t.Fatalf("fabricated agent evidence %+v", report)
				}
			case "success", "claude":
				if env.ExitCode == nil || *env.ExitCode != 0 || report.Result.Text != "fixture answer" || report.Failure != "" {
					t.Fatalf("bad success %+v %+v", env, report)
				}
			case "nonzero":
				if env.ExitCode == nil || *env.ExitCode != 23 || report.Failure == "" {
					t.Fatalf("bad exit %+v", env)
				}
			case "signal":
				if env.ExitCode != nil || env.Signal != 15 {
					t.Fatalf("bad signal %+v", env)
				}
			case "invalid":
				if env.ExitCode == nil || *env.ExitCode != 0 || report.Failure == "" {
					t.Fatalf("bad invalid output %+v", env)
				}
			}
		})
	}
}

func TestPromptSnapshot(t *testing.T) {
	w := taskflow.Workflow{ID: newID(), Agent: "codex", Idea: "Plan a family trip", Messages: []taskflow.Message{{Role: "user", Text: "Budget is 1000"}}, Attempts: []taskflow.Attempt{{ID: newID(), Output: "Actual hotel cost: 300", Failure: "transport unavailable"}}}
	r, e := requestFor(taskflow.Input{AccountID: "a", Workflow: w, Attempt: taskflow.Attempt{ID: newID(), Stage: "plan"}})
	if e != nil {
		t.Fatal(e)
	}
	for _, s := range []string{w.Idea, w.Messages[0].Text, w.Attempts[0].Output, w.Attempts[0].Failure} {
		if !strings.Contains(r.Prompt, s) {
			t.Fatal("snapshot omitted actual context")
		}
	}
}

func TestAgentDeadlineHasActualSignal(t *testing.T) {
	base := t.TempDir()
	executable := filepath.Join(base, "slow-agent")
	writeTest(t, executable, []byte("#!/bin/bash\nexec /bin/sleep 30\n"), 0700)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	r, e := run(ctx, Request{Agent: "codex", Stage: "work", Revision: 1, Workspace: base, ResultPath: base + "/result.json", Prompt: "Bounded deadline fixture"}, executable)
	if e == nil || !r.AgentEvidence || r.ExitCode != nil || r.Signal != 9 || time.Since(started) > 5*time.Second {
		t.Fatalf("deadline evidence %+v %v", r, e)
	}
}

func TestCodexImagesAndTruthfulClaudeCapability(t *testing.T) {
	base := t.TempDir()
	var picture bytes.Buffer
	if e := png.Encode(&picture, image.NewRGBA(image.Rect(0, 0, 2, 2))); e != nil {
		t.Fatal(e)
	}
	imagePath := filepath.Join(base, "image.png")
	writeTest(t, imagePath, picture.Bytes(), 0600)
	executable := filepath.Join(base, "image-agent")
	writeTest(t, executable, []byte(`#!/bin/bash
set -eu
image=;out=
while (($#)); do case "$1" in --image) image=$2;shift;; --output-last-message) out=$2;shift;; esac;shift;done
[[ -r "$image" && -n "$out" ]] || exit 75
printf '%s' '{"text":"image fixture"}' > "$out"
`), 0700)
	req := Request{Agent: "codex", Stage: "work", Revision: 1, Workspace: base, ResultPath: base + "/result.json", Prompt: "Describe the supplied image", Images: []string{imagePath}}
	r, e := run(context.Background(), req, executable)
	if e != nil || r.ExitCode == nil || *r.ExitCode != 0 {
		t.Fatalf("image adapter %+v %v", r, e)
	}
	req.Agent = "claude"
	req.ResultPath = base + "/claude-result.json"
	r, e = run(context.Background(), req, executable)
	if e == nil || r.AgentEvidence || ImageCapabilities()["claude"] {
		t.Fatal("Claude images advertised or executed")
	}
}
