package verifyjob

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/0xikarus/vmbox-service/internal/factory"
	"github.com/0xikarus/vmbox-service/internal/factory/verification"
)

var binary string

func TestMain(m *testing.M) {
	dir, e := os.MkdirTemp("", "verifyjob-test-")
	if e != nil {
		panic(e)
	}
	binary = filepath.Join(dir, "vmbox-verifier")
	cmd := exec.Command(filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-o", binary, "../../../cmd/vmbox-verifier")
	if b, e := cmd.CombinedOutput(); e != nil {
		panic(string(b))
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

type fixture struct {
	job    Job
	cert   string
	count  string
	mu     sync.Mutex
	bodies [][]byte
	status int
	server *httptest.Server
}

func setup(t *testing.T, script string) *fixture {
	t.Helper()
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	attempt := filepath.Join(root, "attempt")
	for _, p := range []string{repo, attempt} {
		if e := os.Mkdir(p, 0700); e != nil {
			t.Fatal(e)
		}
	}
	git := func(args ...string) string {
		c := exec.Command("/usr/bin/git", args...)
		c.Dir = repo
		b, e := c.CombinedOutput()
		if e != nil {
			t.Fatalf("git: %s %v", b, e)
		}
		return strings.TrimSpace(string(b))
	}
	git("init", "--template=")
	git("-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-m", "candidate")
	f := &fixture{count: filepath.Join(root, "count"), status: 200}
	script = strings.ReplaceAll(script, "COUNT", f.count)
	f.job = Job{Version: 1, AttemptID: strings.Repeat("a", 32), DeliveryToken: base64.RawURLEncoding.EncodeToString(make([]byte, 32)), ReceiptPath: filepath.Join(attempt, "receipt.json"), Request: verification.Request{Workspace: repo, ExpectedSHA: git("rev-parse", "HEAD"), OutputDir: attempt, Checks: []factory.Check{{Argv: []string{"/bin/sh", "-c", script}, Cwd: ".", TimeoutSeconds: 10}}}}
	f.server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b bytes.Buffer
		b.ReadFrom(r.Body)
		f.mu.Lock()
		defer f.mu.Unlock()
		f.bodies = append(f.bodies, append([]byte(nil), b.Bytes()...))
		if r.Header.Get("Authorization") != "Bearer "+f.job.DeliveryToken {
			t.Error("missing capability")
		}
		w.WriteHeader(f.status)
	}))
	t.Cleanup(f.server.Close)
	f.job.DeliveryURL = f.server.URL
	f.cert = filepath.Join(root, "ca.pem")
	os.WriteFile(f.cert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: f.server.Certificate().Raw}), 0600)
	return f
}
func (f *fixture) command(data []byte) (*exec.Cmd, *bytes.Buffer) {
	if data == nil {
		data, _ = json.Marshal(f.job)
	}
	c := exec.Command(binary)
	c.Stdin = bytes.NewReader(data)
	c.Env = append(os.Environ(), "SSL_CERT_FILE="+f.cert, "PROVIDER_SECRET=must-not-inherit")
	b := &bytes.Buffer{}
	c.Stdout = b
	c.Stderr = b
	return c, b
}
func (f *fixture) read(t *testing.T) (receipt, Report) {
	t.Helper()
	b, e := os.ReadFile(f.job.ReceiptPath)
	if e != nil {
		t.Fatal(e)
	}
	var s receipt
	var r Report
	if decode(b, &s) != nil || decode(s.Result.Document, &r) != nil {
		t.Fatal("bad receipt")
	}
	if bytes.Contains(b, []byte(f.job.DeliveryToken)) {
		t.Fatal("capability persisted")
	}
	return s, r
}
func TestActualPassFailAndRedelivery(t *testing.T) {
	for _, code := range []string{"0", "7"} {
		t.Run(code, func(t *testing.T) {
			f := setup(t, "echo run >> COUNT; test -z \"$PROVIDER_SECRET$SSL_CERT_FILE\" || exit 99; echo private-output; exit "+code)
			f.status = 403
			c, _ := f.command(nil)
			if c.Run() == nil {
				t.Fatal("refusal succeeded")
			}
			s, r := f.read(t)
			if s.Result.ExitCode == nil || *s.Result.ExitCode != 0 {
				t.Fatal("child process exit should describe report completion", s)
			}
			check := r.Verification.Checks[0]
			want := 0
			if code == "7" {
				want = 7
			}
			if check.ExitCode == nil || *check.ExitCode != want || r.Accepted != (want == 0) {
				t.Fatalf("lost individual exit: %+v %+v", r, check)
			}
			log := filepath.Join(r.Verification.EvidenceDir, check.Stdout.File)
			st, e := os.Stat(log)
			if e != nil || st.Mode().Perm() != 0600 {
				t.Fatal("private log missing", e)
			}
			f.mu.Lock()
			f.status = 200
			f.mu.Unlock()
			c, _ = f.command(nil)
			if e := c.Run(); e != nil {
				t.Fatal(e)
			}
			b, _ := os.ReadFile(f.count)
			if string(b) != "run\n" {
				t.Fatal("reexecuted", string(b))
			}
			f.mu.Lock()
			if len(f.bodies) != 2 || !bytes.Equal(f.bodies[0], f.bodies[1]) {
				t.Error("redelivery changed")
			}
			f.mu.Unlock()
			f.job.Request.Checks[0].Argv = append(f.job.Request.Checks[0].Argv, "changed")
			c, _ = f.command(nil)
			if c.Run() == nil {
				t.Fatal("changed request accepted")
			}
		})
	}
}
func TestActualCancel(t *testing.T) {
	f := setup(t, "echo run >> COUNT; sleep 60")
	c, b := f.command(nil)
	if e := c.Start(); e != nil {
		t.Fatal(e)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, e := os.Stat(f.count); e == nil {
			break
		}
		if time.Now().After(deadline) {
			c.Process.Kill()
			t.Fatal("check not started", b.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
	c.Process.Signal(syscall.SIGTERM)
	if e := c.Wait(); e != nil {
		t.Fatal("cancellation evidence delivery failed", e, b.String())
	}
	s, r := f.read(t)
	if s.Result.ExitCode == nil || *s.Result.ExitCode != 1 || r.Accepted || r.ErrorCode != "verification_incomplete" {
		t.Fatal("wrong process evidence", s, r)
	}
	check := r.Verification.Checks[0]
	if !check.Cancel || check.Signal != 9 || check.ExitCode != nil {
		t.Fatalf("wrong cancellation evidence %+v", check)
	}
}
func TestMalformedAndPaths(t *testing.T) {
	cases := []string{"unknown", "duplicate", "trailing", "oversize", "http", "token", "relative", "symlink", "public", "cwd", "sha", "started", "receipt-symlink"}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			f := setup(t, "echo run >> COUNT")
			var data []byte
			switch name {
			case "unknown":
				data = []byte(`{"version":1,"bogus":true}`)
			case "duplicate":
				data = []byte(`{"version":1,"Version":1}`)
			case "trailing":
				data = []byte(`{} {}`)
			case "oversize":
				data = bytes.Repeat([]byte("x"), 200001)
			case "http":
				f.job.DeliveryURL = "http://example.invalid"
			case "token":
				f.job.DeliveryToken = "bad"
			case "relative":
				f.job.ReceiptPath = "receipt.json"
			case "symlink":
				p := f.job.Request.OutputDir + "-link"
				os.Symlink(f.job.Request.OutputDir, p)
				f.job.Request.OutputDir = p
				f.job.ReceiptPath = filepath.Join(p, "receipt.json")
			case "public":
				os.Chmod(f.job.Request.OutputDir, 0755)
			case "cwd":
				f.job.Request.Checks[0].Cwd = "../repo"
			case "sha":
				f.job.Request.ExpectedSHA = "bad"
			case "started":
				os.WriteFile(filepath.Join(f.job.Request.OutputDir, ".job.started"), []byte("ambiguous"), 0600)
			case "receipt-symlink":
				os.Symlink(f.count, f.job.ReceiptPath)
			}
			c, _ := f.command(data)
			if c.Run() == nil {
				t.Fatal("accepted invalid input")
			}
			if _, e := os.Stat(f.count); !os.IsNotExist(e) {
				t.Fatal("executed invalid job")
			}
		})
	}
}
func TestExactCandidateMismatch(t *testing.T) {
	f := setup(t, "echo run >> COUNT")
	f.job.Request.ExpectedSHA = strings.Repeat("b", 40)
	c, _ := f.command(nil)
	if e := c.Run(); e != nil {
		t.Fatal(e)
	}
	s, r := f.read(t)
	if *s.Result.ExitCode != 1 || r.Accepted {
		t.Fatal("mismatch accepted")
	}
	if _, e := os.Stat(f.count); !os.IsNotExist(e) {
		t.Fatal("mismatch executed")
	}
}

func TestActualChildSignal(t *testing.T) {
	f := setup(t, `echo run >> COUNT; kill -KILL "$PPID"; exit 0`)
	c, _ := f.command(nil)
	if e := c.Run(); e != nil {
		t.Fatal(e)
	}
	s, r := f.read(t)
	if s.Result.ExitCode != nil || s.Result.Signal != 9 || r.Verification != nil || r.Accepted || r.ErrorCode != "verification_incomplete" {
		t.Fatal("fabricated process/report evidence", s, r)
	}
	c, _ = f.command(nil)
	if e := c.Run(); e != nil {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(f.count)
	if string(b) != "run\n" {
		t.Fatal("signal reran")
	}
}
func TestActualTruncatedLog(t *testing.T) {
	f := setup(t, `head -c 1100000 /dev/zero`)
	c, _ := f.command(nil)
	if e := c.Run(); e != nil {
		t.Fatal(e)
	}
	s, r := f.read(t)
	check := r.Verification.Checks[0]
	if *s.Result.ExitCode != 0 || r.Accepted || !check.Stdout.Truncated || check.Stdout.Bytes != verification.LogLimit || *check.ExitCode != 0 {
		t.Fatal("lost truncation or exit evidence", r)
	}
}
func TestDeliveryBounds(t *testing.T) {
	for _, status := range []int{302, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			f := setup(t, "echo run >> COUNT")
			f.status = status
			c, _ := f.command(nil)
			if c.Run() == nil {
				t.Fatal("bad callback accepted")
			}
			f.read(t)
			want := 1
			if status == 503 {
				want = 3
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if len(f.bodies) != want {
				t.Fatal("wrong retry count", len(f.bodies))
			}
			for _, b := range f.bodies {
				if !bytes.Equal(b, f.bodies[0]) {
					t.Fatal("retry changed")
				}
			}
		})
	}
}
func TestRedeliveryWithoutCheckout(t *testing.T) {
	f := setup(t, "echo run >> COUNT")
	c, _ := f.command(nil)
	if e := c.Run(); e != nil {
		t.Fatal(e)
	}
	os.RemoveAll(f.job.Request.Workspace)
	c, _ = f.command(nil)
	if e := c.Run(); e != nil {
		t.Fatal("receipt delivery depends on checkout", e)
	}
}
