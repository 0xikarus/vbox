package publishing

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/0xikarus/vmbox-service/internal/factory"
	"github.com/0xikarus/vmbox-service/internal/factory/githubapp"
	_ "github.com/jackc/pgx/v5/stdlib"
)

func database(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("VMBOX_FACTORY_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set VMBOX_FACTORY_TEST_DATABASE_URL to run PostgreSQL publication tests")
	}
	u, err := url.Parse(dsn)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		t.Fatal("test database must use a postgres URL")
	}
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	schema := "factory_publishing_test_" + token()
	if _, err = admin.Exec("CREATE SCHEMA " + schema); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	db, err := sql.Open("pgx", u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Close()
		if _, err := admin.Exec("DROP SCHEMA " + schema + " CASCADE"); err != nil {
			t.Error(err)
		}
		admin.Close()
	})
	if err = (&factory.Store{DB: db}).Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return db
}
func mustExec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
}
func seed(t *testing.T, db *sql.DB, account, state string) factory.Work {
	t.Helper()
	w := factory.Work{ID: token(), Revision: 4, State: state, CreateWork: factory.CreateWork{RepositoryID: "2"}, RepositoryName: "org/repo", BaseSHA: strings.Repeat("a", 40), ApprovedPlanRevision: 1}
	feature := func(id string, deps ...string) factory.Feature {
		return factory.Feature{ID: id, Title: "Feature " + id, Description: "Implement " + id, AcceptanceCriteria: []string{"Verified behavior"}, DependsOn: deps, Files: []string{"internal/example.go"}, Checks: []factory.Check{{Argv: []string{"go", "test", "./internal/..."}, Cwd: ".", TimeoutSeconds: 60}}}
	}
	w.Plans = []factory.Plan{{Revision: 1, InputRevision: 3, BaseSHA: w.BaseSHA, Markdown: "Implement a foundation, then its consumer.", Features: []factory.Feature{feature("consumer", "foundation"), feature("foundation")}}}
	w.Features = append([]factory.Feature{}, w.Plans[0].Features...)
	data, _ := json.Marshal(w)
	mustExec(t, db, `INSERT INTO factory_work_items(id,account_id,user_id,request_key,request_hash,revision,state,document,created_at,updated_at) VALUES($1,$2,'user',$1,'hash',$3,$4,$5,now(),now())`, w.ID, account, w.Revision, state, data)
	return w
}
func getWork(t *testing.T, db *sql.DB, id string) (factory.Work, string) {
	t.Helper()
	var raw []byte
	var master string
	if err := db.QueryRow(`SELECT document,COALESCE(document->>'masterIssueUrl','') FROM factory_work_items WHERE id=$1`, id).Scan(&raw, &master); err != nil {
		t.Fatal(err)
	}
	var w factory.Work
	if err := json.Unmarshal(raw, &w); err != nil {
		t.Fatal(err)
	}
	return w, master
}
func ready(t *testing.T, db *sql.DB) {
	t.Helper()
	mustExec(t, db, `UPDATE factory_publication_jobs SET next_at=now()-interval '1 second',lease_until=now()-interval '1 second'`)
	mustExec(t, db, `UPDATE factory_publication_operations SET lease_until=now()-interval '1 second'`)
}

type fixture struct {
	mu       sync.Mutex
	db       *sql.DB
	calls    []intent
	ops      []githubapp.PublicationOperation
	records  map[string]githubapp.Publication
	payloads map[string]string
	posts    int
	hidden   bool
	failure  error
	before   func(intent, githubapp.PublicationOperation)
}

func newFixture(db *sql.DB) *fixture {
	return &fixture{db: db, records: map[string]githubapp.Publication{}, payloads: map[string]string{}}
}
func (f *fixture) publish(ctx context.Context, a githubapp.WriteAuthority, op githubapp.PublicationOperation, in intent) (githubapp.Publication, error) {
	in.Authority = a
	// The fixture models GitHub's immutable marker/payload lookup and absent
	// reconciliation. Query independently to prove the intent preceded network.
	var persisted []byte
	var state, lease string
	err := f.db.QueryRowContext(ctx, `SELECT intent,state,lease FROM factory_publication_operations WHERE operation_key=$1`, op.Key).Scan(&persisted, &state, &lease)
	if err != nil {
		return githubapp.Publication{}, fmt.Errorf("intent not committed: %w", err)
	}
	var saved intent
	if err = json.Unmarshal(persisted, &saved); err != nil {
		return githubapp.Publication{}, err
	}
	if state != "attempted" || lease == "" || saved.Authority != a || saved.Kind != in.Kind || saved.FeatureID != in.FeatureID || saved.Master != in.Master || saved.Body != in.Body || hash(saved.Dependencies) != hash(in.Dependencies) {
		return githubapp.Publication{}, errors.New("durable invocation mismatch")
	}
	// Comments do not carry Work in the client method; compare the durable work
	// for issue methods, including its complete approved plan.
	if in.Kind != "comment" && hash(saved.Work) != hash(in.Work) {
		return githubapp.Publication{}, errors.New("snapshot mismatch")
	}
	tx, err := f.db.BeginTx(ctx, nil)
	if err != nil {
		return githubapp.Publication{}, err
	}
	_, err = tx.ExecContext(ctx, `SELECT id FROM factory_work_items WHERE id=$1 FOR UPDATE NOWAIT`, saved.Work.ID)
	tx.Rollback()
	if err != nil {
		return githubapp.Publication{}, fmt.Errorf("DB lock held across network: %w", err)
	}
	f.mu.Lock()
	f.calls = append(f.calls, in)
	f.ops = append(f.ops, op)
	hook := f.before
	f.mu.Unlock()
	if hook != nil {
		hook(in, op)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if a.AccountID != "account" || a.RepositoryID != "2" || !a.IssuesWrite {
		return githubapp.Publication{}, githubapp.ErrNotAllowed
	}
	if f.failure != nil {
		return githubapp.Publication{}, f.failure
	}
	payload := hash(in)
	if prior, ok := f.records[op.Key]; ok && !f.hidden {
		if f.payloads[op.Key] != payload {
			return githubapp.Publication{}, githubapp.ErrPublicationConflict
		}
		return prior, nil
	}
	if op.ReconcileOnly {
		return githubapp.Publication{}, githubapp.ErrPublicationUncertain
	}
	f.posts++
	result := githubapp.Publication{ID: int64(1000 + f.posts), Number: int64(100 + f.posts)}
	if in.Kind == "comment" {
		result.Number = 0
	}
	f.records[op.Key] = result
	f.payloads[op.Key] = payload
	if f.hidden {
		return githubapp.Publication{}, githubapp.ErrPublicationUncertain
	}
	return result, nil
}
func (f *fixture) PublishMasterIssue(ctx context.Context, a githubapp.WriteAuthority, op githubapp.PublicationOperation, w factory.Work) (githubapp.Publication, error) {
	return f.publish(ctx, a, op, intent{Work: w, Kind: "master"})
}
func (f *fixture) PublishFeatureIssue(ctx context.Context, a githubapp.WriteAuthority, op githubapp.PublicationOperation, w factory.Work, id string, master int64, deps map[string]int64) (githubapp.Publication, error) {
	return f.publish(ctx, a, op, intent{Work: w, Kind: "feature", FeatureID: id, Master: master, Dependencies: deps})
}
func (f *fixture) PublishIssueComment(ctx context.Context, a githubapp.WriteAuthority, op githubapp.PublicationOperation, master int64, body string) (githubapp.Publication, error) {
	return f.publish(ctx, a, op, intent{Kind: "comment", Master: master, Body: body})
}
func coordinator(t *testing.T) (*Coordinator, *fixture) {
	t.Helper()
	db := database(t)
	f := newFixture(db)
	c := &Coordinator{DB: db, Client: f, Policy: Policy{AccountID: "account", IssuesWrite: true}}
	if err := c.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := c.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return c, f
}

func TestPublicationDAGAndProgress(t *testing.T) {
	c, f := coordinator(t)
	ctx := context.Background()
	original := seed(t, c.DB, "account", "approved_queued")
	mustExec(t, c.DB, `UPDATE factory_work_items SET document=document || '{"futureField":"preserve"}'::jsonb WHERE id=$1`, original.ID)
	for i := 0; i < 4; i++ {
		// New instances demonstrate there is no in-memory progress dependency.
		c = &Coordinator{DB: c.DB, Client: f, Policy: c.Policy}
		if err := c.Step(ctx); err != nil {
			t.Fatalf("step %d: %v", i, err)
		}
		w, master := getWork(t, c.DB, original.ID)
		if master != "https://github.com/org/repo/issues/101" {
			t.Fatalf("master: %s", master)
		}
		if hash(w.Plans) != hash(original.Plans) || w.Revision != original.Revision {
			t.Fatal("approved plan or revision mutated")
		}
		for _, feature := range w.Features {
			if feature.State == "implemented" || feature.PRURL != "" || feature.BoxID != "" {
				t.Fatal("invented implementation")
			}
		}
		want := "publishing_issues"
		if i == 3 {
			want = "build_queued"
		}
		if w.State != want {
			t.Fatalf("step %d state %s", i, w.State)
		}
	}
	if f.posts != 4 || f.calls[1].FeatureID != "foundation" || f.calls[2].FeatureID != "consumer" || f.calls[2].Dependencies["foundation"] != 102 || f.calls[2].Master != 101 {
		t.Fatalf("wrong DAG publication: %+v", f.calls)
	}
	body := f.calls[3].Body
	if !strings.Contains(body, "https://github.com/org/repo/issues/102") || !strings.Contains(body, "https://github.com/org/repo/issues/103") || !strings.Contains(body, "verification are still pending") {
		t.Fatalf("meaningless comment: %s", body)
	}
	var preserved string
	if err := c.DB.QueryRow(`SELECT document->>'futureField' FROM factory_work_items WHERE id=$1`, original.ID).Scan(&preserved); err != nil || preserved != "preserve" {
		t.Fatal("lost unknown document field", err)
	}
	if err := c.Step(ctx); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal(err)
	}
}

func TestUnknownNeverRepeatsPostAndRecovers(t *testing.T) {
	c, f := coordinator(t)
	w := seed(t, c.DB, "account", "approved_queued")
	f.hidden = true
	for i := 0; i < 3; i++ {
		if err := c.Step(context.Background()); !errors.Is(err, githubapp.ErrPublicationUncertain) {
			t.Fatal(err)
		}
		current, master := getWork(t, c.DB, w.ID)
		if current.State != "publishing_issues" || current.Error == "" || master != "" {
			t.Fatal("fake completion or hidden ambiguity")
		}
		if f.ops[i].ReconcileOnly != (i > 0) || f.ops[i].Key != f.ops[0].Key {
			t.Fatal("unsafe retry")
		}
		ready(t, c.DB)
	}
	if f.posts != 1 {
		t.Fatalf("POST repeated %d times", f.posts)
	}
	f.hidden = false
	if err := c.Step(context.Background()); err != nil {
		b, _ := json.MarshalIndent(f.calls, "", "  ")
		t.Fatal(err, string(b))
	}
	if f.posts != 1 {
		t.Fatal("reconciliation created another issue")
	}
	current, master := getWork(t, c.DB, w.ID)
	if current.Error != "" || master == "" {
		t.Fatal("recovered evidence missing")
	}
}

func TestCrashBeforeCallStillReconcileOnly(t *testing.T) {
	c, f := coordinator(t)
	seed(t, c.DB, "account", "approved_queued")
	task, err := c.claim(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if task.reconcile {
		t.Fatal("first claim reconciliation")
	}
	ready(t, c.DB)
	if err = c.Step(context.Background()); !errors.Is(err, githubapp.ErrPublicationUncertain) {
		t.Fatal(err)
	}
	if f.posts != 0 || len(f.ops) != 1 || !f.ops[0].ReconcileOnly {
		t.Fatal("crash repeated initial write")
	}
}

func TestConcurrentLeaseAndLateOwner(t *testing.T) {
	c, f := coordinator(t)
	seed(t, c.DB, "account", "approved_queued")
	entered, release := make(chan struct{}), make(chan struct{})
	f.before = func(_ intent, op githubapp.PublicationOperation) {
		if !op.ReconcileOnly {
			close(entered)
			<-release
		}
	}
	done := make(chan error, 1)
	go func() { done <- c.Step(context.Background()) }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("publication did not start")
	}
	second := &Coordinator{DB: c.DB, Client: f, Policy: c.Policy}
	for i := 0; i < 5; i++ {
		if err := second.Step(context.Background()); !errors.Is(err, sql.ErrNoRows) {
			t.Fatal("lease failed", err)
		}
	}
	// Expiring just the job cannot bypass the independent operation lease.
	mustExec(t, c.DB, `UPDATE factory_publication_jobs SET lease_until=now()-interval '1 second'`)
	if err := second.Step(context.Background()); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("operation lease bypassed", err)
	}
	ready(t, c.DB)
	if err := second.Step(context.Background()); !errors.Is(err, githubapp.ErrPublicationUncertain) {
		t.Fatal(err)
	}
	close(release)
	if err := <-done; !errors.Is(err, factory.ErrConflict) {
		t.Fatal("late owner accepted", err)
	}
	ready(t, c.DB)
	if err := second.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.posts != 1 {
		t.Fatalf("concurrent duplicate: %d", f.posts)
	}
}

func TestPolicyAccountAndStateSelection(t *testing.T) {
	c, f := coordinator(t)
	for _, state := range []string{"planning_queued", "plan_ready", "build_queued", "planning", "needs_clarification"} {
		seed(t, c.DB, "account", state)
	}
	seed(t, c.DB, "foreign", "approved_queued")
	if err := c.Step(context.Background()); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal(err)
	}
	w := seed(t, c.DB, "account", "approved_queued")
	for _, policy := range []Policy{{}, {AccountID: "account"}, {IssuesWrite: true}} {
		disabled := &Coordinator{DB: c.DB, Client: f, Policy: policy}
		if err := disabled.Step(context.Background()); !errors.Is(err, ErrPolicy) {
			t.Fatal(err)
		}
	}
	if len(f.calls) != 0 {
		t.Fatal("unauthorized network call")
	}
	if err := c.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	var account string
	if err := c.DB.QueryRow(`SELECT account_id FROM factory_publication_jobs WHERE work_id=$1`, w.ID).Scan(&account); err != nil || account != "account" {
		t.Fatal(err)
	}
}

func TestFixedErrorsBlockAndScrub(t *testing.T) {
	for _, failure := range []error{githubapp.ErrInvalidPublication, githubapp.ErrPublicationConflict, githubapp.ErrNotAllowed, &githubapp.APIError{StatusCode: 422}} {
		t.Run(failure.Error(), func(t *testing.T) {
			c, f := coordinator(t)
			w := seed(t, c.DB, "account", "approved_queued")
			f.failure = fmt.Errorf("secret-token private-body: %w", failure)
			if err := c.Step(context.Background()); !errors.Is(err, ErrBlocked) {
				t.Fatal(err)
			}
			current, _ := getWork(t, c.DB, w.ID)
			if current.Error == "" || strings.Contains(current.Error, "secret") {
				t.Fatal("unsafe error", current.Error)
			}
			ready(t, c.DB)
			if err := c.Step(context.Background()); !errors.Is(err, sql.ErrNoRows) {
				t.Fatal("silent retry", err)
			}
			if len(f.calls) != 1 {
				t.Fatal("fixed error retried")
			}
		})
	}
}

func TestApprovalAndSourceFences(t *testing.T) {
	for _, mutation := range []string{
		`document=jsonb_set(document,'{baseSha}','"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"')`,
		`document=jsonb_set(document,'{approvedPlanRevision}','2')`,
		`document=jsonb_set(document,'{plans,0,markdown}','"changed approved plan"')`,
		`revision=revision+1,document=jsonb_set(document,'{revision}','5')`,
		`document=jsonb_set(document,'{repositoryId}','"3"')`,
		`account_id='foreign'`,
	} {
		t.Run(mutation, func(t *testing.T) {
			c, f := coordinator(t)
			w := seed(t, c.DB, "account", "approved_queued")
			f.before = func(_ intent, _ githubapp.PublicationOperation) {
				mustExec(t, c.DB, `UPDATE factory_work_items SET `+mutation+` WHERE id=$1`, w.ID)
			}
			if err := c.Step(context.Background()); !errors.Is(err, ErrFence) {
				t.Fatal(err)
			}
			_, master := getWork(t, c.DB, w.ID)
			if master != "" {
				t.Fatal("late result updated changed work")
			}
			var state string
			if err := c.DB.QueryRow(`SELECT state FROM factory_publication_operations WHERE work_id=$1 AND ordinal=0`, w.ID).Scan(&state); err != nil || state != "succeeded" {
				t.Fatal("lost late outcome evidence", err)
			}
			ready(t, c.DB)
			if err := c.Step(context.Background()); !errors.Is(err, sql.ErrNoRows) {
				t.Fatal("fenced work retried", err)
			}
		})
	}
}

func TestChangedSnapshotBeforeNextOperationBlocks(t *testing.T) {
	c, f := coordinator(t)
	w := seed(t, c.DB, "account", "approved_queued")
	if err := c.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	mustExec(t, c.DB, `UPDATE factory_work_items SET document=jsonb_set(document,'{plans,0,markdown}','"edited"') WHERE id=$1`, w.ID)
	if err := c.Step(context.Background()); !errors.Is(err, ErrBlocked) {
		t.Fatal(err)
	}
	current, _ := getWork(t, c.DB, w.ID)
	if current.Error == "" || len(f.calls) != 1 {
		t.Fatal("changed approval published")
	}
}

func TestInvalidDurablePlanVisible(t *testing.T) {
	c, f := coordinator(t)
	w := seed(t, c.DB, "account", "approved_queued")
	mustExec(t, c.DB, `UPDATE factory_work_items SET document=jsonb_set(document,'{plans,0,features,1,dependsOn}','["consumer"]') WHERE id=$1`, w.ID)
	if err := c.Step(context.Background()); !errors.Is(err, ErrBlocked) {
		t.Fatal(err)
	}
	current, _ := getWork(t, c.DB, w.ID)
	if current.Error == "" || len(f.calls) != 0 {
		t.Fatal("invalid plan publication")
	}
}

func TestLostOutcomePersistenceReconciles(t *testing.T) {
	c, f := coordinator(t)
	w := seed(t, c.DB, "account", "publishing_issues")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.before = func(_ intent, _ githubapp.PublicationOperation) { cancel() }
	if err := c.Step(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	var state string
	if err := c.DB.QueryRow(`SELECT state FROM factory_publication_operations WHERE work_id=$1 AND ordinal=0`, w.ID).Scan(&state); err != nil || state != "attempted" {
		t.Fatal("lost durable attempt", state, err)
	}
	f.before = nil
	ready(t, c.DB)
	if err := c.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.posts != 1 || len(f.ops) != 2 || !f.ops[1].ReconcileOnly {
		t.Fatal("lost response repeated POST")
	}
	var id, number, attempts int
	if err := c.DB.QueryRow(`SELECT publication_id,issue_number,attempts FROM factory_publication_operations WHERE work_id=$1 AND ordinal=0`, w.ID).Scan(&id, &number, &attempts); err != nil || id != 1001 || number != 101 || attempts != 2 {
		t.Fatal("missing outcome metadata", id, number, attempts, err)
	}
}

func TestStoredIntentCannotCrossAccount(t *testing.T) {
	c, f := coordinator(t)
	seed(t, c.DB, "account", "approved_queued")
	if _, err := c.claim(context.Background()); err != nil {
		t.Fatal(err)
	}
	mustExec(t, c.DB, `UPDATE factory_publication_operations SET intent=jsonb_set(intent,'{Authority,AccountID}','"foreign"') WHERE ordinal=0`)
	ready(t, c.DB)
	if err := c.Step(context.Background()); !errors.Is(err, ErrBlocked) {
		t.Fatal(err)
	}
	if len(f.calls) != 0 {
		t.Fatal("stored intent crossed account binding")
	}
}

func TestMissingOperationsNeverRecreated(t *testing.T) {
	c, f := coordinator(t)
	w := seed(t, c.DB, "account", "approved_queued")
	if err := c.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	mustExec(t, c.DB, `DELETE FROM factory_publication_operations WHERE work_id=$1 AND ordinal=0`, w.ID)
	if err := c.Step(context.Background()); !errors.Is(err, ErrBlocked) {
		t.Fatal(err)
	}
	if f.posts != 1 {
		t.Fatal("deleted operation recreated external write")
	}
	current, _ := getWork(t, c.DB, w.ID)
	if current.Error == "" {
		t.Fatal("lost evidence was not visible")
	}
}

func TestReconciliationPolicyFailurePreservesUnknown(t *testing.T) {
	c, f := coordinator(t)
	w := seed(t, c.DB, "account", "approved_queued")
	f.hidden = true
	if err := c.Step(context.Background()); !errors.Is(err, githubapp.ErrPublicationUncertain) {
		t.Fatal(err)
	}
	f.failure = githubapp.ErrNotAllowed
	ready(t, c.DB)
	if err := c.Step(context.Background()); !errors.Is(err, ErrBlocked) {
		t.Fatal(err)
	}
	var jobState, operationState string
	if err := c.DB.QueryRow(`SELECT j.state,o.state FROM factory_publication_jobs j JOIN factory_publication_operations o USING(work_id) WHERE j.work_id=$1 AND o.ordinal=0`, w.ID).Scan(&jobState, &operationState); err != nil || jobState != "blocked" || operationState != "unknown" {
		t.Fatal("erased unresolved write", jobState, operationState, err)
	}
	current, _ := getWork(t, c.DB, w.ID)
	if !strings.Contains(current.Error, "unknown") {
		t.Fatal("ambiguity hidden", current.Error)
	}
	if f.posts != 1 {
		t.Fatal("reconciliation repeated POST")
	}
}
