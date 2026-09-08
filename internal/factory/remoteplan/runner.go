// Package remoteplan connects durable planning claims to controller-managed boxes.
package remoteplan

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/factory"
	"github.com/0xikarus/vmbox-service/internal/factory/planjob"
	"github.com/0xikarus/vmbox-service/internal/factory/planner"
	"github.com/0xikarus/vmbox-service/internal/factory/resultinbox"
	"github.com/0xikarus/vmbox-service/internal/provider"
)

type Controller interface {
	EnsurePlanningBox(context.Context, string, factory.Work) (v1.LogicalBox, error)
	Connection(context.Context, string, string) (v1.LogicalBoxConnection, error)
	SubmitPlanner(context.Context, string, string, string) (v1.ProcessTask, error)
	Process(context.Context, string, string) (v1.ProcessTask, error)
	FindPlanner(context.Context, string, string, string) (*v1.ProcessTask, error)
}
type Repositories interface {
	Resolve(context.Context, string, string, string) (factory.Repository, string, error)
	RepositoryToken(context.Context, string, string, bool) (string, time.Time, error)
}
type Inbox interface {
	Issue(context.Context, string, string, string) (string, error)
	Get(context.Context, string, string, string) ([]byte, error)
}
type Image struct {
	ID   string
	Data []byte
}
type Input struct {
	Connection                                  provider.Connection
	AttemptID, Repository, BaseSHA, SourceToken string
	Job                                         []byte
	Images                                      []Image
}
type Runner struct {
	Controller   Controller
	Repositories Repositories
	Inbox        Inbox
	Assets       factory.AssetBackend
	Stage        func(context.Context, Input) error
	CallbackURL  string
}

func (r *Runner) Start(ctx context.Context, c factory.Claim) (factory.Submission, error) {
	w := c.Work
	if len(w.Attempts) == 0 || r.Controller == nil || r.Repositories == nil || r.Inbox == nil || r.Stage == nil || r.CallbackURL == "" {
		return factory.Submission{}, fmt.Errorf("planning backend incomplete")
	}
	a := w.Attempts[len(w.Attempts)-1]
	if w.BoxID != "" {
		found, err := r.Controller.FindPlanner(ctx, c.AccountID, w.BoxID, a.ID)
		if err != nil {
			return factory.Submission{}, err
		}
		if found != nil {
			return factory.Submission{TaskID: found.ID, BoxID: w.BoxID, BoxName: found.BoxName}, nil
		}
	}
	b, err := r.Controller.EnsurePlanningBox(ctx, c.AccountID, w)
	if err != nil {
		return factory.Submission{}, err
	}
	submission := factory.Submission{BoxID: b.ID, BoxName: b.Name, State: string(b.State), Pending: true}
	if w.BoxID == "" {
		// Persist the resolved box before staging or submission. A restart can
		// then recover its exact accepted task without allocating new compute.
		return submission, nil
	}
	if b.State != v1.LogicalBoxRunning {
		return submission, nil
	}
	// Recheck repository installation authority at execution, using the original
	// pinned commit, never silently switching to a newer default-branch head.
	repo, sha, err := r.Repositories.Resolve(ctx, c.AccountID, w.RepositoryID, w.BaseSHA)
	if err != nil {
		return factory.Submission{}, err
	}
	if sha != w.BaseSHA || repo.FullName != w.RepositoryName {
		return factory.Submission{}, factory.RejectPlanning("The authorized repository or pinned source revision changed.")
	}
	prompt, err := json.Marshal(struct {
		Repository    string            `json:"repository"`
		BaseSHA       string            `json:"baseSha"`
		Messages      []factory.Message `json:"messages"`
		PreviousPlans []factory.Plan    `json:"previousPlans"`
	}{w.RepositoryName, w.BaseSHA, w.Messages, w.Plans})
	if err != nil || len(prompt) > 100000 {
		return factory.Submission{}, factory.RejectPlanning("Planning history exceeds the current agent input limit; start a new work item with a concise summary.")
	}
	root := "/data/workspace/.vmbox-factory/attempts/" + a.ID
	request := planner.Request{Agent: w.Agent, Workspace: path.Join(root, "repo"), Prompt: string(prompt), ResultPath: path.Join(root, "plan.json")}
	images := []Image{}
	var total int64
	for _, expected := range w.Assets {
		if r.Assets == nil {
			return factory.Submission{}, fmt.Errorf("private assets unavailable")
		}
		f, actual, e := r.Assets.Open(ctx, c.AccountID, expected.ID)
		if e != nil {
			return factory.Submission{}, e
		}
		data, e := io.ReadAll(io.LimitReader(f, (10<<20)+1))
		f.Close()
		h := sha256.Sum256(data)
		total += int64(len(data))
		if e != nil || actual != expected || int64(len(data)) != expected.Size || hex.EncodeToString(h[:]) != expected.SHA256 || total > 40<<20 {
			return factory.Submission{}, factory.RejectPlanning("A private attachment no longer matches its saved manifest.")
		}
		images = append(images, Image{ID: expected.ID, Data: data})
		request.Images = append(request.Images, path.Join(root, "images", expected.ID))
	}
	if w.Agent == "claude" && len(images) > 0 {
		return factory.Submission{}, factory.RejectPlanning("Claude image planning is not yet supported by the installed adapter.")
	}
	capability, err := r.Inbox.Issue(ctx, c.AccountID, w.ID, a.ID)
	if errors.Is(err, resultinbox.ErrExpired) {
		return factory.Submission{}, factory.RejectPlanning("This attempt's delivery permission expired before launch; reply to start a new attempt.")
	}
	if err != nil {
		return factory.Submission{}, err
	}
	job, err := json.Marshal(planjob.Job{Version: 1, AttemptID: a.ID, Request: request, ReceiptPath: path.Join(root, "receipt.json"), DeliveryURL: r.CallbackURL, DeliveryToken: capability})
	if err != nil {
		return factory.Submission{}, err
	}
	token, expires, err := r.Repositories.RepositoryToken(ctx, c.AccountID, w.RepositoryID, false)
	if err != nil {
		return factory.Submission{}, err
	}
	if time.Until(expires) < time.Minute {
		return factory.Submission{}, fmt.Errorf("repository grant expires too soon")
	}
	connection, err := r.Controller.Connection(ctx, c.AccountID, b.ID)
	if err != nil {
		return factory.Submission{}, err
	}
	if err = r.Stage(ctx, Input{Connection: connection.Connection, AttemptID: a.ID, Repository: w.RepositoryName, BaseSHA: w.BaseSHA, SourceToken: token, Job: job, Images: images}); err != nil {
		return factory.Submission{}, err
	}
	// Resolve once more before submission: staging an old deployment must never
	// cause a command to be launched on a different, unstaged assignment.
	current, err := r.Controller.Connection(ctx, c.AccountID, b.ID)
	if err != nil {
		return factory.Submission{}, err
	}
	if current.Connection.Endpoint != connection.Connection.Endpoint || current.Connection.Metadata["deploymentInstanceId"] != connection.Connection.Metadata["deploymentInstanceId"] {
		return factory.Submission{}, fmt.Errorf("planning assignment changed during staging")
	}
	task, err := r.Controller.SubmitPlanner(ctx, c.AccountID, b.ID, a.ID)
	if err != nil {
		return factory.Submission{}, err
	}
	return factory.Submission{TaskID: task.ID, BoxID: b.ID, BoxName: b.Name}, nil
}

func (r *Runner) Observe(ctx context.Context, c factory.Claim) (factory.Observation, error) {
	w := c.Work
	if len(w.Attempts) == 0 {
		return factory.Observation{}, fmt.Errorf("missing attempt")
	}
	a := w.Attempts[len(w.Attempts)-1]
	task, err := r.Controller.Process(ctx, c.AccountID, a.TaskID)
	if err != nil {
		return factory.Observation{}, err
	}
	if task.ID != a.TaskID || task.LogicalBoxID != w.BoxID || task.Agent != "shell" {
		return factory.Observation{}, fmt.Errorf("planning process identity changed")
	}
	if task.State != "exited" {
		return factory.Observation{State: task.State}, nil
	}
	body, err := r.Inbox.Get(ctx, c.AccountID, w.ID, a.ID)
	if errors.Is(err, resultinbox.ErrNotFound) {
		return factory.Observation{State: "result_missing"}, nil
	}
	if err != nil {
		return factory.Observation{}, err
	}
	report, err := resultinbox.Decode(body)
	if err != nil || report.AttemptID != a.ID {
		return factory.Observation{}, fmt.Errorf("invalid durable planning report")
	}
	// Wrapper exit is intentionally NOT substituted for the actual agent exit.
	return factory.Observation{State: "exited", Finished: true, ExitCode: report.ExitCode, Signal: report.Signal, Result: report.Document, Truncated: report.Truncated}, nil
}
