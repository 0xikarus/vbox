// Package remotebuild orchestrates trusted, persisted build attempts. It does
// not accept browser requests and does not transfer or verify candidate bundles.
package remotebuild

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"reflect"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/factory"
	"github.com/0xikarus/vmbox-service/internal/factory/builder"
	"github.com/0xikarus/vmbox-service/internal/factory/buildjob"
	"github.com/0xikarus/vmbox-service/internal/factory/execution"
	"github.com/0xikarus/vmbox-service/internal/factory/remoteplan"
	"github.com/0xikarus/vmbox-service/internal/factory/resultinbox"
	"github.com/0xikarus/vmbox-service/internal/factory/staging"
)

var ErrNotReady = errors.New("remote build not ready: prepared dependency source proof unavailable")

type Controller interface {
	EnsureBuilderBox(context.Context, string, factory.Work, string, string) (v1.LogicalBox, error)
	FindBuilder(context.Context, string, string, string) (*v1.ProcessTask, error)
	SubmitBuilder(context.Context, string, string, string) (v1.ProcessTask, error)
	Connection(context.Context, string, string) (v1.LogicalBoxConnection, error)
	Process(context.Context, string, string) (v1.ProcessTask, error)
}

// Input must be loaded under the account's execution lease. Node is the current
// persisted graph node, not a caller-selected attempt. PreparedSourceSHA is an
// explicit trusted source-preparation output; it is never defaulted to BaseSHA.
type Input struct {
	AccountID         string
	Work              factory.Work
	Node              execution.Node
	PreparedSourceSHA string
}

type Runner struct {
	AccountID    string
	Controller   Controller
	Repositories remoteplan.Repositories
	Inbox        remoteplan.Inbox
	Assets       factory.AssetBackend
	// Use BuilderStage with the production Stager; functions permit focused fixtures.
	Stage       func(context.Context, staging.Input) error
	CallbackURL string
}

func BuilderStage(s staging.Stager) func(context.Context, staging.Input) error {
	s.Role = "builder"
	return s.Stage
}

type Observation struct {
	State string
	// Finished describes terminal build evidence only, never verified completion.
	Finished        bool
	WrapperExitCode *int
	WrapperSignal   int
	Report          *buildjob.Report
	Process         execution.Process
	// Artifact remains on the builder box at this fixed path. No transfer occurred.
	ArtifactPath string
}

func digest(s string, n int) bool {
	if len(s) != n || s != strings.ToLower(s) {
		return false
	}
	_, e := hex.DecodeString(s)
	return e == nil
}
func opaqueID(s string) bool {
	return s != "" && len(s) <= 512 && utf8.ValidString(s) && strings.IndexFunc(s, func(r rune) bool { return unicode.IsControl(r) || unicode.IsSpace(r) }) < 0
}
func root(in Input) string { return "/data/workspace/.vmbox-factory/attempts/" + in.Node.Attempt.ID }
func command(in Input) string {
	return "exec /data/workspace/.vmbox-factory/bin/vmbox-builder < " + root(in) + "/job.json"
}

func (r *Runner) request(in Input) (builder.Request, error) {
	bad := func() (builder.Request, error) {
		return builder.Request{}, fmt.Errorf("invalid or stale build identity")
	}
	a := in.Node.Attempt
	if !opaqueID(r.AccountID) || in.AccountID != r.AccountID || a == nil || a.Stage != "build" || !digest(a.ID, 32) || in.Node.State != "building" || (a.State != "queued" && a.State != "submitted") || (a.TaskID != "" && (!opaqueID(a.TaskID) || a.BoxID == "")) || (a.BoxID != "" && (!opaqueID(a.BoxID) || a.BoxID == in.Work.BoxID)) {
		return bad()
	}
	if (in.Work.Agent != "codex" && in.Work.Agent != "claude") || !opaqueID(in.Work.Profile) || !opaqueID(in.Work.RepositoryID) || !opaqueID(in.Node.Feature.ID) || !digest(in.PreparedSourceSHA, 40) {
		return bad()
	}
	g, err := execution.New(in.Work)
	if err != nil {
		return bad()
	}
	found := false
	for _, n := range g.Nodes {
		if n.Feature.ID == in.Node.Feature.ID && reflect.DeepEqual(n.Feature, in.Node.Feature) && n.Branch == in.Node.Branch {
			found = true
		}
	}
	h := sha256.Sum256([]byte(g.WorkID + "\x00" + in.Node.Branch + "\x00build"))
	if !found || a.ID != hex.EncodeToString(h[:16]) {
		return bad()
	}
	if len(in.Node.Feature.DependsOn) != 0 {
		return builder.Request{}, ErrNotReady
	}
	// Without dependencies, the approved baseline is the only prepared source.
	if in.PreparedSourceSHA != in.Work.BaseSHA {
		return bad()
	}
	prompt, err := json.Marshal(struct {
		Account, Work, Attempt, Repository, Profile string
		PlanRevision                                int
		Feature                                     factory.Feature
		Assets                                      []factory.AssetRef
	}{in.AccountID, in.Work.ID, a.ID, in.Work.RepositoryName, in.Work.Profile, in.Work.ApprovedPlanRevision, in.Node.Feature, in.Work.Assets})
	if err != nil || len(prompt) > 100000 || len(in.Work.Assets) > 8 || (in.Work.Agent == "claude" && len(in.Work.Assets) > 0) {
		return bad()
	}
	req := builder.Request{Agent: in.Work.Agent, Workspace: root(in) + "/repo", Prompt: string(prompt), Branch: in.Node.Branch, BaseSHA: in.PreparedSourceSHA, ResultPath: root(in) + "/build.json"}
	seen := map[string]bool{}
	var total int64
	for _, im := range in.Work.Assets {
		if !digest(im.ID, 32) || seen[im.ID] || im.Size <= 0 || im.Size > staging.MaxImageBytes || !digest(im.SHA256, 64) {
			return bad()
		}
		seen[im.ID] = true
		total += im.Size
		req.Images = append(req.Images, root(in)+"/images/"+im.ID)
	}
	if total > staging.MaxImagesBytes {
		return bad()
	}
	return req, nil
}

func validTask(in Input, t v1.ProcessTask, box string) bool {
	return opaqueID(t.ID) && t.LogicalBoxID == box && t.BoxName == "factory-build-"+in.Node.Attempt.ID && t.Agent == "shell" && t.Prompt == command(in) && (in.Node.Attempt.TaskID == "" || t.ID == in.Node.Attempt.TaskID)
}

func (r *Runner) Start(ctx context.Context, in Input) (factory.Submission, error) {
	req, err := r.request(in)
	if err != nil {
		return factory.Submission{}, err
	}
	if r.Controller == nil {
		return factory.Submission{}, fmt.Errorf("controller unavailable")
	}
	a := in.Node.Attempt
	// Recover even when staging, grants, or delivery configuration is unavailable.
	if a.BoxID != "" {
		t, e := r.Controller.FindBuilder(ctx, in.AccountID, a.BoxID, a.ID)
		if e != nil {
			return factory.Submission{}, e
		}
		if t != nil {
			if !validTask(in, *t, a.BoxID) {
				return factory.Submission{}, fmt.Errorf("accepted task identity changed")
			}
			return factory.Submission{BoxID: a.BoxID, BoxName: t.BoxName, TaskID: t.ID, State: t.State}, nil
		}
		if a.TaskID != "" {
			return factory.Submission{}, fmt.Errorf("accepted task missing; reconciliation required")
		}
	}
	if r.Repositories == nil || r.Inbox == nil || r.Stage == nil {
		return factory.Submission{}, fmt.Errorf("build backend incomplete")
	}
	u, e := url.Parse(r.CallbackURL)
	if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return factory.Submission{}, fmt.Errorf("invalid callback URL")
	}
	b, err := r.Controller.EnsureBuilderBox(ctx, in.AccountID, in.Work, a.ID, a.BoxID)
	if err != nil {
		return factory.Submission{}, err
	}
	if !opaqueID(b.ID) || b.ID == in.Work.BoxID || b.Name != "factory-build-"+a.ID || (a.BoxID != "" && b.ID != a.BoxID) {
		return factory.Submission{}, fmt.Errorf("builder box identity changed")
	}
	pending := factory.Submission{BoxID: b.ID, BoxName: b.Name, State: string(b.State), Pending: true}
	// Persist box identity before the next call can stage or submit.
	if a.BoxID == "" || b.State != v1.LogicalBoxRunning {
		return pending, nil
	}
	repo, sha, err := r.Repositories.Resolve(ctx, in.AccountID, in.Work.RepositoryID, in.PreparedSourceSHA)
	if err != nil {
		return factory.Submission{}, err
	}
	if repo.ID != in.Work.RepositoryID || repo.FullName != in.Work.RepositoryName || sha != in.PreparedSourceSHA {
		return factory.Submission{}, fmt.Errorf("authorized source changed")
	}
	images := []staging.Image{}
	for _, expected := range in.Work.Assets {
		if r.Assets == nil {
			return factory.Submission{}, fmt.Errorf("private assets unavailable")
		}
		f, actual, e := r.Assets.Open(ctx, in.AccountID, expected.ID)
		if e != nil {
			return factory.Submission{}, e
		}
		data, e := io.ReadAll(io.LimitReader(f, staging.MaxImageBytes+1))
		closeErr := f.Close()
		h := sha256.Sum256(data)
		if e != nil || closeErr != nil || actual != expected || int64(len(data)) != expected.Size || hex.EncodeToString(h[:]) != expected.SHA256 {
			return factory.Submission{}, fmt.Errorf("private asset manifest changed")
		}
		images = append(images, staging.Image{ID: expected.ID, Data: data})
	}
	capability, err := r.Inbox.Issue(ctx, in.AccountID, in.Work.ID, a.ID)
	if err != nil {
		return factory.Submission{}, err
	}
	job, err := json.Marshal(buildjob.Job{Version: 1, AttemptID: a.ID, Request: req, ReceiptPath: root(in) + "/receipt.json", DeliveryURL: r.CallbackURL, DeliveryToken: capability})
	if err != nil || len(job) > staging.MaxJobBytes {
		return factory.Submission{}, fmt.Errorf("build job exceeds bounds")
	}
	token, expires, err := r.Repositories.RepositoryToken(ctx, in.AccountID, in.Work.RepositoryID, false)
	if err != nil {
		return factory.Submission{}, err
	}
	if time.Until(expires) < time.Minute {
		return factory.Submission{}, fmt.Errorf("source grant expires too soon")
	}
	conn, err := r.Controller.Connection(ctx, in.AccountID, b.ID)
	if err != nil {
		return factory.Submission{}, err
	}
	if conn.LogicalBoxID != b.ID {
		return factory.Submission{}, fmt.Errorf("connection identity changed")
	}
	if err = r.Stage(ctx, staging.Input{Connection: conn.Connection, AttemptID: a.ID, Repository: repo.FullName, BaseSHA: sha, SourceToken: token, Job: job, Images: images}); err != nil {
		return factory.Submission{}, err
	}
	current, err := r.Controller.Connection(ctx, in.AccountID, b.ID)
	if err != nil {
		return factory.Submission{}, err
	}
	if current.LogicalBoxID != b.ID || !reflect.DeepEqual(current.Connection, conn.Connection) {
		return factory.Submission{}, fmt.Errorf("builder assignment changed during staging")
	}
	task, err := r.Controller.SubmitBuilder(ctx, in.AccountID, b.ID, a.ID)
	if err != nil {
		return factory.Submission{}, err
	}
	if !validTask(in, task, b.ID) {
		return factory.Submission{}, fmt.Errorf("submitted task identity changed")
	}
	return factory.Submission{BoxID: b.ID, BoxName: b.Name, TaskID: task.ID, State: task.State}, nil
}

func requestHash(in Input, req builder.Request, callback string) string {
	b, _ := json.Marshal(struct {
		Attempt     string
		Request     builder.Request
		DeliveryURL string
	}{in.Node.Attempt.ID, req, callback})
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func (r *Runner) Observe(ctx context.Context, in Input) (Observation, error) {
	req, err := r.request(in)
	if err != nil {
		return Observation{}, err
	}
	a := in.Node.Attempt
	if r.Controller == nil || r.Inbox == nil || a.BoxID == "" || a.TaskID == "" {
		return Observation{}, fmt.Errorf("observation binding missing")
	}
	t, err := r.Controller.Process(ctx, in.AccountID, a.TaskID)
	if err != nil {
		return Observation{}, err
	}
	if !validTask(in, t, a.BoxID) {
		return Observation{}, fmt.Errorf("build process identity changed")
	}
	o := Observation{State: t.State, WrapperExitCode: t.ExitCode, WrapperSignal: t.Signal}
	if t.State != "exited" {
		return o, nil
	}
	body, err := r.Inbox.Get(ctx, in.AccountID, in.Work.ID, a.ID)
	if errors.Is(err, resultinbox.ErrNotFound) {
		o.State = "result_missing"
		return o, nil
	}
	if err != nil {
		return o, err
	}
	env, err := resultinbox.Decode(body)
	if err != nil || env.AttemptID != a.ID {
		return o, fmt.Errorf("invalid scoped build envelope")
	}
	report, err := decodeReport(env.Document)
	if err != nil || report.Version != 1 || report.RequestHash != requestHash(in, req, r.CallbackURL) {
		return o, fmt.Errorf("build request binding changed")
	}
	b := report.Build
	if !reflect.DeepEqual(b.ExitCode, env.ExitCode) || b.Signal != env.Signal || b.Truncated != env.Truncated || (b.Branch != "" && b.Branch != req.Branch) || (b.HEAD != "" && !digest(b.HEAD, 40)) {
		return o, fmt.Errorf("inconsistent build process evidence")
	}
	switch report.ErrorCode {
	case "":
		ar := report.Artifact
		if ar == nil || ar.Filename != buildjob.ArtifactFilename || ar.BaseSHA != req.BaseSHA || !digest(ar.SHA256, 64) || ar.Size <= 0 || ar.Size > buildjob.MaxArtifactBytes || !digest(b.CandidateSHA, 40) || b.CandidateSHA != b.HEAD || b.CandidateSHA == req.BaseSHA || b.Branch != req.Branch || !b.Clean || b.ExitCode == nil || *b.ExitCode != 0 || b.Signal != 0 || b.Truncated {
			return o, fmt.Errorf("invalid candidate artifact binding")
		}
		o.ArtifactPath = root(in) + "/" + buildjob.ArtifactFilename
	case "build_rejected", "artifact_export_failed":
		if report.Artifact != nil || (report.ErrorCode == "build_rejected" && b.CandidateSHA != "") || (b.CandidateSHA != "" && (!digest(b.CandidateSHA, 40) || b.CandidateSHA != b.HEAD || b.Branch != req.Branch)) {
			return o, fmt.Errorf("invalid rejected build evidence")
		}
	default:
		return o, fmt.Errorf("unknown build report error")
	}
	o.Finished = true
	o.Report = &report
	o.Process = execution.Process{AttemptID: a.ID, BoxID: a.BoxID, TaskID: a.TaskID, ExitCode: env.ExitCode, Signal: env.Signal}
	return o, nil
}
