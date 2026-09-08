package taskflowruntime

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/factory"
	"github.com/0xikarus/vmbox-service/internal/factory/resultinbox"
	"github.com/0xikarus/vmbox-service/internal/taskflow"
	"github.com/DATA-DOG/go-sqlmock"
)

type fixtureInbox struct {
	issues int
	body   []byte
	err    error
}

func fixtureCapability(account, work, attempt string) string {
	h := hmac.New(sha256.New, bytes.Repeat([]byte{9}, 32))
	h.Write([]byte("vmbox/resultinbox/capability/v1\x00"))
	for _, s := range []string{account, work, attempt} {
		var n [8]byte
		binary.BigEndian.PutUint64(n[:], uint64(len(s)))
		h.Write(n[:])
		h.Write([]byte(s))
	}
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}
func (i *fixtureInbox) Issue(context.Context, string, string, string) (string, error) {
	i.issues++
	return strings.Repeat("c", 43), i.err
}
func (i *fixtureInbox) Get(context.Context, string, string, string) ([]byte, error) {
	if i.body == nil {
		return nil, resultinbox.ErrNotFound
	}
	return i.body, i.err
}

func TestControllerLifecycleRecoveryAndObservation(t *testing.T) {
	ctx := context.Background()
	work, attempt := newID(), newID()
	box := "box-fixture"
	account := "account-fixture"
	var calls []string
	accepted := false
	changed := false
	connections := 0
	command := taskRoot + "/bin/vmbox-task-runner < " + taskRoot + "/attempts/" + attempt + "/job.json"
	process := v1.ProcessTask{ID: "task-fixture", LogicalBoxID: box, Agent: "shell", State: "running", Prompt: "exec " + command}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		if r.Header.Get("Authorization") != "Bearer controller-fixture-secret" {
			t.Error("controller binding")
			w.WriteHeader(401)
			return
		}
		var out any
		switch r.URL.Path {
		case "/v1/whoami":
			out = map[string]string{"accountId": account, "role": "owner"}
		case "/v1/logical-boxes":
			if r.Method == "GET" {
				out = []v1.LogicalBox{{ID: box, Name: "task-" + attempt, State: v1.LogicalBoxRunning}}
			} else {
				t.Error("unexpected creation")
				w.WriteHeader(500)
				return
			}
		case "/v1/logical-boxes/" + box + "/process-tasks":
			if r.Method == "GET" {
				out = []v1.ProcessTask{}
				if accepted {
					out = []v1.ProcessTask{process}
				}
			} else {
				if r.Header.Get("Idempotency-Key") != "task-run:"+attempt {
					t.Error("submission key")
				}
				var req v1.CreateBoxTaskRequest
				if json.NewDecoder(r.Body).Decode(&req) != nil || req.Agent != "shell" || req.Prompt != "exec "+command {
					t.Error("fixed wrapper command")
				}
				accepted = true
				out = process
			}
		case "/v1/logical-boxes/" + box + "/connection":
			connections++
			endpoint := "worker@example.test"
			if changed && connections%2 == 0 {
				endpoint = "other@example.test"
			}
			out = map[string]any{"logicalBoxId": box, "connection": map[string]any{"transport": "openssh", "endpoint": endpoint, "metadata": map[string]string{"deploymentInstanceId": "deployment-1"}}}
		case "/v1/process-tasks/task-fixture":
			out = process
		default:
			t.Errorf("unexpected route %s", r.URL.Path)
			w.WriteHeader(500)
			return
		}
		_ = json.NewEncoder(w).Encode(out)
	}))
	defer server.Close()
	inbox := &fixtureInbox{}
	stages := 0
	runner, e := New(Config{Controller: &factory.ControllerClient{URL: server.URL, Token: "controller-fixture-secret", AccountID: account, HTTP: server.Client()}, Inbox: inbox, CallbackURL: "https://callback.example.test/tasks/result", Stage: func(_ context.Context, in Input) error {
		stages++
		if in.AttemptID != attempt || strings.Contains(string(in.Job), "controller-fixture-secret") {
			t.Error("private staging identity")
		}
		var j Job
		if strictJSON(in.Job, &j) != nil || j.DeliveryToken != strings.Repeat("c", 43) || j.Request.Stage != "plan" || !strings.Contains(j.Request.Prompt, "Plan a family trip") {
			t.Error("typed private task job")
		}
		return nil
	}})
	if e != nil {
		t.Fatal(e)
	}
	input := taskflow.Input{AccountID: account, Workflow: taskflow.Workflow{ID: work, Agent: "codex", Profile: "saved", Idea: "Plan a family trip"}, Attempt: taskflow.Attempt{ID: attempt, Stage: "plan"}}
	sub, e := runner.Start(ctx, input)
	if e != nil || sub.BoxID != box || !sub.Pending || stages != 0 || inbox.issues != 0 {
		t.Fatalf("box persistence boundary %+v %v", sub, e)
	}
	input.Attempt.BoxID = box
	sub, e = runner.Start(ctx, input)
	if e != nil || sub.TaskID != process.ID || sub.Pending || stages != 1 || inbox.issues != 1 {
		t.Fatalf("submit %+v %v", sub, e)
	}
	// Recovery must work after credentials/grants expire and the box hibernates;
	// Find executes before Ensure, Issue, Connection, staging or submission.
	calls = nil
	inbox.err = fmt.Errorf("expired")
	recovered, e := runner.Start(ctx, input)
	if e != nil || recovered.TaskID != process.ID || inbox.issues != 1 || stages != 1 {
		t.Fatal("recovery restaged or reissued", e)
	}
	if strings.Join(calls, "|") != "GET /v1/whoami|GET /v1/logical-boxes/"+box+"/process-tasks" {
		t.Fatal(calls)
	}
	input.Attempt.TaskID = process.ID
	obs, e := runner.Observe(ctx, input)
	if e != nil || obs.Finished || obs.State != "running" {
		t.Fatal(obs, e)
	}
	process.State = "exited"
	inbox.err = nil
	obs, e = runner.Observe(ctx, input)
	if e != nil || !obs.Finished || obs.State != "result_missing" || obs.ExitCode != nil {
		t.Fatal("missing evidence fabricated or nonterminal", obs, e)
	}
	zero := 0
	doc, _ := json.Marshal(Report{AgentEvidence: true, Result: taskflow.Result{Text: "Clarify budget", Plan: &taskflow.Plan{Revision: 1, Summary: "Need budget", Questions: []string{"Budget?"}, Assignments: []taskflow.Assignment{}}}})
	inbox.body, _ = json.Marshal(resultinbox.Result{Version: 1, AttemptID: attempt, ExitCode: &zero, Document: doc})
	obs, e = runner.Observe(ctx, input)
	if e != nil || !obs.Finished || obs.Failure != "" || obs.Result.Plan == nil {
		t.Fatal(obs, e)
	}
	// A new deployment assignment during staging cannot receive an unstaged launch.
	accepted = false
	input.Attempt.TaskID = ""
	changed = true
	connections = 0
	if _, e = runner.Start(ctx, input); e == nil {
		t.Fatal("changed assignment submitted")
	}
	if accepted {
		t.Fatal("submitted unstaged deployment")
	}
}

func TestDedicatedInboxTLSAccountWorkAttemptScope(t *testing.T) {
	db, mock, e := sqlmock.New()
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	inbox, e := resultinbox.New(db, bytes.Repeat([]byte{9}, 32), 0)
	if e != nil {
		t.Fatal(e)
	}
	mock.ExpectExec("CREATE TABLE IF NOT EXISTS factory_result_inbox_v1").WillReturnResult(sqlmock.NewResult(0, 0))
	if e = inbox.Migrate(context.Background()); e != nil {
		t.Fatal(e)
	}
	account, work, attempt := "new-account", newID(), newID()
	// Issue uses only the independent scoped inbox table, with no factory work FK.
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO factory_result_inbox_v1").WithArgs(account, work, attempt, sqlmock.AnyArg(), int64(resultinbox.DefaultTTL/time.Microsecond)).WillReturnResult(sqlmock.NewResult(0, 1))
	// Compute the exact grant once through a permissive argument matcher that
	// captures the INSERT hash, then SELECT returns the same hash.
	// The deterministic capability is obtained from a fixture-equivalent HMAC.
	token := fixtureCapability(account, work, attempt)
	hash := sha256.Sum256([]byte(token))
	mock.ExpectQuery("SELECT capability_hash, expires_at").WithArgs(account, work, attempt).WillReturnRows(sqlmock.NewRows([]string{"capability_hash", "expires_at"}).AddRow(hash[:], time.Now().Add(time.Minute)))
	mock.ExpectQuery("SELECT .*timestamptz > clock_timestamp").WithArgs(sqlmock.AnyArg()).WillReturnRows(sqlmock.NewRows([]string{"alive"}).AddRow(true))
	mock.ExpectCommit()
	got, e := inbox.Issue(context.Background(), account, work, attempt)
	if e != nil || got != token {
		t.Fatal("issue", e)
	}
	server := httptest.NewTLSServer(inbox.Handler())
	defer server.Close()
	zero := 0
	body, _ := json.Marshal(resultinbox.Result{Version: 1, AttemptID: attempt, ExitCode: &zero, Document: json.RawMessage(`{"result":{"text":"actual fixture output"}}`)})
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT attempt_id,body,expires_at").WithArgs(hash[:]).WillReturnRows(sqlmock.NewRows([]string{"attempt_id", "body", "alive"}).AddRow(attempt, nil, true))
	mock.ExpectExec("UPDATE factory_result_inbox_v1").WithArgs(hash[:], body).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	req, _ := http.NewRequest("POST", server.URL+"/result", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	response, e := server.Client().Do(req)
	if e != nil {
		t.Fatal(e)
	}
	response.Body.Close()
	if response.StatusCode != 204 {
		t.Fatal(response.StatusCode)
	}
	mock.ExpectQuery("SELECT body FROM factory_result_inbox_v1").WithArgs(account, work, attempt).WillReturnRows(sqlmock.NewRows([]string{"body"}).AddRow(body))
	read, e := inbox.Get(context.Background(), account, work, attempt)
	if e != nil || !bytes.Equal(read, body) {
		t.Fatal(e)
	}
	// A capability for this attempt cannot deliver a sibling's document.
	foreign := bytes.Replace(body, []byte(attempt), []byte(newID()), 1)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT attempt_id,body,expires_at").WithArgs(hash[:]).WillReturnRows(sqlmock.NewRows([]string{"attempt_id", "body", "alive"}).AddRow(attempt, body, true))
	mock.ExpectRollback()
	req, _ = http.NewRequest("POST", server.URL+"/result", bytes.NewReader(foreign))
	req.Header.Set("Authorization", "Bearer "+token)
	response, e = server.Client().Do(req)
	if e != nil {
		t.Fatal(e)
	}
	io.Copy(io.Discard, response.Body)
	response.Body.Close()
	if response.StatusCode != 400 {
		t.Fatal("foreign attempt accepted", response.StatusCode)
	}
	if e = mock.ExpectationsWereMet(); e != nil {
		t.Fatal(e)
	}
}
