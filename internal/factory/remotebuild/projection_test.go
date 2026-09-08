package remotebuild

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/0xikarus/vmbox-service/internal/factory"
	"github.com/0xikarus/vmbox-service/internal/factory/buildjob"
	"github.com/0xikarus/vmbox-service/internal/factory/execution"
	"github.com/0xikarus/vmbox-service/internal/factory/staging"
	"github.com/DATA-DOG/go-sqlmock"
)

type workCapture struct{ work *factory.Work }

func (c workCapture) Match(v driver.Value) bool {
	b, ok := v.([]byte)
	return ok && json.Unmarshal(b, c.work) == nil
}

// Exercise Store.mutate itself so the runner receives the JSON projection that
// production persists, rather than a hand-maintained approximation of it.
func persistProjection(t *testing.T, in *Input, g execution.Graph, reserve bool) execution.Graph {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	wj, err := json.Marshal(in.Work)
	if err != nil {
		t.Fatal(err)
	}
	gj, err := json.Marshal(g)
	if err != nil {
		t.Fatal(err)
	}
	mock.ExpectBegin()
	mock.ExpectExec("SELECT pg_advisory_xact_lock").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT document FROM factory_work_items").WithArgs(in.AccountID, in.Work.ID).WillReturnRows(sqlmock.NewRows([]string{"document"}).AddRow(wj))
	mock.ExpectQuery("SELECT version,document FROM factory_execution_graphs").WithArgs(in.AccountID, in.Work.ID).WillReturnRows(sqlmock.NewRows([]string{"version", "document"}).AddRow(1, gj))
	if reserve {
		mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	}
	mock.ExpectExec("UPDATE factory_execution_graphs").WithArgs(in.AccountID, in.Work.ID, sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 1))
	var projected factory.Work
	mock.ExpectExec("UPDATE factory_work_items").WithArgs(in.AccountID, in.Work.ID, workCapture{&projected}, "implementing").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	s := execution.Store{DB: db}
	var snapshot execution.Snapshot
	if reserve {
		snapshot, err = s.Reserve(context.Background(), in.AccountID, in.Work.ID, 1, in.Node.Feature.ID, "build", 1)
	} else {
		snapshot, err = s.Bind(context.Background(), in.AccountID, in.Work.ID, 1, in.Node.Feature.ID, in.Node.Attempt.ID, "builder", "task")
	}
	if err != nil {
		t.Fatal(err)
	}
	if err = mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
	in.Work = projected
	in.Node = snapshot.Graph.Nodes[0]
	return snapshot.Graph
}

func TestPersistedReserveAndBindProjection(t *testing.T) {
	r, in, c, inbox := setup(t)
	in.Work.MaxWorkers = 1
	in.Work.Features[0].State = "queued"
	// Publication evidence retained in the graph is overwritten by mutate's
	// execution projection; it must not alter the request or its receipt hash.
	in.Work.Features[0].BoxID = "publication-box"
	in.Work.Features[0].PRURL = "https://github.com/owner/repo/pull/3"
	g, err := execution.New(in.Work)
	if err != nil {
		t.Fatal(err)
	}
	g = persistProjection(t, &in, g, true)
	if in.Work.State != "implementing" || in.Work.Features[0].State != "building" || in.Work.Features[0].BoxID != "" || in.Work.Features[0].PRURL != "" || len(in.Work.FeatureAttempts) != 1 {
		t.Fatalf("unexpected Reserve projection: %+v", in.Work)
	}
	before, _ := json.Marshal(in)
	req, err := r.request(in)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(in)
	if string(before) != string(after) {
		t.Fatal("validation mutated persisted input")
	}
	submission, err := r.Start(context.Background(), in)
	if err != nil || !submission.Pending || submission.BoxID != "builder" {
		t.Fatalf("Reserve Start: %+v %v", submission, err)
	}
	// Persisted box intent permits fresh staging before the task is bound.
	in.Node.Attempt.BoxID = submission.BoxID
	var job buildjob.Job
	r.Stage = func(_ context.Context, s staging.Input) error { return json.Unmarshal(s.Job, &job) }
	submission, err = r.Start(context.Background(), in)
	if err != nil || submission.TaskID != "task" || !reflect.DeepEqual(job.Request, req) {
		t.Fatalf("staged Start: %+v %v", submission, err)
	}
	persistProjection(t, &in, g, false)
	if in.Work.Features[0].BoxID != "builder" || in.Work.FeatureAttempts[0].TaskID != "task" || in.Node.Attempt.State != "submitted" {
		t.Fatal("missing Bind projection")
	}
	bound, err := r.request(in)
	if err != nil || !reflect.DeepEqual(req, bound) {
		t.Fatalf("Bind changed request: %v", err)
	}
	c.found = &c.task
	submission, err = r.Start(context.Background(), in)
	if err != nil || submission.TaskID != "task" || c.submit != 1 {
		t.Fatalf("Bind recovery: %+v %v", submission, err)
	}
	c.task.State = "exited"
	report := reportFor(r, in)
	report.RequestHash = requestHash(in, job.Request, r.CallbackURL)
	deliver(inbox, in, report)
	observation, err := r.Observe(context.Background(), in)
	if err != nil || !observation.Finished {
		t.Fatalf("Bind Observe: %+v %v", observation, err)
	}
}

func TestProjectedWorkRejectsChangedIdentity(t *testing.T) {
	cases := map[string]func(*Input){
		"work state":           func(in *Input) { in.Work.State = "planning" },
		"approval revision":    func(in *Input) { in.Work.ApprovedPlanRevision++ },
		"approval input":       func(in *Input) { in.Work.Plans[0].InputRevision = in.Work.Revision },
		"approval contents":    func(in *Input) { in.Work.Plans[0].Features[0].Title = "changed" },
		"master issue":         func(in *Input) { in.Work.MasterIssueURL = "https://github.com/other/repo/issues/1" },
		"changed issue":        func(in *Input) { in.Work.Features[0].IssueURL = "https://github.com/owner/repo/issues/99" },
		"node issue":           func(in *Input) { in.Node.Feature.IssueURL = "https://github.com/owner/repo/issues/99" },
		"missing issue":        func(in *Input) { in.Work.Features[0].IssueURL = "" },
		"feature title":        func(in *Input) { in.Work.Features[0].Title = "changed" },
		"feature checks":       func(in *Input) { in.Work.Features[0].Checks = nil },
		"feature ownership":    func(in *Input) { in.Work.Features[0].Files = []string{"changed.go"} },
		"feature acceptance":   func(in *Input) { in.Work.Features[0].AcceptanceCriteria = []string{"changed"} },
		"feature dependencies": func(in *Input) { in.Work.Features[0].DependsOn = []string{"other"} },
		"node feature":         func(in *Input) { in.Node.Feature.Description = "changed" },
		"branch":               func(in *Input) { in.Node.Branch += "-changed" },
		"account":              func(in *Input) { in.AccountID = "other" },
		"attempt":              func(in *Input) { in.Node.Attempt.ID = strings.Repeat("f", 32) },
		"source":               func(in *Input) { in.PreparedSourceSHA = strings.Repeat("f", 40) },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			r, in, c, _ := setup(t)
			in.Work.MaxWorkers = 1
			g, err := execution.New(in.Work)
			if err != nil {
				t.Fatal(err)
			}
			g = persistProjection(t, &in, g, true)
			persistProjection(t, &in, g, false)
			c.found = &c.task
			mutate(&in)
			if _, err := r.Start(context.Background(), in); err == nil {
				t.Fatal("Start accepted changed identity")
			}
			if _, err := r.Observe(context.Background(), in); err == nil {
				t.Fatal("Observe accepted changed identity")
			}
			if c.ensure != 0 || c.submit != 0 {
				t.Fatal("invalid identity launched work")
			}
		})
	}
}
