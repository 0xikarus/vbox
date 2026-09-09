package taskflow

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

type boundaryRunner struct {
	mu           sync.Mutex
	starts       []Input
	observations []Input
	plan         Plan
	verdict      string
	failWork     string
	uncertain    bool
	observeError bool
	running      bool
	missing      bool
}

func (f *boundaryRunner) Start(_ context.Context, in Input) (Submission, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.starts = append(f.starts, in)
	if f.uncertain {
		f.uncertain = false
		return Submission{}, errors.New("connection lost after submit")
	}
	return Submission{BoxID: "box-" + in.Attempt.ID, TaskID: "task-" + in.Attempt.ID, State: "submitted"}, nil
}
func (f *boundaryRunner) Observe(_ context.Context, in Input) (Observation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.observations = append(f.observations, in)
	if f.observeError {
		return Observation{}, errors.New("controller timeout")
	}
	if f.running {
		return Observation{State: "running"}, nil
	}
	code := 0
	o := Observation{Finished: true, ExitCode: &code}
	switch in.Attempt.Stage {
	case "plan":
		p := f.plan
		o.Result = Result{Plan: &p}
	case "work":
		o.Result.Text = "actual output for " + in.Attempt.AssignmentID
		if in.Attempt.AssignmentID == f.failWork {
			code = 7
			o.Failure = "worker failed"
		}
	case "synthesize":
		o.Result = Result{Text: "actual coordinator synthesis", Verdict: f.verdict}
	}
	if f.missing {
		o.Result = Result{}
	}
	return o, nil
}
func testPlan() Plan {
	return Plan{Summary: "Research and combine findings", Questions: []string{}, Assignments: []Assignment{
		{ID: "a", Title: "Research A", Instruction: "Investigate first topic", AcceptanceCriteria: []string{"Cite findings"}, DependsOn: []string{}},
		{ID: "b", Title: "Research B", Instruction: "Investigate second topic", AcceptanceCriteria: []string{"Cite findings"}, DependsOn: []string{}},
		{ID: "c", Title: "Compare", Instruction: "Compare both findings", AcceptanceCriteria: []string{"Explain differences"}, DependsOn: []string{"a", "b"}},
	}}
}
func setup(t *testing.T) (*Service, *boundaryRunner) {
	t.Helper()
	dsn := os.Getenv("VMBOX_FACTORY_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("private PostgreSQL required; use scripts/test-factory-in-box.sh bootstrap")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	schema := "taskflow_test_" + newID()
	if _, err = db.Exec(`CREATE SCHEMA ` + schema); err != nil {
		t.Fatal(err)
	}
	// Every connection uses this private test schema, including concurrent Steps.
	separator := "?"
	if strings.Contains(dsn, "?") {
		separator = "&"
	}
	scoped, err := sql.Open("pgx", dsn+separator+"search_path="+schema)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { scoped.Close(); db.Exec(`DROP SCHEMA ` + schema + ` CASCADE`); db.Close() })
	store := &Store{DB: scoped}
	if err = store.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = store.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	runner := &boundaryRunner{plan: testPlan(), verdict: "accepted"}
	s := &Service{Store: store, Runner: runner, GatewayToken: "test-gateway", MaxWorkers: 6, Images: map[string]bool{"codex": true}}
	s.Profiles = func(ctx context.Context, a string) ([]Profile, error) {
		// Queries inside callbacks catch transactions held across network boundaries
		// when the test constrains the pool to one connection.
		var n int
		if err := scoped.QueryRowContext(ctx, `SELECT 1`).Scan(&n); err != nil {
			return nil, err
		}
		return []Profile{{Application: "codex", Name: "saved"}}, nil
	}
	s.ValidateAssets = func(ctx context.Context, a string, ids []string) error {
		var n int
		if err := scoped.QueryRowContext(ctx, `SELECT 1`).Scan(&n); err != nil {
			return err
		}
		for _, id := range ids {
			if id != "asset-"+a {
				return errors.New("foreign asset")
			}
		}
		return nil
	}
	return s, runner
}
func api(t *testing.T, s *Service, account, method, path, key string, body any, status int) Workflow {
	t.Helper()
	b, _ := json.Marshal(body)
	r := httptest.NewRequest(method, "/v1/factory/tasks"+path, bytes.NewReader(b))
	r.Header.Set("Authorization", "Bearer test-gateway")
	r.Header.Set("X-Vmbox-Account", account)
	r.Header.Set("X-Vmbox-User", "user")
	r.Header.Set("Idempotency-Key", key)
	out := httptest.NewRecorder()
	s.Handler().ServeHTTP(out, r)
	if out.Code != status {
		t.Fatalf("%s %s status=%d want=%d body=%s", method, path, out.Code, status, out.Body.String())
	}
	var w Workflow
	if status == 200 && path != "/capabilities" {
		if err := json.Unmarshal(out.Body.Bytes(), &w); err != nil {
			t.Fatal(err)
		}
	}
	return w
}
func createTask(t *testing.T, s *Service, account, key string) Workflow {
	return api(t, s, account, "POST", "", key, Create{Idea: "Compare research findings without a repository", Agent: "codex", Profile: "saved", AssetIDs: []string{"asset-" + account}, MaxWorkers: 3}, 200)
}
func get(t *testing.T, s *Service, w Workflow) Workflow {
	t.Helper()
	v, err := s.Store.Get(context.Background(), "a", w.ID)
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func step(t *testing.T, s *Service) {
	t.Helper()
	if err := s.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
}
func until(t *testing.T, s *Service, w Workflow, state string) Workflow {
	t.Helper()
	for i := 0; i < 50; i++ {
		v := get(t, s, w)
		if v.State == state {
			return v
		}
		step(t, s)
	}
	t.Fatalf("never reached %s: %+v", state, get(t, s, w))
	return Workflow{}
}
func runTask(t *testing.T, s *Service, w Workflow) Workflow {
	t.Helper()
	w = until(t, s, w, "awaiting_approval")
	return api(t, s, "a", "POST", "/"+w.ID+"/run", newID(), map[string]any{"version": w.Version, "planRevision": w.Plans[len(w.Plans)-1].Revision}, 200)
}

func TestPostgresCompleteGeneralTaskFlow(t *testing.T) {
	s, f := setup(t)
	s.Store.DB.SetMaxOpenConns(1)
	w := createTask(t, s, "a", "create")
	w = runTask(t, s, w)
	w = until(t, s, w, "completed")
	if w.Final != "actual coordinator synthesis" || w.Verdict != "accepted" || len(w.Attempts) != 5 || len(w.Plans) != 1 {
		t.Fatalf("bad final workflow: %+v", w)
	}
	for _, in := range f.starts {
		if in.AccountID != "a" {
			t.Fatal("account lost")
		}
		if in.Attempt.Stage == "work" && in.Attempt.AssignmentID == "c" {
			for _, a := range in.Workflow.Attempts {
				if a.Stage == "work" && a.AssignmentID != "c" && !successful(a) {
					t.Fatal("dependency dispatched early")
				}
			}
		}
		if in.Attempt.Stage == "synthesize" {
			for _, a := range in.Workflow.Attempts {
				if a.Stage == "work" && !strings.HasPrefix(a.Output, "actual output") {
					t.Fatal("synthesis missing real worker result")
				}
			}
		}
	}
	oldAttempts := append([]Attempt{}, w.Attempts...)
	w = api(t, s, "a", "POST", "/"+w.ID+"/messages", "reply", map[string]any{"version": w.Version, "text": "Now investigate the follow-up"}, 200)
	w = runTask(t, s, w)
	w = until(t, s, w, "completed")
	if len(w.Plans) != 2 || w.Plans[1].Revision != 2 || len(w.Attempts) != 10 || len(w.Messages) != 6 {
		t.Fatalf("lost revision history: %+v", w)
	}
	for i, a := range oldAttempts {
		if fingerprint(a) != fingerprint(w.Attempts[i]) {
			t.Fatal("terminal history rewritten")
		}
	}
}

func TestPostgresQuestionsApprovalAndValidation(t *testing.T) {
	s, f := setup(t)
	f.plan.Questions = []string{"Which period?"}
	w := until(t, s, createTask(t, s, "a", "create"), "awaiting_reply")
	api(t, s, "a", "POST", "/"+w.ID+"/run", "premature", map[string]any{"version": w.Version, "planRevision": 1}, 409)
	w = api(t, s, "a", "POST", "/"+w.ID+"/messages", "answer", map[string]any{"version": w.Version, "text": "Last year"}, 200)
	f.plan = testPlan()
	w = until(t, s, w, "awaiting_approval")
	api(t, s, "a", "POST", "/"+w.ID+"/run", "stale-plan", map[string]any{"version": w.Version, "planRevision": 1}, 409)
	api(t, s, "a", "POST", "/"+w.ID+"/run", "stale-version", map[string]any{"version": w.Version - 1, "planRevision": 2}, 409)
	w = runTask(t, s, w)
	api(t, s, "a", "POST", "/"+w.ID+"/messages", "running-reply", map[string]any{"version": w.Version, "text": "Change running work"}, 409)
	for name, change := range map[string]func(*Plan){"criteria": func(p *Plan) { p.Assignments[0].AcceptanceCriteria = nil }, "duplicate": func(p *Plan) { p.Assignments[1].ID = "a" }, "cycle": func(p *Plan) { p.Assignments[0].DependsOn = []string{"c"} }, "unknown": func(p *Plan) { p.Assignments[0].DependsOn = []string{"unknown"} }, "empty": func(p *Plan) { p.Assignments = nil }, "bounds": func(p *Plan) {
		for len(p.Assignments) < 21 {
			p.Assignments = append(p.Assignments, p.Assignments[0])
		}
	}} {
		t.Run(name, func(t *testing.T) {
			p := testPlan()
			change(&p)
			if validatePlan(p, true) == nil {
				t.Fatal("accepted invalid plan")
			}
		})
	}
}

func TestPostgresFailureRetryAndSemanticVerdict(t *testing.T) {
	s, f := setup(t)
	f.failWork = "a"
	f.verdict = "needs_revision"
	w := runTask(t, s, createTask(t, s, "a", "create"))
	w = until(t, s, w, "needs_revision")
	var failed Attempt
	for _, a := range w.Attempts {
		if a.AssignmentID == "a" {
			failed = a
		}
		if a.AssignmentID == "c" && a.State != "queued" {
			t.Fatal("failed dependency dispatched")
		}
	}
	if failed.ExitCode == nil || *failed.ExitCode != 7 || w.Verdict != "needs_revision" {
		t.Fatal("exit/verdict not preserved")
	}
	synth := f.starts[len(f.starts)-1]
	found := false
	for _, a := range synth.Workflow.Attempts {
		if a.ID == failed.ID && a.Failure != "" {
			found = true
		}
	}
	if !found {
		t.Fatal("synthesis missing failed attempt")
	}
	f.failWork = ""
	f.verdict = "accepted"
	w = api(t, s, "a", "POST", "/"+w.ID+"/retry", "retry", map[string]any{"version": w.Version, "attemptId": failed.ID}, 200)
	w = until(t, s, w, "completed")
	if len(w.Attempts) != 7 {
		t.Fatalf("retry lost history: %+v", w.Attempts)
	}
	api(t, s, "a", "POST", "/"+w.ID+"/retry", "old-retry", map[string]any{"version": w.Version, "attemptId": failed.ID}, 409)
	for _, a := range w.Attempts {
		if a.ID == failed.ID && fingerprint(a) != fingerprint(failed) {
			t.Fatal("failed history mutated")
		}
	}
	// A successful process without a coordinator verdict cannot complete a task.
	s2, f2 := setup(t)
	f2.verdict = ""
	w2 := runTask(t, s2, createTask(t, s2, "a", "create"))
	w2 = until(t, s2, w2, "failed")
	if w2.Verdict != "" || w2.Final != "" || w2.Attempts[len(w2.Attempts)-1].ExitCode == nil || *w2.Attempts[len(w2.Attempts)-1].ExitCode != 0 {
		t.Fatal("semantic failure confused with process exit")
	}
}

func TestPostgresRecoveryObservationAndCancellation(t *testing.T) {
	s, f := setup(t)
	f.uncertain = true
	w := createTask(t, s, "a", "create")
	if s.Step(context.Background()) == nil {
		t.Fatal("expected submission uncertainty")
	}
	w = get(t, s, w)
	id := w.Attempts[0].ID
	// A fresh Service must recover the exact same accepted runtime identity.
	restarted := *s
	s = &restarted
	step(t, s)
	if len(f.starts) != 2 || f.starts[0].Attempt.ID != f.starts[1].Attempt.ID {
		t.Fatal("replayed uncertain task with new identity")
	}
	f.observeError = true
	if s.Step(context.Background()) == nil {
		t.Fatal("expected observation timeout")
	}
	w = get(t, s, w)
	if w.Attempts[0].ID != id || terminal(w.Attempts[0]) {
		t.Fatal("timeout made task terminal")
	}
	w = api(t, s, "a", "POST", "/"+w.ID+"/cancel", "cancel", map[string]any{"version": w.Version}, 200)
	if w.State != "cancelling" {
		t.Fatal("pretend cancellation")
	}
	f.observeError = false
	f.running = true
	step(t, s)
	w = get(t, s, w)
	if w.State != "cancelling" {
		t.Fatal("running process ignored")
	}
	f.running = false
	w = until(t, s, w, "cancelled")
	if w.Attempts[0].ExitCode == nil || len(f.starts) != 2 {
		t.Fatal("cancellation lost terminal observation or dispatched new work")
	}
	w2 := createTask(t, s, "a", "queued-cancel")
	w2 = api(t, s, "a", "POST", "/"+w2.ID+"/cancel", "cancel-queued", map[string]any{"version": w2.Version}, 200)
	if w2.State != "cancelled" {
		t.Fatal("queued cancellation failed")
	}
	step(t, s)
	if len(f.starts) != 2 {
		t.Fatal("dispatch after cancellation")
	}
}

func TestPostgresIdempotencyAccountsAndStrictHTTP(t *testing.T) {
	s, _ := setup(t)
	w := createTask(t, s, "a", "same")
	replay := createTask(t, s, "a", "same")
	if fingerprint(w) != fingerprint(replay) {
		t.Fatal("create not idempotent")
	}
	other := createTask(t, s, "b", "same")
	if other.ID == w.ID {
		t.Fatal("account collision")
	}
	api(t, s, "b", "GET", "/"+w.ID, "", nil, 404)
	api(t, s, "b", "POST", "/"+w.ID+"/cancel", "cross", map[string]any{"version": w.Version}, 404)
	api(t, s, "a", "POST", "", "same", Create{Idea: "Different", Agent: "codex", Profile: "saved", MaxWorkers: 1}, 409)
	cancelled := api(t, s, "a", "POST", "/"+w.ID+"/cancel", "cancel", map[string]any{"version": w.Version}, 200)
	again := api(t, s, "a", "POST", "/"+w.ID+"/cancel", "cancel", map[string]any{"version": w.Version}, 200)
	if fingerprint(cancelled) != fingerprint(again) {
		t.Fatal("mutation not idempotent")
	}
	api(t, s, "a", "POST", "", "", Create{}, 400)
	for _, body := range []any{map[string]any{"idea": "x", "accountId": "b"}, Create{Idea: "x", Agent: "codex", Profile: "unknown", MaxWorkers: 1}, Create{Idea: "x", Agent: "codex", Profile: "saved", MaxWorkers: 7}, Create{Idea: "x", Agent: "codex", Profile: "saved", MaxWorkers: 1, AssetIDs: []string{"asset-b"}}, Create{Idea: "x", Agent: "codex", Profile: "saved", MaxWorkers: 1, AssetIDs: []string{"asset-a", "asset-a"}}} {
		api(t, s, "a", "POST", "", newID(), body, 400)
	}
	r := httptest.NewRequest(http.MethodGet, "/v1/factory/tasks", nil)
	out := httptest.NewRecorder()
	s.Handler().ServeHTTP(out, r)
	if out.Code != 401 {
		t.Fatal("missing auth accepted")
	}
	list, err := s.Store.List(context.Background(), "a")
	if err != nil || len(list) != 1 {
		t.Fatalf("account list: %v %v", list, err)
	}
}

func TestPostgresLeasesAdmissionAndValidationBeforeDispatch(t *testing.T) {
	s, f := setup(t)
	// The callback can use the only DB connection: no transaction spans it.
	s.Store.DB.SetMaxOpenConns(1)
	w := createTask(t, s, "a", "create")
	s.ValidateAssets = func(context.Context, string, []string) error { return errors.New("attachment deleted") }
	step(t, s)
	w = get(t, s, w)
	if len(f.starts) != 0 || w.State != "failed" {
		t.Fatal("invalid attachment dispatched")
	}
	s.ValidateAssets = nil
	// New tasks without attachments exercise cross-account admission.
	for i := 0; i < 8; i++ {
		api(t, s, fmt.Sprint(i), "POST", "", newID(), Create{Idea: "General task", Agent: "codex", Profile: "saved", MaxWorkers: 1}, 200)
	}
	f.running = true
	for i := 0; i < 12; i++ {
		step(t, s)
	}
	var count int
	if err := s.Store.DB.QueryRow(ActiveCountSQL).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 6 || len(f.starts) != 6 {
		t.Fatalf("global admission count=%d starts=%d", count, len(f.starts))
	}
	// Recover expired leases but reject late writes from the former owner.
	c, err := s.claim(context.Background())
	if err != nil || c.attempt == "" {
		t.Fatalf("claim: %+v %v", c, err)
	}
	if _, err = s.Store.DB.Exec(`UPDATE general_tasks SET lease_until=clock_timestamp()-interval '1 second' WHERE account_id=$1 AND id=$2`, c.account, c.id); err != nil {
		t.Fatal(err)
	}
	if _, err = s.leased(context.Background(), c, func(*document) error { return nil }); !errors.Is(err, ErrConflict) {
		t.Fatalf("expired lease accepted: %v", err)
	}
	for i := 0; i < 8; i++ {
		step(t, s)
	}
	if len(f.starts) != 6 {
		t.Fatal("lease expiry released capacity or replayed task")
	}
}

func TestPostgresAdmissionIncludesExistingFactory(t *testing.T) {
	s, f := setup(t)
	_, err := s.Store.DB.Exec(`CREATE TABLE factory_work_items(state text); INSERT INTO factory_work_items VALUES ('planning'),('planning'); CREATE TABLE factory_execution_graphs(document jsonb); INSERT INTO factory_execution_graphs VALUES ('{"nodes":[{"state":"building"},{"state":"verifying"},{"state":"reviewing"},{"state":"building"}]}')`)
	if err != nil {
		t.Fatal(err)
	}
	createTask(t, s, "a", "create")
	step(t, s)
	if len(f.starts) != 0 {
		t.Fatal("existing Factory work not counted")
	}
	if _, err = s.Store.DB.Exec(`DELETE FROM factory_work_items`); err != nil {
		t.Fatal(err)
	}
	step(t, s)
	if len(f.starts) != 1 {
		t.Fatal("capacity not released")
	}
}

func TestPostgresConcurrentCreateAndSteps(t *testing.T) {
	s, f := setup(t)
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); createTask(t, s, "a", "same") }()
	}
	wg.Wait()
	list, err := s.Store.List(context.Background(), "a")
	if err != nil || len(list) != 1 {
		t.Fatalf("duplicate concurrent creates: %v %v", list, err)
	}
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); step(t, s) }()
	}
	wg.Wait()
	if len(f.starts) != 1 {
		t.Fatalf("concurrent scheduler duplicated dispatch: %d", len(f.starts))
	}
}

func TestPostgresMissingResultDoesNotReplay(t *testing.T) {
	s, f := setup(t)
	f.missing = true
	w := until(t, s, createTask(t, s, "a", "create"), "failed")
	if w.Attempts[0].State != "result_missing" || w.Attempts[0].ExitCode == nil {
		t.Fatal("missing result lost actual exit")
	}
	api(t, s, "a", "POST", "/"+w.ID+"/retry", "retry-plan", map[string]any{"version": w.Version, "attemptId": w.Attempts[0].ID}, 409)
	step(t, s)
	if len(f.starts) != 1 || len(get(t, s, w).Attempts) != 1 {
		t.Fatal("missing result replayed the process")
	}
}

func TestPostgresDispatchSnapshotSurvivesPolling(t *testing.T) {
	s, f := setup(t)
	f.uncertain = true
	createTask(t, s, "a", "stable")
	if err := s.Step(context.Background()); err == nil {
		t.Fatal("expected lost submission response")
	}
	step(t, s)
	if len(f.starts) != 2 || f.starts[0].Attempt.ID != f.starts[1].Attempt.ID {
		t.Fatal("did not recover same attempt")
	}
	if fingerprint(f.starts[0].Workflow) != fingerprint(f.starts[1].Workflow) {
		t.Fatal("polling changed private staging input")
	}
}

func TestPostgresGitHubProfilePersistsAndReachesEveryStage(t *testing.T) {
	s, f := setup(t)
	s.Profiles = func(_ context.Context, account string) ([]Profile, error) {
		return []Profile{{Application: "codex", Name: "saved"}, {Application: "github", Name: "team-" + account}}, nil
	}
	input := Create{Idea: "Review repository information", Agent: "codex", Profile: "saved", GitHubProfile: "team-a", MaxWorkers: 2}
	api(t, s, "b", "POST", "", "foreign", input, 400)
	bad := input
	bad.GitHubProfile = "saved"
	api(t, s, "a", "POST", "", "wrong-application", bad, 400)
	w := api(t, s, "a", "POST", "", "github", input, 200)
	bad.GitHubProfile = ""
	api(t, s, "a", "POST", "", "github", bad, 409)
	w = until(t, s, runTask(t, s, w), "completed")
	if w.GitHubProfile != "team-a" || selection(w).GitHubProfile != "team-a" {
		t.Fatal("profile not persisted")
	}
	stages := map[string]bool{}
	for _, in := range f.starts {
		if in.Workflow.GitHubProfile != "team-a" {
			t.Fatal("stage lost selected GitHub profile")
		}
		stages[in.Attempt.Stage] = true
	}
	if !stages["plan"] || !stages["work"] || !stages["synthesize"] {
		t.Fatal("missing stage coverage")
	}
	w = api(t, s, "a", "POST", "/"+w.ID+"/messages", "refine-github", map[string]any{"version": w.Version, "text": "Refine the results"}, 200)
	if w.GitHubProfile != "team-a" {
		t.Fatal("refinement lost profile")
	}
}

func TestPostgresCancelUncertainAndDependencyQueue(t *testing.T) {
	s, f := setup(t)
	f.uncertain = true
	w := createTask(t, s, "a", "create")
	if s.Step(context.Background()) == nil {
		t.Fatal("expected ambiguity")
	}
	w = get(t, s, w)
	w = api(t, s, "a", "POST", "/"+w.ID+"/cancel", "cancel", map[string]any{"version": w.Version}, 200)
	w = until(t, s, w, "cancelled")
	if len(f.starts) != 1 || w.Attempts[0].ExitCode == nil {
		t.Fatal("cancelled ambiguous submission started again or lost observation")
	}
	w = runTask(t, s, createTask(t, s, "a", "second"))
	step(t, s)
	w = get(t, s, w)
	w = api(t, s, "a", "POST", "/"+w.ID+"/cancel", "cancel-workers", map[string]any{"version": w.Version}, 200)
	before := len(f.starts)
	w = until(t, s, w, "cancelled")
	if len(f.starts) != before {
		t.Fatal("cancel dispatched dependency-queued workers")
	}
}

type pausedRunner struct {
	Runner
	entered chan struct{}
	resume  chan struct{}
	db      *sql.DB
}

func (p pausedRunner) Start(ctx context.Context, in Input) (Submission, error) {
	var n int
	if err := p.db.QueryRowContext(ctx, `SELECT 1`).Scan(&n); err != nil {
		return Submission{}, err
	}
	close(p.entered)
	select {
	case <-ctx.Done():
		return Submission{}, ctx.Err()
	case <-p.resume:
		return p.Runner.Start(ctx, in)
	}
}
func TestPostgresLeaseRenewalAndCancelDuringNetwork(t *testing.T) {
	s, f := setup(t)
	s.Store.DB.SetMaxOpenConns(1)
	w := createTask(t, s, "a", "create")
	p := pausedRunner{Runner: f, entered: make(chan struct{}), resume: make(chan struct{}), db: s.Store.DB}
	s.Runner = p
	finished := make(chan error, 1)
	go func() { finished <- s.Step(context.Background()) }()
	select {
	case <-p.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("runner callback blocked by transaction")
	}
	// Shorten the persisted lease so its renewal is directly observable.
	if _, err := s.Store.DB.Exec(`UPDATE general_tasks SET lease_until=clock_timestamp()+interval '20 seconds' WHERE account_id='a' AND id=$1`, w.ID); err != nil {
		t.Fatal(err)
	}
	time.Sleep(17 * time.Second)
	var remaining float64
	if err := s.Store.DB.QueryRow(`SELECT extract(epoch from lease_until-clock_timestamp()) FROM general_tasks WHERE account_id='a' AND id=$1`, w.ID).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining < 40 {
		t.Fatalf("lease was not renewed: %.1f seconds", remaining)
	}
	w = get(t, s, w)
	w = api(t, s, "a", "POST", "/"+w.ID+"/cancel", "cancel", map[string]any{"version": w.Version}, 200)
	close(p.resume)
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	w = get(t, s, w)
	if w.State != "cancelling" || w.Attempts[0].TaskID == "" {
		t.Fatal("late submission overwrote cancel or lost receipt")
	}
	w = until(t, s, w, "cancelled")
	if len(f.starts) != 1 || w.Attempts[0].ExitCode == nil {
		t.Fatal("cancelled in-flight work not observed")
	}
}

func TestPostgresConcurrentGlobalAdmission(t *testing.T) {
	s, f := setup(t)
	f.running = true
	for i := 0; i < 12; i++ {
		createTask(t, s, fmt.Sprint(i), "create")
	}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); step(t, s) }()
	}
	wg.Wait()
	var count int
	if err := s.Store.DB.QueryRow(ActiveCountSQL).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 6 || len(f.starts) != 6 {
		t.Fatalf("concurrent admission: reservations=%d starts=%d", count, len(f.starts))
	}
}
