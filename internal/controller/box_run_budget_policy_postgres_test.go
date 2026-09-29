package controller

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestBoxRunBudgetPolicyPostgres(t *testing.T) {
	dsn := os.Getenv("VMBOX_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires disposable PostgreSQL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	store, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	store.DB.SetMaxOpenConns(1)
	schema := "run_budget_test_" + strings.ReplaceAll(uuid(), "-", "")
	if _, err := store.DB.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer store.DB.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
	if _, err := store.DB.ExecContext(ctx, "SET search_path TO "+schema); err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	owner, err := store.Bootstrap(ctx, "run-budget-test", "owner", uuid())
	if err != nil {
		t.Fatal(err)
	}
	boxID := uuid()
	slotID := uuid()
	if _, err := store.DB.ExecContext(ctx, `INSERT INTO compute_slots(id,account_id,provider,ordinal,state,health,fencing_token)
		VALUES($1,$2,'railway',1,'occupied','healthy','fixture-fence')`, slotID, owner.AccountID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.ExecContext(ctx, `INSERT INTO logical_boxes(id,account_id,owner_user_id,name,provider,state,volume_id,volume_name,slot_id,fencing_token)
		VALUES($1,$2,$3,'fixture','railway','running','fixture-volume','fixture-volume',$4,'fixture-fence')`, boxID, owner.AccountID, owner.UserID, slotID); err != nil {
		t.Fatal(err)
	}
	started := time.Now().UTC().Add(-90 * time.Minute)
	if _, err := store.DB.ExecContext(ctx, `INSERT INTO allocation_requests(id,account_id,logical_box_id,state,idempotency_key,requested_by,slot_id,assignment_generation,updated_at)
		VALUES($1,$2,$3,'ready',$4,$5,$6,0,$7)`, uuid(), owner.AccountID, boxID, uuid(), owner.UserID, slotID, started); err != nil {
		t.Fatal(err)
	}
	get := httptest.NewRequest(http.MethodGet, "/v1/logical-boxes/"+boxID+"/run-budget-policy", nil)
	get.SetPathValue("id", boxID)
	getResponse := httptest.NewRecorder()
	(&Server{Store: store}).boxRunBudgetPolicy(getResponse, get, owner)
	var view boxRunBudgetPolicyResponse
	if err := json.Unmarshal(getResponse.Body.Bytes(), &view); getResponse.Code != http.StatusOK || err != nil || view.RunningSince == nil || view.RunningSince.Sub(started) > time.Second || started.Sub(*view.RunningSince) > time.Second {
		t.Fatalf("running since: status=%d body=%s error=%v", getResponse.Code, getResponse.Body.String(), err)
	}
	budget, err := store.syncAgentRunBudget(ctx, owner.AccountID, boxID, 8*time.Hour)
	if err != nil || budget.BudgetSeconds != 8*3600 || budget.DeadlineAt == nil {
		t.Fatalf("default budget=%+v error=%v", budget, err)
	}
	runBudgetPrincipal := owner
	runBudgetPrincipal.Subject = "controller:run-budget"
	if err := (&Server{Store: store}).resumeLogicalBoxHibernate(ctx, runBudgetPrincipal, boxID); err == nil || !strings.Contains(err.Error(), "run-time limit changed") {
		t.Fatalf("non-expired run budget should not hibernate the box: %v", err)
	}
	var state string
	if err := store.DB.QueryRowContext(ctx, `SELECT state FROM logical_boxes WHERE id=$1`, boxID).Scan(&state); err != nil || state != "running" {
		t.Fatalf("state after rejected hibernation=%q error=%v", state, err)
	}
	request := httptest.NewRequest(http.MethodPut, "/v1/logical-boxes/"+boxID+"/run-budget-policy", strings.NewReader(`{"seconds":14400}`))
	request.SetPathValue("id", boxID)
	response := httptest.NewRecorder()
	(&Server{Store: store}).boxRunBudgetPolicy(response, request, owner)
	if response.Code != http.StatusOK {
		t.Fatalf("set four-hour limit: status=%d body=%s", response.Code, response.Body.String())
	}
	invalid := httptest.NewRequest(http.MethodPut, "/v1/logical-boxes/"+boxID+"/run-budget-policy", strings.NewReader(`{}`))
	invalid.SetPathValue("id", boxID)
	invalidResponse := httptest.NewRecorder()
	(&Server{Store: store}).boxRunBudgetPolicy(invalidResponse, invalid, owner)
	if invalidResponse.Code != http.StatusBadRequest {
		t.Fatalf("missing seconds accepted: status=%d", invalidResponse.Code)
	}
	budget, err = store.syncAgentRunBudget(ctx, owner.AccountID, boxID, 8*time.Hour)
	if err != nil || budget.BudgetSeconds != 4*3600 || budget.DeadlineAt == nil || budget.RemainingSeconds < 4*3600-10 {
		t.Fatalf("four-hour budget=%+v error=%v", budget, err)
	}
	adjust := func(action string, seconds int64, deadline time.Time) (*httptest.ResponseRecorder, boxRunBudgetPolicyResponse) {
		t.Helper()
		body, err := json.Marshal(map[string]any{"action": action, "seconds": seconds, "expectedDeadlineAt": deadline})
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(http.MethodPost, "/v1/logical-boxes/"+boxID+"/run-budget-policy/adjust", strings.NewReader(string(body)))
		request.SetPathValue("id", boxID)
		response := httptest.NewRecorder()
		(&Server{Store: store}).boxRunBudgetPolicy(response, request, owner)
		var policy boxRunBudgetPolicyResponse
		if response.Code == http.StatusOK {
			if err := json.Unmarshal(response.Body.Bytes(), &policy); err != nil {
				t.Fatal(err)
			}
		}
		return response, policy
	}
	initialDeadline := *budget.DeadlineAt
	response, view = adjust("add", 8*3600, initialDeadline)
	if response.Code != http.StatusOK || view.Seconds != 4*3600 || view.RemainingSeconds < 12*3600-10 || view.DeadlineAt == nil || view.RunningSince == nil || view.RunningSince.Sub(started) > time.Second || started.Sub(*view.RunningSince) > time.Second {
		t.Fatalf("add eight hours: status=%d policy=%+v body=%s", response.Code, view, response.Body.String())
	}
	stale, _ := adjust("add", 8*3600, initialDeadline)
	if stale.Code != http.StatusConflict {
		t.Fatalf("stale adjustment accepted: status=%d body=%s", stale.Code, stale.Body.String())
	}
	invalidAdjust, _ := adjust("add", 3600, *view.DeadlineAt)
	if invalidAdjust.Code != http.StatusBadRequest {
		t.Fatalf("unsupported adjustment accepted: status=%d", invalidAdjust.Code)
	}
	response, view = adjust("reset", 0, *view.DeadlineAt)
	if response.Code != http.StatusOK || view.Seconds != 4*3600 || view.RemainingSeconds < 4*3600-10 || view.RemainingSeconds > 4*3600 || view.RunningSince == nil || view.RunningSince.Sub(started) > time.Second || started.Sub(*view.RunningSince) > time.Second {
		t.Fatalf("reset countdown: status=%d policy=%+v body=%s", response.Code, view, response.Body.String())
	}
	if err := store.SetBoxRunBudget(ctx, owner.AccountID, boxID, 0); err != nil {
		t.Fatal(err)
	}
	off, _ := adjust("add", 4*3600, *view.DeadlineAt)
	if off.Code != http.StatusConflict {
		t.Fatalf("disabled countdown extended: status=%d body=%s", off.Code, off.Body.String())
	}
	budget, err = store.syncAgentRunBudget(ctx, owner.AccountID, boxID, 8*time.Hour)
	if err != nil || budget.BudgetSeconds != 0 || budget.DeadlineAt != nil || budget.RemainingSeconds != 0 {
		t.Fatalf("disabled budget=%+v error=%v", budget, err)
	}
	getResponse = httptest.NewRecorder()
	(&Server{Store: store}).boxRunBudgetPolicy(getResponse, get, owner)
	view = boxRunBudgetPolicyResponse{}
	if err := json.Unmarshal(getResponse.Body.Bytes(), &view); getResponse.Code != http.StatusOK || err != nil || view.RunningSince == nil {
		t.Fatalf("runtime disappeared with limit off: status=%d body=%s error=%v", getResponse.Code, getResponse.Body.String(), err)
	}
	if _, err := store.DB.ExecContext(ctx, `UPDATE logical_boxes SET state='hibernated',assignment_generation=assignment_generation+1 WHERE id=$1`, boxID); err != nil {
		t.Fatal(err)
	}
	if err := store.SetBoxRunBudget(ctx, owner.AccountID, boxID, 2*3600); err != nil {
		t.Fatal(err)
	}
	getResponse = httptest.NewRecorder()
	(&Server{Store: store}).boxRunBudgetPolicy(getResponse, get, owner)
	view = boxRunBudgetPolicyResponse{}
	if err := json.Unmarshal(getResponse.Body.Bytes(), &view); getResponse.Code != http.StatusOK || err != nil || view.RunningSince != nil {
		t.Fatalf("stopped box still has current runtime: status=%d body=%s error=%v", getResponse.Code, getResponse.Body.String(), err)
	}
	budget, err = store.syncAgentRunBudget(ctx, owner.AccountID, boxID, 8*time.Hour)
	if err != nil || budget.BudgetSeconds != 2*3600 || budget.DeadlineAt != nil {
		t.Fatalf("hibernated budget=%+v error=%v", budget, err)
	}
	if _, err := store.DB.ExecContext(ctx, `UPDATE logical_boxes SET state='running',assignment_generation=assignment_generation+1 WHERE id=$1`, boxID); err != nil {
		t.Fatal(err)
	}
	resumedAt := time.Now().UTC().Add(-10 * time.Minute)
	if _, err := store.DB.ExecContext(ctx, `INSERT INTO allocation_requests(id,account_id,logical_box_id,state,idempotency_key,requested_by,slot_id,assignment_generation,updated_at)
		VALUES($1,$2,$3,'ready',$4,$5,$6,2,$7)`, uuid(), owner.AccountID, boxID, uuid(), owner.UserID, slotID, resumedAt); err != nil {
		t.Fatal(err)
	}
	getResponse = httptest.NewRecorder()
	(&Server{Store: store}).boxRunBudgetPolicy(getResponse, get, owner)
	view = boxRunBudgetPolicyResponse{}
	if err := json.Unmarshal(getResponse.Body.Bytes(), &view); getResponse.Code != http.StatusOK || err != nil || view.RunningSince == nil || view.RunningSince.Sub(resumedAt) > time.Second || resumedAt.Sub(*view.RunningSince) > time.Second {
		t.Fatalf("resumed run did not reset elapsed time: status=%d body=%s error=%v", getResponse.Code, getResponse.Body.String(), err)
	}
	budget, err = store.syncAgentRunBudget(ctx, owner.AccountID, boxID, 8*time.Hour)
	if err != nil || budget.BudgetSeconds != 2*3600 || budget.DeadlineAt == nil {
		t.Fatalf("resumed budget=%+v error=%v", budget, err)
	}
}
