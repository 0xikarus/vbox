package remoteplan

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/factory"
	"github.com/0xikarus/vmbox-service/internal/factory/planjob"
	"github.com/0xikarus/vmbox-service/internal/factory/resultinbox"
	"github.com/0xikarus/vmbox-service/internal/provider"
)

// These controlled controller/repository fixtures test orchestration, not agents.
type controllerFixture struct {
	box                    v1.LogicalBox
	task                   v1.ProcessTask
	connections, submitted int
	change                 bool
	found                  *v1.ProcessTask
}

func (f *controllerFixture) FindPlanner(context.Context, string, string, string) (*v1.ProcessTask, error) {
	return f.found, nil
}

func (f *controllerFixture) EnsurePlanningBox(context.Context, string, factory.Work) (v1.LogicalBox, error) {
	return f.box, nil
}
func (f *controllerFixture) Connection(context.Context, string, string) (v1.LogicalBoxConnection, error) {
	f.connections++
	endpoint := "box@one.example"
	if f.change && f.connections > 1 {
		endpoint = "box@two.example"
	}
	return v1.LogicalBoxConnection{LogicalBoxID: f.box.ID, Connection: provider.Connection{Transport: "openssh", Endpoint: endpoint}}, nil
}
func (f *controllerFixture) SubmitPlanner(context.Context, string, string, string) (v1.ProcessTask, error) {
	f.submitted++
	return f.task, nil
}
func (f *controllerFixture) Process(context.Context, string, string) (v1.ProcessTask, error) {
	return f.task, nil
}

type repoFixture struct {
	sha    string
	writes bool
}

func (f *repoFixture) Resolve(_ context.Context, account, id, ref string) (factory.Repository, string, error) {
	return factory.Repository{ID: id, FullName: "owner/repo"}, f.sha, nil
}
func (f *repoFixture) RepositoryToken(_ context.Context, account, id string, write bool) (string, time.Time, error) {
	f.writes = write
	return "scoped-fixture-token", time.Now().Add(time.Hour), nil
}

type inboxFixture struct{ result []byte }

func (f *inboxFixture) Issue(context.Context, string, string, string) (string, error) {
	return strings.Repeat("x", 43), nil
}
func (f *inboxFixture) Get(context.Context, string, string, string) ([]byte, error) {
	if f.result == nil {
		return nil, resultinbox.ErrNotFound
	}
	return f.result, nil
}

func setup() (*Runner, factory.Claim, *controllerFixture, *inboxFixture) {
	a := strings.Repeat("a", 32)
	sha := strings.Repeat("b", 40)
	c := factory.Claim{AccountID: "account", Work: factory.Work{ID: strings.Repeat("c", 32), CreateWork: factory.CreateWork{Agent: "codex", RepositoryID: "123", Profile: "profile"}, RepositoryName: "owner/repo", BaseSHA: sha, BoxID: "box", Messages: []factory.Message{{Role: "user", Text: "Inspect the actual repository"}}, Attempts: []factory.Attempt{{ID: a, TaskID: "task"}}}}
	controller := &controllerFixture{box: v1.LogicalBox{ID: "box", Name: "planner", State: v1.LogicalBoxRunning}, task: v1.ProcessTask{ID: "task", LogicalBoxID: "box", Agent: "shell", State: "running"}}
	inbox := &inboxFixture{}
	r := &Runner{Controller: controller, Repositories: &repoFixture{sha: sha}, Inbox: inbox, CallbackURL: "https://factory.example/result", Stage: func(context.Context, Input) error { return nil }}
	return r, c, controller, inbox
}

func TestProvisioningDoesNotStageOrSubmit(t *testing.T) {
	r, c, controller, _ := setup()
	controller.box.State = v1.LogicalBoxAttaching
	r.Stage = func(context.Context, Input) error { t.Fatal("staged before running"); return nil }
	s, err := r.Start(context.Background(), c)
	if err != nil || !s.Pending || s.BoxID != "box" || s.State != "attaching" || controller.submitted != 0 {
		t.Fatalf("bad provisioning: %+v %v", s, err)
	}
}

func TestStagePinnedSourceAndPrivateJob(t *testing.T) {
	r, c, controller, _ := setup()
	r.Stage = func(_ context.Context, in Input) error {
		if in.Repository != "owner/repo" || in.BaseSHA != c.Work.BaseSHA || in.AttemptID != c.Work.Attempts[0].ID {
			t.Error("source/attempt changed")
		}
		var job planjob.Job
		if json.Unmarshal(in.Job, &job) != nil || job.AttemptID != in.AttemptID || !strings.Contains(job.Request.Prompt, c.Work.Messages[0].Text) {
			t.Error("missing actual conversation")
		}
		if job.DeliveryToken == "" || in.SourceToken == "" || strings.Contains(job.Request.Prompt, in.SourceToken) {
			t.Error("invalid credential separation")
		}
		if !strings.HasSuffix(job.Request.Workspace, "/repo") {
			t.Error("wrong working directory")
		}
		return nil
	}
	s, err := r.Start(context.Background(), c)
	if err != nil || s.TaskID != "task" || s.Pending || controller.connections != 2 || controller.submitted != 1 {
		t.Fatalf("bad submission: %+v %v", s, err)
	}
	if r.Repositories.(*repoFixture).writes {
		t.Fatal("planning requested repository write")
	}
}

func TestAssignmentChangeDoesNotLaunch(t *testing.T) {
	r, c, controller, _ := setup()
	controller.change = true
	if _, err := r.Start(context.Background(), c); err == nil || controller.submitted != 0 {
		t.Fatal("launched against changed assignment")
	}
}

func TestPinnedSourceChangeDoesNotLaunch(t *testing.T) {
	r, c, controller, _ := setup()
	r.Repositories.(*repoFixture).sha = "changed"
	if _, err := r.Start(context.Background(), c); err == nil || controller.submitted != 0 {
		t.Fatal("changed source accepted")
	}
}

func TestObserveNeverUsesWrapperExitAsAgentOutcome(t *testing.T) {
	r, c, controller, inbox := setup()
	zero := 0
	seven := 7
	controller.task.State = "exited"
	controller.task.ExitCode = &zero
	o, err := r.Observe(context.Background(), c)
	if err != nil || o.Finished || o.ExitCode != nil || o.State != "result_missing" {
		t.Fatal("wrapper exit manufactured agent completion")
	}
	inbox.result, _ = json.Marshal(resultinbox.Result{Version: 1, AttemptID: c.Work.Attempts[0].ID, ExitCode: &seven})
	o, err = r.Observe(context.Background(), c)
	if err != nil || !o.Finished || o.ExitCode == nil || *o.ExitCode != 7 {
		t.Fatal("actual agent exit lost")
	}
	controller.task.LogicalBoxID = "sibling"
	if _, err = r.Observe(context.Background(), c); err == nil {
		t.Fatal("sibling result accepted")
	}
}

func TestAcceptedSubmissionRecoveredWithoutRestaging(t *testing.T) {
	r, c, controller, _ := setup()
	controller.found = &controller.task
	controller.box.State = v1.LogicalBoxHibernated
	r.Stage = func(context.Context, Input) error { t.Fatal("accepted attempt restaged"); return nil }
	s, err := r.Start(context.Background(), c)
	if err != nil || s.TaskID != "task" || controller.connections != 0 || controller.submitted != 0 {
		t.Fatal("lost submission not recovered")
	}
}
