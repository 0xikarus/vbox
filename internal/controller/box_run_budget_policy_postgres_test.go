package controller

import (
	"context"
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
	if err := store.SetBoxRunBudget(ctx, owner.AccountID, boxID, 0); err != nil {
		t.Fatal(err)
	}
	budget, err = store.syncAgentRunBudget(ctx, owner.AccountID, boxID, 8*time.Hour)
	if err != nil || budget.BudgetSeconds != 0 || budget.DeadlineAt != nil || budget.RemainingSeconds != 0 {
		t.Fatalf("disabled budget=%+v error=%v", budget, err)
	}
	if _, err := store.DB.ExecContext(ctx, `UPDATE logical_boxes SET state='hibernated',assignment_generation=assignment_generation+1 WHERE id=$1`, boxID); err != nil {
		t.Fatal(err)
	}
	if err := store.SetBoxRunBudget(ctx, owner.AccountID, boxID, 2*3600); err != nil {
		t.Fatal(err)
	}
	budget, err = store.syncAgentRunBudget(ctx, owner.AccountID, boxID, 8*time.Hour)
	if err != nil || budget.BudgetSeconds != 2*3600 || budget.DeadlineAt != nil {
		t.Fatalf("hibernated budget=%+v error=%v", budget, err)
	}
	if _, err := store.DB.ExecContext(ctx, `UPDATE logical_boxes SET state='running',assignment_generation=assignment_generation+1 WHERE id=$1`, boxID); err != nil {
		t.Fatal(err)
	}
	budget, err = store.syncAgentRunBudget(ctx, owner.AccountID, boxID, 8*time.Hour)
	if err != nil || budget.BudgetSeconds != 2*3600 || budget.DeadlineAt == nil {
		t.Fatalf("resumed budget=%+v error=%v", budget, err)
	}
}
