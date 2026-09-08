package remotebuild

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/factory"
	"github.com/0xikarus/vmbox-service/internal/factory/builder"
	"github.com/0xikarus/vmbox-service/internal/factory/buildjob"
	"github.com/0xikarus/vmbox-service/internal/factory/execution"
	"github.com/0xikarus/vmbox-service/internal/factory/resultinbox"
	"github.com/0xikarus/vmbox-service/internal/factory/staging"
	"github.com/0xikarus/vmbox-service/internal/provider"
)

type control struct {
	box                         v1.LogicalBox
	task                        v1.ProcessTask
	found                       *v1.ProcessTask
	ensure, submit, connections int
	change                      bool
	fail                        error
	work                        factory.Work
}

func (c *control) EnsureBuilderBox(_ context.Context, _ string, w factory.Work, _, _ string) (v1.LogicalBox, error) {
	c.ensure++
	c.work = w
	return c.box, nil
}
func (c *control) FindBuilder(context.Context, string, string, string) (*v1.ProcessTask, error) {
	return c.found, c.fail
}
func (c *control) SubmitBuilder(context.Context, string, string, string) (v1.ProcessTask, error) {
	c.submit++
	if c.fail != nil {
		c.found = &c.task
	}
	return c.task, c.fail
}
func (c *control) Process(context.Context, string, string) (v1.ProcessTask, error) {
	return c.task, c.fail
}
func (c *control) Connection(context.Context, string, string) (v1.LogicalBoxConnection, error) {
	c.connections++
	endpoint := "one"
	if c.change && c.connections > 1 {
		endpoint = "two"
	}
	return v1.LogicalBoxConnection{LogicalBoxID: c.box.ID, Connection: provider.Connection{Transport: "openssh", Endpoint: endpoint}}, nil
}

type repos struct {
	sha     string
	calls   int
	changed bool
}

func (r *repos) Resolve(_ context.Context, _, id, sha string) (factory.Repository, string, error) {
	r.calls++
	if r.changed {
		sha = strings.Repeat("f", 40)
	}
	return factory.Repository{ID: id, FullName: "owner/repo"}, sha, nil
}
func (r *repos) RepositoryToken(_ context.Context, _, _ string, write bool) (string, time.Time, error) {
	if write {
		panic("publication grant requested")
	}
	return "source_token", time.Now().Add(time.Hour), nil
}

type inbox struct {
	body                   []byte
	issues                 int
	account, work, attempt string
	fail                   error
}

func (i *inbox) Issue(_ context.Context, a, w, id string) (string, error) {
	i.issues++
	i.account = a
	i.work = w
	i.attempt = id
	return strings.Repeat("A", 43), i.fail
}
func (i *inbox) Get(_ context.Context, a, w, id string) ([]byte, error) {
	if i.fail != nil {
		return nil, i.fail
	}
	if i.body == nil || a != i.account || w != i.work || id != i.attempt {
		return nil, resultinbox.ErrNotFound
	}
	return i.body, nil
}
func setup(t *testing.T) (*Runner, Input, *control, *inbox) {
	t.Helper()
	f := factory.Feature{ID: "api", Title: "API", Description: "Implement API", AcceptanceCriteria: []string{"works"}, Checks: []factory.Check{{Argv: []string{"go", "test", "./..."}, Cwd: ".", TimeoutSeconds: 60}}}
	p := factory.Plan{Revision: 1, InputRevision: 1, Markdown: "approved", BaseSHA: strings.Repeat("a", 40), Features: []factory.Feature{f}}
	f.IssueURL = "https://github.com/owner/repo/issues/2"
	w := factory.Work{ID: strings.Repeat("b", 32), Revision: 2, State: "build_queued", BaseSHA: p.BaseSHA, RepositoryName: "owner/repo", ApprovedPlanRevision: 1, Plans: []factory.Plan{p}, Features: []factory.Feature{f}, MasterIssueURL: "https://github.com/owner/repo/issues/1", BoxID: "planner", CreateWork: factory.CreateWork{RepositoryID: "123", Agent: "codex", Profile: "chosen"}}
	g, e := execution.New(w)
	if e != nil {
		t.Fatal(e)
	}
	n := g.Nodes[0]
	n.State = "building"
	h := sha256.Sum256([]byte(w.ID + "\x00" + n.Branch + "\x00build"))
	n.Attempt = &execution.StageAttempt{ID: hex.EncodeToString(h[:16]), Stage: "build", State: "queued", BoxID: "builder"}
	in := Input{AccountID: "account", Work: w, Node: n, PreparedSourceSHA: w.BaseSHA}
	c := &control{box: v1.LogicalBox{ID: "builder", Name: "factory-build-" + n.Attempt.ID, State: v1.LogicalBoxRunning}}
	c.task = v1.ProcessTask{ID: "task", LogicalBoxID: "builder", BoxName: c.box.Name, Agent: "shell", Prompt: command(in), State: "running"}
	i := &inbox{account: in.AccountID, work: w.ID, attempt: n.Attempt.ID}
	r := &Runner{AccountID: in.AccountID, Controller: c, Repositories: &repos{}, Inbox: i, CallbackURL: "https://factory.example/result", Stage: func(context.Context, staging.Input) error { return nil }}
	return r, in, c, i
}
func TestPendingLifecycleAndSeparateBox(t *testing.T) {
	for _, state := range []v1.LogicalBoxState{v1.LogicalBoxRunning, v1.LogicalBoxAttaching, v1.LogicalBoxHibernated, v1.LogicalBoxDetached} {
		t.Run(string(state), func(t *testing.T) {
			r, in, c, i := setup(t)
			c.box.State = state
			in.Node.Attempt.BoxID = ""
			r.Stage = func(context.Context, staging.Input) error { t.Fatal("premature staging"); return nil }
			s, e := r.Start(context.Background(), in)
			if e != nil || !s.Pending || s.BoxID != "builder" || s.State != string(state) || c.submit != 0 || i.issues != 0 {
				t.Fatalf("%+v %v", s, e)
			}
			if c.work.Profile != "chosen" {
				t.Fatal("profile lost")
			}
		})
	}
}
func TestAcceptedRecoveryPrecedesAllFreshWork(t *testing.T) {
	r, in, c, i := setup(t)
	c.found = &c.task
	c.box.State = v1.LogicalBoxHibernated
	r.Repositories = nil
	r.Stage = nil
	i.fail = resultinbox.ErrExpired
	s, e := r.Start(context.Background(), in)
	if e != nil || s.TaskID != "task" || c.ensure != 0 || c.connections != 0 || i.issues != 0 {
		t.Fatalf("%+v %v", s, e)
	}
	c.task.Prompt = "menu output"
	if _, e = r.Start(context.Background(), in); e == nil {
		t.Fatal("wrong wrapper recovered")
	}
}
func TestStageRetryAndLostSubmission(t *testing.T) {
	r, in, c, i := setup(t)
	var staged []byte
	r.Stage = func(_ context.Context, s staging.Input) error {
		if s.BaseSHA != in.PreparedSourceSHA || s.Repository != "owner/repo" || s.SourceToken != "source_token" {
			t.Fatal("wrong source")
		}
		var j buildjob.Job
		if json.Unmarshal(s.Job, &j) != nil || j.Request.Branch != in.Node.Branch || j.Request.ResultPath != root(in)+"/build.json" || j.ReceiptPath != root(in)+"/receipt.json" || !strings.Contains(j.Request.Prompt, "acceptanceCriteria") || !strings.Contains(j.Request.Prompt, "checks") || strings.Contains(j.Request.Prompt, s.SourceToken) {
			t.Fatal("wrong job")
		}
		if staged != nil && string(staged) != string(s.Job) {
			t.Fatal("retry changed job")
		}
		staged = s.Job
		return nil
	}
	// A failed stage can be retried with the same private manifest.
	real := r.Stage
	r.Stage = func(ctx context.Context, s staging.Input) error { _ = real(ctx, s); return errors.New("SSH") }
	if _, e := r.Start(context.Background(), in); e == nil || c.submit != 0 {
		t.Fatal("SSH error launched work")
	}
	r.Stage = real
	// Lose the submit acknowledgement, then recover without staging again.
	r.Stage = func(ctx context.Context, s staging.Input) error {
		e := real(ctx, s)
		c.fail = errors.New("lost acknowledgement")
		return e
	}
	if _, e := r.Start(context.Background(), in); e == nil {
		t.Fatal("expected lost acknowledgement")
	}
	c.fail = nil
	r.Stage = func(context.Context, staging.Input) error { t.Fatal("restaged accepted task"); return nil }
	s, e := r.Start(context.Background(), in)
	if e != nil || s.TaskID != "task" || c.submit != 1 || i.issues != 2 {
		t.Fatalf("%+v %v", s, e)
	}
}
func TestRejectIdentitySourceAndBounds(t *testing.T) {
	cases := map[string]func(*Runner, *Input, *control){
		"account":           func(r *Runner, in *Input, c *control) { in.AccountID = "other" },
		"stage":             func(r *Runner, in *Input, c *control) { in.Node.Attempt.Stage = "review" },
		"attempt":           func(r *Runner, in *Input, c *control) { in.Node.Attempt.ID = strings.Repeat("c", 32) },
		"approval":          func(r *Runner, in *Input, c *control) { in.Node.Feature.Title = "changed" },
		"revision":          func(r *Runner, in *Input, c *control) { in.Work.ApprovedPlanRevision = 2 },
		"source missing":    func(r *Runner, in *Input, c *control) { in.PreparedSourceSHA = "" },
		"source changed":    func(r *Runner, in *Input, c *control) { in.PreparedSourceSHA = strings.Repeat("c", 40) },
		"planner box":       func(r *Runner, in *Input, c *control) { in.Node.Attempt.BoxID = in.Work.BoxID },
		"box mismatch":      func(r *Runner, in *Input, c *control) { c.box.ID = "sibling" },
		"authorized source": func(r *Runner, in *Input, c *control) { r.Repositories.(*repos).changed = true },
		"assignment":        func(r *Runner, in *Input, c *control) { c.change = true },
		"missing accepted":  func(r *Runner, in *Input, c *control) { in.Node.Attempt.TaskID = "task" },
		"oversized prompt": func(r *Runner, in *Input, c *control) {
			in.Node.Feature.Description = strings.Repeat("x", 100001)
			in.Work.Features[0] = in.Node.Feature
			in.Work.Plans[0].Features[0].Description = in.Node.Feature.Description
		},
		"images count": func(r *Runner, in *Input, c *control) { in.Work.Assets = make([]factory.AssetRef, 9) },
		"image bound": func(r *Runner, in *Input, c *control) {
			in.Work.Assets = []factory.AssetRef{{ID: strings.Repeat("a", 32), SHA256: strings.Repeat("b", 64), Size: staging.MaxImageBytes + 1}}
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			r, in, c, _ := setup(t)
			mutate(r, &in, c)
			if _, e := r.Start(context.Background(), in); e == nil || c.submit != 0 {
				t.Fatal("accepted invalid input")
			}
		})
	}
}
func TestDependenciesExplicitlyNotReady(t *testing.T) {
	r, in, c, _ := setup(t)
	dep := in.Work.Plans[0].Features[0]
	dep.ID = "dependency"
	in.Work.Plans[0].Features = append(in.Work.Plans[0].Features, dep)
	dep.IssueURL = in.Work.Features[0].IssueURL
	in.Work.Features = append(in.Work.Features, dep)
	in.Work.Plans[0].Features[0].DependsOn = []string{dep.ID}
	in.Work.Features[0].DependsOn = []string{dep.ID}
	in.Node.Feature = in.Work.Features[0]
	if _, e := r.Start(context.Background(), in); !errors.Is(e, ErrNotReady) || c.ensure != 0 {
		t.Fatalf("dependency ignored: %v", e)
	}
}
func reportFor(r *Runner, in Input) buildjob.Report {
	req, _ := r.request(in)
	zero := 0
	sha := strings.Repeat("c", 40)
	return buildjob.Report{Version: 1, RequestHash: requestHash(in, req, r.CallbackURL), Build: builder.Result{ExitCode: &zero, CandidateSHA: sha, HEAD: sha, Branch: req.Branch, Clean: true}, Artifact: &buildjob.Artifact{Filename: buildjob.ArtifactFilename, BaseSHA: req.BaseSHA, Size: 12, SHA256: strings.Repeat("d", 64)}}
}
func deliver(i *inbox, in Input, b buildjob.Report) {
	doc, _ := json.Marshal(b)
	i.body, _ = json.Marshal(resultinbox.Result{Version: 1, AttemptID: in.Node.Attempt.ID, ExitCode: b.Build.ExitCode, Signal: b.Build.Signal, Truncated: b.Build.Truncated, Document: doc})
}
func TestObserveOneShotEvidenceAndUnknownWrapperExit(t *testing.T) {
	r, in, c, i := setup(t)
	in.Node.Attempt.TaskID = "task"
	c.task.State = "exited"
	c.task.Output = "Choose a menu option: done successfully" // output never counts as a receipt
	o, e := r.Observe(context.Background(), in)
	if e != nil || o.Finished || o.State != "result_missing" {
		t.Fatalf("%+v %v", o, e)
	}
	b := reportFor(r, in)
	deliver(i, in, b)
	o, e = r.Observe(context.Background(), in)
	if e != nil || !o.Finished || o.WrapperExitCode != nil || o.Process.ExitCode == nil || *o.Process.ExitCode != 0 || o.ArtifactPath != root(in)+"/candidate.bundle" {
		t.Fatalf("%+v %v", o, e)
	}
	seven := 7
	c.task.ExitCode = &seven
	o, e = r.Observe(context.Background(), in)
	if e != nil || *o.WrapperExitCode != 7 || *o.Process.ExitCode != 0 {
		t.Fatal("wrapper substituted")
	}
	b.ErrorCode = "build_rejected"
	b.Artifact = nil
	b.Build.CandidateSHA = ""
	b.Build.ExitCode = nil
	b.Build.Signal = 9
	deliver(i, in, b)
	o, e = r.Observe(context.Background(), in)
	if e != nil || !o.Finished || o.Process.ExitCode != nil || o.Process.Signal != 9 {
		t.Fatal("signal lost")
	}
	c.fail = errors.New("SSH unavailable")
	o, e = r.Observe(context.Background(), in)
	if e == nil || o.Finished {
		t.Fatal("transport manufactured completion")
	}
}
func TestObserveRejectsInvalidReports(t *testing.T) {
	cases := map[string]func(*buildjob.Report){
		"hash":         func(b *buildjob.Report) { b.RequestHash = strings.Repeat("e", 64) },
		"candidate":    func(b *buildjob.Report) { b.Build.CandidateSHA = strings.Repeat("e", 40) },
		"branch":       func(b *buildjob.Report) { b.Build.Branch = "factory/other" },
		"base":         func(b *buildjob.Report) { b.Artifact.BaseSHA = strings.Repeat("e", 40) },
		"path":         func(b *buildjob.Report) { b.Artifact.Filename = "../candidate.bundle" },
		"size":         func(b *buildjob.Report) { b.Artifact.Size = buildjob.MaxArtifactBytes + 1 },
		"digest":       func(b *buildjob.Report) { b.Artifact.SHA256 = "bad" },
		"unknown exit": func(b *buildjob.Report) { b.Build.ExitCode = nil },
		"truncated":    func(b *buildjob.Report) { b.Build.Truncated = true },
		"error":        func(b *buildjob.Report) { b.ErrorCode = "success" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			r, in, c, i := setup(t)
			in.Node.Attempt.TaskID = "task"
			c.task.State = "exited"
			b := reportFor(r, in)
			mutate(&b)
			deliver(i, in, b)
			o, e := r.Observe(context.Background(), in)
			if e == nil || o.Finished {
				t.Fatal("invalid evidence accepted")
			}
		})
	}
	for _, mode := range []string{"foreign account", "stale attempt", "duplicate", "unknown field", "envelope mismatch", "process", "oversized"} {
		t.Run(mode, func(t *testing.T) {
			r, in, c, i := setup(t)
			in.Node.Attempt.TaskID = "task"
			c.task.State = "exited"
			deliver(i, in, reportFor(r, in))
			switch mode {
			case "foreign account":
				i.account = "other"
			case "stale attempt":
				i.body = []byte(strings.ReplaceAll(string(i.body), in.Node.Attempt.ID, strings.Repeat("f", 32)))
			case "duplicate":
				i.body = []byte(strings.Replace(string(i.body), `"requestHash":`, `"version":1,"requestHash":`, 1))
			case "unknown field":
				i.body = []byte(strings.Replace(string(i.body), `"requestHash":`, `"surprise":1,"requestHash":`, 1))
			case "envelope mismatch":
				i.body = []byte(strings.Replace(string(i.body), `"exitCode":0`, `"exitCode":7`, 1))
			case "process":
				c.task.LogicalBoxID = "sibling"
			case "oversized":
				i.body = make([]byte, resultinbox.MaxBodyBytes+1)
			}
			o, e := r.Observe(context.Background(), in)
			if o.Finished || (mode != "foreign account" && e == nil) {
				t.Fatalf("accepted %s", mode)
			}
		})
	}
}

type assets struct {
	factory.AssetBackend
	file string
	ref  factory.AssetRef
}

func (a assets) Open(_ context.Context, account, id string) (*os.File, factory.AssetRef, error) {
	if account != "account" || id != a.ref.ID {
		return nil, factory.AssetRef{}, errors.New("scope")
	}
	f, e := os.Open(a.file)
	return f, a.ref, e
}
func TestActualImageBytesAndManifest(t *testing.T) {
	r, in, _, _ := setup(t)
	data := []byte("actual fixture image bytes")
	h := sha256.Sum256(data)
	ref := factory.AssetRef{ID: strings.Repeat("e", 32), Size: int64(len(data)), SHA256: hex.EncodeToString(h[:]), MediaType: "image/png"}
	file := t.TempDir() + "/image"
	if e := os.WriteFile(file, data, 0600); e != nil {
		t.Fatal(e)
	}
	r.Assets = assets{file: file, ref: ref}
	in.Work.Assets = []factory.AssetRef{ref}
	r.Stage = func(_ context.Context, s staging.Input) error {
		if len(s.Images) != 1 || string(s.Images[0].Data) != string(data) {
			t.Fatal("image bytes lost")
		}
		return nil
	}
	if _, e := r.Start(context.Background(), in); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(file, []byte("changed"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := r.Start(context.Background(), in); e == nil {
		t.Fatal("corrupt asset accepted")
	}
}

var _ Controller = (*factory.ControllerClient)(nil)
