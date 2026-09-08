package buildjob

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/0xikarus/vmbox-service/internal/factory/builder"
)

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	c := exec.Command("git", append([]string{"-C", dir}, args...)...)
	b, e := c.CombinedOutput()
	if e != nil {
		t.Fatalf("git %v: %v %s", args, e, b)
	}
	return strings.TrimSpace(string(b))
}

// These executable fixtures exercise builder.Run and real Git, not live Codex.
func processJob(t *testing.T, endpoint, body string) Job {
	t.Helper()
	j := testJob(t, endpoint)
	j.Request.Workspace = t.TempDir()
	git(t, j.Request.Workspace, "init", "-q")
	git(t, j.Request.Workspace, "config", "user.name", "Fixture")
	git(t, j.Request.Workspace, "config", "user.email", "fixture@example.invalid")
	os.WriteFile(filepath.Join(j.Request.Workspace, "file"), []byte("base"), 0600)
	git(t, j.Request.Workspace, "add", ".")
	git(t, j.Request.Workspace, "commit", "-qm", "base")
	j.Request.BaseSHA = git(t, j.Request.Workspace, "rev-parse", "HEAD")
	j.Request.Branch = "factory/fixture"
	bin := t.TempDir()
	if e := os.WriteFile(filepath.Join(bin, "codex"), []byte("#!/bin/sh\nset -e\ncat >/dev/null\n"+body), 0700); e != nil {
		t.Fatal(e)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	return j
}

const commitScript = "printf candidate > file\ngit add file\ngit commit -qm candidate\n"

func TestRealBundleAndReceiptRedelivery(t *testing.T) {
	var reports [][]byte
	refuse := true
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		reports = append(reports, b)
		if refuse {
			w.WriteHeader(409)
		} else {
			w.WriteHeader(204)
		}
	}))
	defer server.Close()
	j := processJob(t, server.URL, commitScript)
	// This unrelated ref must not be advertised or included in the export.
	unrelated := git(t, j.Request.Workspace, "commit-tree", git(t, j.Request.Workspace, "rev-parse", "HEAD^{tree}"), "-m", "unrelated root")
	git(t, j.Request.Workspace, "update-ref", "refs/heads/unrelated", unrelated)
	if e := execute(context.Background(), input(j), builder.Run, server.Client()); e == nil {
		t.Fatal("expected delivery failure")
	}
	head := git(t, j.Request.Workspace, "rev-parse", "HEAD")
	refuse = false
	if e := execute(context.Background(), input(j), func(context.Context, builder.Request) (builder.Result, error) {
		t.Fatal("agent rerun")
		return builder.Result{}, nil
	}, server.Client()); e != nil {
		t.Fatal(e)
	}
	if len(reports) != 2 || string(reports[0]) != string(reports[1]) {
		t.Fatal("receipt changed")
	}
	var env Envelope
	var report Report
	if json.Unmarshal(reports[0], &env) != nil || json.Unmarshal(env.Document, &report) != nil {
		t.Fatal("bad report")
	}
	if report.Version != 1 || report.ErrorCode != "" || report.Build.CandidateSHA != head || report.Artifact == nil {
		t.Fatalf("%+v", report)
	}
	path := filepath.Join(filepath.Dir(j.ReceiptPath), ArtifactFilename)
	b, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	h := sha256.Sum256(b)
	if report.Artifact.Filename != ArtifactFilename || report.Artifact.Size != int64(len(b)) || report.Artifact.SHA256 != hex.EncodeToString(h[:]) {
		t.Fatal("artifact mismatch")
	}
	imported := t.TempDir()
	git(t, imported, "init", "-q")
	heads := git(t, imported, "bundle", "list-heads", path)
	if heads != head+" refs/heads/candidate" {
		t.Fatalf("unexpected bundle refs: %s", heads)
	}
	git(t, imported, "fetch", path, "refs/heads/candidate:refs/heads/imported")
	if git(t, imported, "rev-parse", "refs/heads/imported") != head {
		t.Fatal("import mismatch")
	}
	if exec.Command("git", "-C", imported, "cat-file", "-e", unrelated).Run() == nil {
		t.Fatal("unrelated object exported")
	}
	if git(t, imported, "show", "imported:file") != "candidate" {
		t.Fatal("wrong content")
	}
	// A tiny explicit cap refuses the entire export; no published partial artifact.
	d := t.TempDir()
	os.Chmod(d, 0700)
	dir, _, e := openReceiptDir(filepath.Join(d, "receipt"))
	if e != nil {
		t.Fatal(e)
	}
	defer dir.Close()
	if a, e := exportBundle(context.Background(), dir, j.Request, report.Build, 10); e == nil || a != nil {
		t.Fatal("partial export accepted")
	}
	if _, e := os.Stat(filepath.Join(d, ArtifactFilename)); !os.IsNotExist(e) {
		t.Fatal("partial artifact published")
	}
}

func TestRealProcessEvidence(t *testing.T) {
	for _, tc := range []struct {
		name, body        string
		code              int
		truncated, cancel bool
	}{
		{"semantic", "echo secret-bearing-prose", 0, false, false},
		{"nonzero", "echo secret-bearing-diagnostic >&2\nexit 7", 7, false, false},
		{"oversized", "head -c 1100000 /dev/zero", 0, true, false},
		{"cancelled", "exec sleep 20", 0, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var env Envelope
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				b, _ := io.ReadAll(r.Body)
				if strings.Contains(string(b), "secret-bearing") {
					t.Error("diagnostics leaked")
				}
				if e := json.Unmarshal(b, &env); e != nil {
					t.Error(e)
				}
				w.WriteHeader(204)
			}))
			defer server.Close()
			j := processJob(t, server.URL, tc.body)
			ctx := context.Background()
			if tc.cancel {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, 500*time.Millisecond)
				defer cancel()
			}
			if e := execute(ctx, input(j), builder.Run, server.Client()); e != nil {
				t.Fatal(e)
			}
			if tc.cancel {
				if env.Signal == 0 {
					t.Fatal("missing actual signal")
				}
			} else if env.ExitCode == nil || *env.ExitCode != tc.code {
				t.Fatal("wrong exit")
			}
			var report Report
			if e := json.Unmarshal(env.Document, &report); e != nil {
				t.Fatal(e)
			}
			if report.ErrorCode != "build_rejected" || report.Artifact != nil || env.Truncated != tc.truncated {
				t.Fatalf("%+v %+v", env, report)
			}
		})
	}
}
func TestStrictBoundedJobs(t *testing.T) {
	for _, b := range []string{
		"{}", "null", "[]", `{"version":1,"version":1}`, `{"version":1,"Version":1}`,
		`{"unknown":true}`, `{} {}`, strings.Repeat(" ", 200001),
	} {
		if e := execute(context.Background(), strings.NewReader(b), func(context.Context, builder.Request) (builder.Result, error) {
			t.Fatal("invalid job ran")
			return builder.Result{}, nil
		}, nil); e == nil {
			t.Fatalf("accepted %q", b[:min(len(b), 50)])
		}
	}
}
