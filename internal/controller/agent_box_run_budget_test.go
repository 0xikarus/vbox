package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/DATA-DOG/go-sqlmock"
)

func TestValidateAgentBoxRunBudget(t *testing.T) {
	box := v1.LogicalBox{ID: "box-b", Name: "builder", State: v1.LogicalBoxHibernated}
	if err := validateAgentBoxRunBudget("box-a", box, false, "builder"); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, actor, confirmation, want string
		protected                       bool
	}{
		{"self", "box-b", "builder", "own", false},
		{"name", "box-a", "Builder", "exactly match", false},
		{"protected", "box-a", "builder", "protected", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := validateAgentBoxRunBudget(test.actor, box, test.protected, test.confirmation); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v, want %q", err, test.want)
			}
		})
	}
}

func TestAgentBoxRunBudgetRequiresGrantAndTool(t *testing.T) {
	for _, test := range []struct {
		name, tools string
		restart     bool
	}{
		{"missing grant", `["set_agent_box_run_budget"]`, false},
		{"missing tool", `["restart_agent_box"]`, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, mock := testStore(t)
			manage, _ := json.Marshal(v1.ManageAgentBoxesGrant{Restart: test.restart})
			mcp := []byte(`{"enabled":true,"allowedTools":` + test.tools + `}`)
			mock.ExpectQuery("FROM box_role_assignments").WithArgs("account-a", "box-a").WillReturnRows(sqlmock.NewRows([]string{"permission", "config"}).AddRow(v1.RolePermissionManageAgentBoxes, manage).AddRow(v1.RolePermissionMCPTools, mcp))
			request := httptest.NewRequest(http.MethodPut, "/v1/agent-desktop/boxes/builder/run-budget", strings.NewReader(`{"seconds":14400,"confirmation":"builder"}`))
			request.SetPathValue("box", "builder")
			response := httptest.NewRecorder()
			(&Server{Store: store}).agentBoxRunBudgetHandler(response, request, Principal{AccountID: "account-a", Subject: "desktop-box:box-a"})
			if response.Code != http.StatusForbidden {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestAgentBoxRunBudgetSetsTargetLimit(t *testing.T) {
	store, mock := testStore(t)
	manage, _ := json.Marshal(v1.ManageAgentBoxesGrant{Restart: true})
	mcp, _ := json.Marshal(v1.MCPToolsGrant{Enabled: true, AllowedTools: []string{"set_agent_box_run_budget"}})
	mock.ExpectQuery("FROM box_role_assignments").WithArgs("account-a", "box-a").WillReturnRows(sqlmock.NewRows([]string{"permission", "config"}).AddRow(v1.RolePermissionManageAgentBoxes, manage).AddRow(v1.RolePermissionMCPTools, mcp))
	boxRows := func() *sqlmock.Rows {
		return sqlmock.NewRows([]string{"id", "account_id", "owner_user_id", "name", "provider", "provider_credential", "default_agent", "roles", "state", "volume_id", "volume_name", "slot_id", "assignment_generation", "lease_owner", "lease_expires_at", "restoration_state", "failure_reason", "created_at", "updated_at", "tools"}).
			AddRow("box-b", "account-a", "owner-a", "builder", "railway", "primary", "codex", []byte(`[]`), "running", "volume-b", "volume-b", "slot-b", 1, "", nil, "", "", time.Now(), time.Now(), []byte(`[]`))
	}
	mock.ExpectQuery("FROM logical_boxes WHERE account_id=").WithArgs("account-a", "builder").WillReturnRows(boxRows())
	mock.ExpectQuery("FROM logical_boxes WHERE account_id=").WithArgs("account-a", "box-b").WillReturnRows(boxRows())
	mock.ExpectQuery("SELECT EXISTS\\(SELECT 1 FROM box_protection").WithArgs("account-a", "box-b").WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT state,assignment_generation FROM logical_boxes").WithArgs("account-a", "box-b").WillReturnRows(sqlmock.NewRows([]string{"state", "generation"}).AddRow("running", 1))
	mock.ExpectExec("UPDATE logical_boxes SET metadata=jsonb_set").WithArgs("account-a", "box-b", int64(14400)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO agent_run_budgets").WithArgs("account-a", "box-b", int64(1), int64(14400), sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	mock.ExpectQuery("SELECT state,assignment_generation,").WithArgs("account-a", "box-b", int64(8*3600), maxAgentRunBudgetSeconds).WillReturnRows(sqlmock.NewRows([]string{"state", "generation", "seconds"}).AddRow("running", 1, 14400))
	mock.ExpectExec("INSERT INTO agent_run_budgets").WithArgs("account-a", "box-b", int64(1), int64(14400), sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("UPDATE agent_run_budgets SET deadline_at=now").WithArgs("account-a", "box-b").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("SELECT remaining_seconds,deadline_at,extension_seconds").WithArgs("account-a", "box-b").WillReturnRows(sqlmock.NewRows([]string{"remaining_seconds", "deadline_at", "extension_seconds"}).AddRow(14400, time.Now().Add(4*time.Hour), 0))
	request := httptest.NewRequest(http.MethodPut, "/v1/agent-desktop/boxes/builder/run-budget", strings.NewReader(`{"seconds":14400,"confirmation":"builder"}`))
	request.SetPathValue("box", "builder")
	response := httptest.NewRecorder()
	(&Server{Store: store, DefaultRunBudget: 8 * time.Hour}).agentBoxRunBudgetHandler(response, request, Principal{AccountID: "account-a", Subject: "desktop-box:box-a"})
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"budgetSeconds":14400`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
