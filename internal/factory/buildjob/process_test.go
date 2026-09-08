package buildjob

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/0xikarus/vmbox-service/internal/factory"
	"github.com/0xikarus/vmbox-service/internal/factory/builder"
	"github.com/0xikarus/vmbox-service/internal/factory/verification"
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
	// Give the baseline real ancestry, then stage independent depth-1 clones.
	origin := j.Request.Workspace
	old := j.Request.BaseSHA
	git(t, origin, "commit", "--allow-empty", "-qm", "staged baseline")
	j.Request.BaseSHA = git(t, origin, "rev-parse", "HEAD")
	worker, imported := filepath.Join(t.TempDir(), "worker"), filepath.Join(t.TempDir(), "verifier")
	for _, clone := range []string{worker, imported} {
		git(t, origin, "clone", "--depth=1", "file://"+origin, clone)
		if git(t, clone, "rev-list", "--count", "HEAD") != "1" || exec.Command("git", "-C", clone, "cat-file", "-e", old).Run() == nil {
			t.Fatal("staging did not exclude baseline ancestry")
		}
	}
	j.Request.Workspace = worker
	git(t, worker, "config", "user.name", "Fixture")
	git(t, worker, "config", "user.email", "fixture@example.invalid")
	// This unrelated ref must not be advertised or included in the export.
	unrelated := git(t, j.Request.Workspace, "commit-tree", git(t, j.Request.Workspace, "rev-parse", "HEAD^{tree}"), "-m", "unrelated root")
	git(t, j.Request.Workspace, "update-ref", "refs/heads/unrelated", unrelated)
	// Exercise the actual adapter and job, with an executable CLI fixture. This
	// includes shallow source inspection, real commit, export and delivery.
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
	if report.Artifact.BaseSHA != j.Request.BaseSHA || report.Artifact.Filename != ArtifactFilename || report.Artifact.Size != int64(len(b)) || report.Artifact.SHA256 != hex.EncodeToString(h[:]) {
		t.Fatal("artifact mismatch")
	}
	heads := git(t, imported, "bundle", "list-heads", path)
	if heads != head+" refs/heads/candidate" {
		t.Fatalf("unexpected bundle refs: %s", heads)
	}
	if err := ImportBundle(context.Background(), imported, bytes.NewReader(b), *report.Artifact, j.Request.BaseSHA, head); err != nil {
		t.Fatal(err)
	}
	if git(t, imported, "rev-parse", "refs/heads/candidate") != head {
		t.Fatal("import mismatch")
	}
	if exec.Command("git", "-C", imported, "cat-file", "-e", unrelated).Run() == nil {
		t.Fatal("unrelated object exported")
	}
	if git(t, imported, "show", "candidate:file") != "candidate" {
		t.Fatal("wrong content")
	}
	if exec.Command("git", "-C", imported, "cat-file", "-e", old).Run() == nil {
		t.Fatal("pre-baseline ancestry exported")
	}
	git(t, imported, "checkout", "--detach", head)
	checked, err := verification.Run(context.Background(), verification.Request{Workspace: imported, ExpectedSHA: head, OutputDir: filepath.Join(t.TempDir(), "evidence"), Checks: []factory.Check{{Argv: []string{"/bin/sh", "-c", "test \"$(cat file)\" = candidate"}, Cwd: ".", TimeoutSeconds: 5}}})
	if err != nil || !checked.AllPassed || len(checked.Checks) != 1 || checked.Checks[0].ExitCode == nil || *checked.Checks[0].ExitCode != 0 {
		t.Fatal("independent imported-source check failed", checked, err)
	}
	git(t, imported, "checkout", "--detach", j.Request.BaseSHA)
	for _, tc := range []struct {
		name            string
		data            []byte
		artifact        Artifact
		base, candidate string
	}{
		{"wrong trusted baseline", b, *report.Artifact, old, head},
		{"wrong candidate", b, *report.Artifact, j.Request.BaseSHA, unrelated},
		{"truncated", b[:len(b)-1], *report.Artifact, j.Request.BaseSHA, head},
		{"digest", append([]byte("!"), b[1:]...), *report.Artifact, j.Request.BaseSHA, head},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if ImportBundle(context.Background(), imported, bytes.NewReader(tc.data), tc.artifact, tc.base, tc.candidate) == nil {
				t.Fatal("invalid import accepted")
			}
		})
	}
	empty := t.TempDir()
	git(t, empty, "init", "-q")
	if ImportBundle(context.Background(), empty, bytes.NewReader(b), *report.Artifact, j.Request.BaseSHA, head) == nil {
		t.Fatal("missing baseline accepted")
	}
	for _, header := range []string{
		"# v2 git bundle\n" + head + " refs/heads/candidate",
		"# v2 git bundle\n-" + old + " wrong baseline\n" + head + " refs/heads/candidate",
		"# v2 git bundle\n-" + j.Request.BaseSHA + " baseline\n" + head + " refs/heads/candidate\n" + head + " refs/heads/extra",
	} {
		bad := append([]byte(header), b[bytes.Index(b, []byte("\n\n")):]...)
		a := *report.Artifact
		h := sha256.Sum256(bad)
		a.Size, a.SHA256 = int64(len(bad)), hex.EncodeToString(h[:])
		if ImportBundle(context.Background(), imported, bytes.NewReader(bad), a, j.Request.BaseSHA, head) == nil {
			t.Fatal("invalid prerequisite/ref header accepted despite matching digest")
		}
	}
	// Even with the baseline object present, a repository at another HEAD is
	// not the exact independently staged baseline required by the importer.
	git(t, imported, "checkout", "--detach", head)
	if ImportBundle(context.Background(), imported, bytes.NewReader(b), *report.Artifact, j.Request.BaseSHA, head) == nil {
		t.Fatal("different HEAD accepted")
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

func TestGitRunCleansDescendants(t *testing.T) {
	for _, cancel := range []bool{false, true} {
		t.Run(fmt.Sprint(cancel), func(t *testing.T) {
			bin := t.TempDir()
			marker := filepath.Join(bin, "escaped")
			// The descendant closes inherited pipes so pipe draining cannot hide a leak.
			body := "#!/bin/sh\n(sleep 0.3; touch '" + marker + "') </dev/null >/dev/null 2>&1 &\n"
			if cancel {
				body += "sleep 30\n"
			}
			if err := os.WriteFile(filepath.Join(bin, "git"), []byte(body), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
			ctx, stop := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer stop()
			err := gitRun(ctx, nil, "version")
			if (err != nil) != cancel {
				t.Fatalf("unexpected Git exit: %v", err)
			}
			time.Sleep(500 * time.Millisecond)
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatal("Git descendant escaped cleanup")
			}
		})
	}
}

func TestExportRejectsMissingCandidateAncestry(t *testing.T) {
	j := processJob(t, "https://example.invalid", commitScript)
	git(t, j.Request.Workspace, "commit", "--allow-empty", "-qm", "intermediate")
	missing := git(t, j.Request.Workspace, "rev-parse", "HEAD")
	git(t, j.Request.Workspace, "commit", "--allow-empty", "-qm", "candidate")
	head := git(t, j.Request.Workspace, "rev-parse", "HEAD")
	if err := os.Remove(filepath.Join(j.Request.Workspace, ".git", "objects", missing[:2], missing[2:])); err != nil {
		t.Fatal(err)
	}
	dir, _, err := openReceiptDir(j.ReceiptPath)
	if err != nil {
		t.Fatal(err)
	}
	defer dir.Close()
	zero := 0
	result := builder.Result{ExitCode: &zero, CandidateSHA: head, HEAD: head, Branch: j.Request.Branch, Clean: true}
	if a, err := exportBundle(context.Background(), dir, j.Request, result, MaxArtifactBytes); err == nil || a != nil {
		t.Fatal("missing candidate ancestry silently exported")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(j.ReceiptPath), ArtifactFilename)); !os.IsNotExist(err) {
		t.Fatal("failed export published artifact")
	}
}
