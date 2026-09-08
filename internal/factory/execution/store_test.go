package execution

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"testing"

	"github.com/0xikarus/vmbox-service/internal/factory"
	_ "github.com/jackc/pgx/v5/stdlib"
)

func database(t *testing.T) (Store, *factory.Store) {
	t.Helper()
	dsn := os.Getenv("VMBOX_FACTORY_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("isolated PostgreSQL required")
	}
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	var suffix [12]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("execution_test_%x", suffix)
	if _, err = admin.Exec("CREATE SCHEMA " + schema); err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	db, err := sql.Open("pgx", u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close(); admin.Exec("DROP SCHEMA " + schema + " CASCADE"); admin.Close() })
	core := &factory.Store{DB: db}
	if err = core.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	s := Store{DB: db}
	if err = s.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return s, core
}

func TestDurableFeatureAdmissionIdentityAndIsolation(t *testing.T) {
	s, core := database(t)
	ctx := context.Background()
	w := approved()
	w.MaxWorkers = 2
	b, _ := json.Marshal(w)
	_, err := s.DB.Exec(`INSERT INTO factory_work_items(id,account_id,user_id,request_key,request_hash,revision,state,document,created_at,updated_at) VALUES($1,'a','u','fixture','fixture',2,'build_queued',$2,now(),now())`, w.ID, b)
	if err != nil {
		t.Fatal(err)
	}
	x, err := s.Initialize(ctx, "a", w.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Load(ctx, "foreign", w.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("foreign account read")
	}
	x, err = s.Reserve(ctx, "a", w.ID, x.Version, "api", "build", 1)
	if err != nil {
		t.Fatal(err)
	}
	attempt := x.Graph.Nodes[0].Attempt.ID
	if _, err = s.Reserve(ctx, "a", w.ID, x.Version, "docs", "build", 1); !errors.Is(err, ErrCapacity) {
		t.Fatal("feature cap exceeded")
	}
	_, err = core.Create(ctx, "a", "u", "plan", factory.CreateWork{RepositoryID: "r", Idea: "idea", Agent: "codex", Profile: "p"}, factory.Repository{ID: "r"}, "sha", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = core.ClaimLimited(ctx, 1); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("planning ignored active feature worker")
	}
	restarted := Store{DB: s.DB}
	recovered, err := restarted.Load(ctx, "a", w.ID)
	if err != nil || recovered.Graph.Nodes[0].Attempt.ID != attempt {
		t.Fatal("attempt lost on restart")
	}
	if _, err = s.Bind(ctx, "a", w.ID, 1, "api", attempt, "builder", "task"); !errors.Is(err, ErrConflict) {
		t.Fatal("stale writer accepted")
	}
	x, err = s.Bind(ctx, "a", w.ID, recovered.Version, "api", attempt, "builder", "task")
	if err != nil {
		t.Fatal(err)
	}
	r := built()
	r.AttemptID = attempt
	wrong := r
	wrong.BoxID = "foreign"
	if _, err = s.AcceptBuild(ctx, "a", w.ID, x.Version, "api", wrong); !errors.Is(err, ErrConflict) {
		t.Fatal("wrong process accepted")
	}
	x, err = s.AcceptBuild(ctx, "a", w.ID, x.Version, "api", r)
	if err != nil {
		t.Fatal(err)
	}
	if x.Graph.Nodes[0].State != "needs_verification" {
		t.Fatal("builder marked verified")
	}
	if _, err = s.Bind(ctx, "a", w.ID, x.Version, "api", attempt, "builder", "task"); !errors.Is(err, ErrConflict) {
		t.Fatal("terminal attempt rebound")
	}
	if _, err = core.ClaimLimited(ctx, 1); err != nil {
		t.Fatal("finished build did not free admission")
	}
	projected, err := core.Get(ctx, "a", w.ID)
	if err != nil || projected.Features[0].State != "needs_verification" || projected.Features[0].BoxID != "builder" {
		t.Fatal("UI projection missing")
	}
}

func TestTerminalFailureReleasesCapacityWithoutInventingExit(t *testing.T) {
	s, _ := database(t)
	ctx := context.Background()
	w := approved()
	w.MaxWorkers = 2
	b, _ := json.Marshal(w)
	if _, err := s.DB.Exec(`INSERT INTO factory_work_items(id,account_id,user_id,request_key,request_hash,revision,state,document,created_at,updated_at) VALUES($1,'a','u','failure','failure',2,'build_queued',$2,now(),now())`, w.ID, b); err != nil {
		t.Fatal(err)
	}
	x, err := s.Initialize(ctx, "a", w.ID)
	if err != nil {
		t.Fatal(err)
	}
	x, err = s.Reserve(ctx, "a", w.ID, x.Version, "api", "build", 1)
	if err != nil {
		t.Fatal(err)
	}
	p := Process{AttemptID: x.Graph.Nodes[0].Attempt.ID, BoxID: "builder", TaskID: "failed-task"}
	x, err = s.Bind(ctx, "a", w.ID, x.Version, "api", p.AttemptID, p.BoxID, p.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Fail(ctx, "a", w.ID, x.Version, "api", "build", p, "process_failed"); !errors.Is(err, ErrConflict) {
		t.Fatal("missing exit treated as completion")
	}
	zero := 0
	p.ExitCode = &zero
	if _, err = s.Fail(ctx, "a", w.ID, x.Version, "api", "build", p, "process_failed"); !errors.Is(err, ErrConflict) {
		t.Fatal("exit zero called process failure")
	}
	x, err = s.Fail(ctx, "a", w.ID, x.Version, "api", "build", p, "candidate_rejected")
	if err != nil {
		t.Fatal(err)
	}
	n := x.Graph.Nodes[0]
	if n.State != "build_failed" || n.Attempt.Process.ExitCode == nil || *n.Attempt.Process.ExitCode != 0 || n.Attempt.Failure != "candidate_rejected" {
		t.Fatal("semantic failure lost real exit")
	}
	if _, err = s.Bind(ctx, "a", w.ID, x.Version, "api", p.AttemptID, p.BoxID, p.TaskID); !errors.Is(err, ErrConflict) {
		t.Fatal("failed task replayed")
	}
	if _, err = s.Reserve(ctx, "a", w.ID, x.Version, "docs", "build", 1); err != nil {
		t.Fatal("failure did not free admission", err)
	}
}
