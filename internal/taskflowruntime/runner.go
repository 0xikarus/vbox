// Package taskflowruntime runs repository-independent tasks on controller boxes.
package taskflowruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/factory"
	"github.com/0xikarus/vmbox-service/internal/factory/resultinbox"
	"github.com/0xikarus/vmbox-service/internal/taskflow"
	"github.com/0xikarus/vmbox-service/internal/transport"
)

type Controller interface {
	EnsureTaskBox(context.Context, string, string, string, string, string, string) (v1.LogicalBox, error)
	SubmitTaskRunner(context.Context, string, string, string) (v1.ProcessTask, error)
	FindTaskRunner(context.Context, string, string, string) (*v1.ProcessTask, error)
	Connection(context.Context, string, string) (v1.LogicalBoxConnection, error)
	Process(context.Context, string, string) (v1.ProcessTask, error)
}
type Inbox interface {
	Issue(context.Context, string, string, string) (string, error)
	Get(context.Context, string, string, string) ([]byte, error)
}

// Config is trusted operator configuration. CallbackURL must identify a dedicated
// resultinbox.Handler mount over TLS, using the same Inbox passed here.
type Config struct {
	Controller  Controller
	Inbox       Inbox
	Assets      factory.AssetBackend
	SSH         transport.SSH
	BinaryPath  string
	CallbackURL string
	// Stage may replace SSH staging for tests; nil uses Stager with SSH/BinaryPath.
	Stage func(context.Context, Input) error
}
type Runner struct{ config Config }

var _ taskflow.Runner = (*Runner)(nil)
var _ Controller = (*factory.ControllerClient)(nil)
var _ Inbox = (*resultinbox.Inbox)(nil)

func New(c Config) (*Runner, error) {
	u, e := url.Parse(c.CallbackURL)
	if c.Controller == nil || c.Inbox == nil || e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("task runtime requires controller, inbox and HTTPS callback")
	}
	if c.Stage == nil {
		if c.BinaryPath == "" {
			return nil, fmt.Errorf("task runner binary required")
		}
		c.Stage = (Stager{SSH: c.SSH, BinaryPath: c.BinaryPath}).Stage
	}
	return &Runner{config: c}, nil
}

// ImageCapabilities reports implemented adapters, not whether login or box
// provisioning is ready. Claude structured text is supported; images are not.
func ImageCapabilities() map[string]bool { return map[string]bool{"codex": true, "claude": false} }

func requestFor(in taskflow.Input) (Request, error) {
	w, a := in.Workflow, in.Attempt
	if in.AccountID == "" || !validIdentity(w.ID) || !validIdentity(a.ID) {
		return Request{}, fmt.Errorf("invalid task identity")
	}
	revision := w.ApprovedRevision
	directive := ""
	switch a.Stage {
	case "plan":
		revision = 1
		for _, p := range w.Plans {
			if p.Revision >= revision {
				revision = p.Revision + 1
			}
		}
		directive = fmt.Sprintf("Plan revision %d. Address the idea and latest conversation, preserving prior results. If clarification is required, return nonempty questions and no assignments. Otherwise return 1–20 bounded assignments with unique IDs, concrete instructions, nonempty acceptance criteria and a dependency DAG. Do not execute the work.", revision)
	case "work", "synthesize":
		var approved *taskflow.Plan
		for i := range w.Plans {
			if w.Plans[i].Revision == revision {
				if approved != nil {
					return Request{}, fmt.Errorf("ambiguous approved revision")
				}
				approved = &w.Plans[i]
			}
		}
		if approved == nil {
			return Request{}, fmt.Errorf("approved plan missing")
		}
		b, _ := json.Marshal(taskflow.Result{Text: approved.Summary, Plan: approved})
		if _, e := ValidateResult(b, "plan", revision); e != nil || len(approved.Questions) > 0 {
			return Request{}, fmt.Errorf("approved plan invalid")
		}
		if a.Stage == "work" {
			var assignment *taskflow.Assignment
			for i := range approved.Assignments {
				if approved.Assignments[i].ID == a.AssignmentID {
					assignment = &approved.Assignments[i]
				}
			}
			if assignment == nil {
				return Request{}, fmt.Errorf("assignment missing")
			}
			directive = fmt.Sprintf("Execute only assignment %q in approved revision %d. Follow its instruction and acceptance criteria. Use actual prior attempt outputs for dependencies; report gaps honestly. Return substantive text, including evidence and limitations. The coordinator will judge acceptance.", a.AssignmentID, revision)
		} else {
			directive = "Synthesize the actual outputs and failures for the approved revision. Judge each assignment against its acceptance criteria. Never fabricate missing outputs. Return a useful final answer and verdict accepted, needs_revision, or blocked. Process exit zero alone is not acceptance."
		}
	default:
		return Request{}, fmt.Errorf("invalid task stage")
	}
	snapshot, e := json.Marshal(struct {
		Directive string            `json:"directive"`
		Workflow  taskflow.Workflow `json:"workflow"`
		Attempt   taskflow.Attempt  `json:"attempt"`
	}{directive, w, a})
	if e != nil {
		return Request{}, e
	}
	root := taskRoot + "/attempts/" + a.ID
	req := Request{Stage: a.Stage, Revision: revision, Agent: w.Agent, Workspace: root + "/workspace", Prompt: string(snapshot), ResultPath: root + "/result.json"}
	return req, validateRequest(req)
}

func (r *Runner) Start(ctx context.Context, in taskflow.Input) (taskflow.Submission, error) {
	c := r.config
	w, a := in.Workflow, in.Attempt
	if in.AccountID == "" || !validIdentity(w.ID) || !validIdentity(a.ID) {
		return taskflow.Submission{}, fmt.Errorf("invalid task identity")
	}
	if a.BoxID != "" {
		found, e := c.Controller.FindTaskRunner(ctx, in.AccountID, a.BoxID, a.ID)
		if e != nil {
			return taskflow.Submission{}, e
		}
		if found != nil {
			return taskflow.Submission{BoxID: a.BoxID, TaskID: found.ID, State: found.State}, nil
		}
		if a.TaskID != "" {
			return taskflow.Submission{}, fmt.Errorf("accepted task is missing; reconciliation required")
		}
	}
	req, e := requestFor(in)
	if e != nil {
		return taskflow.Submission{}, e
	}
	if len(w.AssetIDs) > 8 || (len(w.AssetIDs) > 0 && !ImageCapabilities()[w.Agent]) {
		return taskflow.Submission{}, fmt.Errorf("image input unsupported or excessive")
	}
	b, e := c.Controller.EnsureTaskBox(ctx, in.AccountID, w.ID, a.ID, a.BoxID, w.Agent, w.Profile)
	if e != nil {
		return taskflow.Submission{}, e
	}
	sub := taskflow.Submission{BoxID: b.ID, State: string(b.State), Pending: true}
	// Persist the resolved box before any staging or submission, enabling recovery
	// even if Submit's acknowledgement is lost or the box has since hibernated.
	if a.BoxID == "" || b.State != v1.LogicalBoxRunning {
		return sub, nil
	}
	if b.ID != a.BoxID {
		return sub, fmt.Errorf("task box changed")
	}
	images := []Image{}
	seen := map[string]bool{}
	total := 0
	for _, id := range w.AssetIDs {
		if c.Assets == nil || !validIdentity(id) || seen[id] {
			return sub, fmt.Errorf("private asset unavailable or duplicate")
		}
		seen[id] = true
		f, manifest, e := c.Assets.Open(ctx, in.AccountID, id)
		if e != nil {
			return sub, e
		}
		data, e := io.ReadAll(io.LimitReader(f, MaxImageBytes+1))
		f.Close()
		total += len(data)
		h := sha256.Sum256(data)
		if e != nil || len(data) > MaxImageBytes || total > MaxImagesBytes || manifest.ID != id || manifest.Size != int64(len(data)) || manifest.SHA256 != hex.EncodeToString(h[:]) {
			return sub, fmt.Errorf("private image integrity failure")
		}
		images = append(images, Image{ID: id, Data: data})
		req.Images = append(req.Images, path.Join(taskRoot, "attempts", a.ID, "images", id))
	}
	token, e := c.Inbox.Issue(ctx, in.AccountID, w.ID, a.ID)
	if e != nil {
		return sub, e
	}
	job, e := json.Marshal(Job{Version: 1, AttemptID: a.ID, Request: req, ReceiptPath: taskRoot + "/attempts/" + a.ID + "/receipt.json", DeliveryURL: c.CallbackURL, DeliveryToken: token})
	if e != nil {
		return sub, e
	}
	conn, e := c.Controller.Connection(ctx, in.AccountID, b.ID)
	if e != nil {
		return sub, e
	}
	if e = c.Stage(ctx, Input{Connection: conn.Connection, AttemptID: a.ID, Job: job, Images: images}); e != nil {
		return sub, e
	}
	current, e := c.Controller.Connection(ctx, in.AccountID, b.ID)
	if e != nil {
		return sub, e
	}
	if current.Connection.Endpoint != conn.Connection.Endpoint || current.Connection.Metadata["deploymentInstanceId"] != conn.Connection.Metadata["deploymentInstanceId"] {
		return sub, fmt.Errorf("assignment changed during staging")
	}
	t, e := c.Controller.SubmitTaskRunner(ctx, in.AccountID, b.ID, a.ID)
	if e != nil {
		return sub, e
	}
	return taskflow.Submission{BoxID: b.ID, TaskID: t.ID, State: t.State}, nil
}

func (r *Runner) Observe(ctx context.Context, in taskflow.Input) (taskflow.Observation, error) {
	a, w := in.Attempt, in.Workflow
	c := r.config
	if a.TaskID == "" || a.BoxID == "" {
		return taskflow.Observation{}, fmt.Errorf("missing submitted task")
	}
	t, e := c.Controller.Process(ctx, in.AccountID, a.TaskID)
	if e != nil {
		return taskflow.Observation{}, e
	}
	if t.ID != a.TaskID || t.LogicalBoxID != a.BoxID || t.Agent != "shell" {
		return taskflow.Observation{}, fmt.Errorf("task process identity changed")
	}
	if t.State != "exited" {
		return taskflow.Observation{State: t.State}, nil
	}
	body, e := c.Inbox.Get(ctx, in.AccountID, w.ID, a.ID)
	if errors.Is(e, resultinbox.ErrNotFound) {
		return taskflow.Observation{State: "result_missing", Finished: true, Failure: "No durable agent evidence: wrapper startup, isolation, interruption or delivery requires reconciliation; do not replay."}, nil
	}
	if e != nil {
		return taskflow.Observation{}, e
	}
	envelope, e := resultinbox.Decode(body)
	if e != nil || envelope.AttemptID != a.ID {
		return taskflow.Observation{}, fmt.Errorf("invalid task receipt")
	}
	obs := taskflow.Observation{State: "exited", Finished: true, ExitCode: envelope.ExitCode, Signal: envelope.Signal}
	var report Report
	if strictJSON(envelope.Document, &report) != nil || envelope.Truncated {
		obs.Failure = "invalid_task_report"
		return obs, nil
	}
	if !report.AgentEvidence {
		obs.ExitCode, obs.Signal = nil, 0
		obs.Failure = "agent_startup_or_process_evidence_unavailable"
		return obs, nil
	}
	if report.Failure != "" {
		obs.Failure = report.Failure
		return obs, nil
	}
	req, e := requestFor(in)
	if e != nil {
		return taskflow.Observation{}, e
	}
	doc, _ := json.Marshal(report.Result)
	result, e := ValidateResult(doc, a.Stage, req.Revision)
	if e != nil {
		obs.Failure = "invalid_task_result"
	} else if envelope.ExitCode == nil || *envelope.ExitCode != 0 {
		obs.Failure = "agent_process_failed"
	} else {
		obs.Result = result
	}
	return obs, nil
}
