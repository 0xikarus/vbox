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

func TestAgentManagedBoxRedactsInfrastructureAndAccessDetails(t *testing.T) {
	box := v1.LogicalBox{ID: "box-b", Name: "builder", State: v1.LogicalBoxRunning, DefaultAgent: "codex", Provider: "railway", ProviderCredential: "secret-ref", VolumeID: "volume-secret", SlotID: "slot-secret"}
	got := safeAgentManagedBox(box)
	if got.ID != box.ID || got.Name != box.Name || got.State != box.State || got.DefaultAgent != box.DefaultAgent {
		t.Fatalf("safe box=%+v", got)
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"secret-ref", "volume-secret", "slot-secret"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("safe response exposed %q: %s", secret, encoded)
		}
	}
}

func TestValidateAgentBoxDeletionRequiresOtherUnprotectedBoxAndExactName(t *testing.T) {
	box := v1.LogicalBox{ID: "box-b", Name: "builder"}
	if err := validateAgentBoxDeletion("box-a", box, false, "builder"); err != nil {
		t.Fatal(err)
	}
	for name, test := range map[string]struct {
		actor        string
		protected    bool
		confirmation string
		want         string
	}{
		"self":      {actor: "box-b", confirmation: "builder", want: "cannot delete itself"},
		"protected": {actor: "box-a", protected: true, confirmation: "builder", want: "protected"},
		"name":      {actor: "box-a", confirmation: "Builder", want: "exactly match"},
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateAgentBoxDeletion(test.actor, box, test.protected, test.confirmation); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v, want %q", err, test.want)
			}
		})
	}
}

func TestValidateAgentBoxRestartRequiresRunningOtherUnprotectedBoxAndExactName(t *testing.T) {
	box := v1.LogicalBox{ID: "box-b", Name: "builder", State: v1.LogicalBoxRunning}
	if err := validateAgentBoxRestart("box-a", box, false, "builder"); err != nil {
		t.Fatal(err)
	}
	for name, test := range map[string]struct {
		actor        string
		protected    bool
		confirmation string
		state        v1.LogicalBoxState
		want         string
	}{
		"self":      {actor: "box-b", confirmation: "builder", state: v1.LogicalBoxRunning, want: "cannot restart itself"},
		"protected": {actor: "box-a", protected: true, confirmation: "builder", state: v1.LogicalBoxRunning, want: "protected"},
		"name":      {actor: "box-a", confirmation: "Builder", state: v1.LogicalBoxRunning, want: "exactly match"},
		"state":     {actor: "box-a", confirmation: "builder", state: v1.LogicalBoxHibernated, want: "only a running box"},
	} {
		t.Run(name, func(t *testing.T) {
			candidate := box
			candidate.State = test.state
			if err := validateAgentBoxRestart(test.actor, candidate, test.protected, test.confirmation); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v, want %q", err, test.want)
			}
		})
	}
}

func TestValidateAgentBoxWakeRequiresHibernatedOtherUnprotectedBoxAndExactName(t *testing.T) {
	box := v1.LogicalBox{ID: "box-b", Name: "builder", State: v1.LogicalBoxHibernated}
	if err := validateAgentBoxWake("box-a", box, false, "builder"); err != nil {
		t.Fatal(err)
	}
	for name, test := range map[string]struct {
		actor        string
		protected    bool
		confirmation string
		state        v1.LogicalBoxState
		want         string
	}{
		"self":      {actor: "box-b", confirmation: "builder", state: v1.LogicalBoxHibernated, want: "cannot wake itself"},
		"protected": {actor: "box-a", protected: true, confirmation: "builder", state: v1.LogicalBoxHibernated, want: "protected"},
		"name":      {actor: "box-a", confirmation: "Builder", state: v1.LogicalBoxHibernated, want: "exactly match"},
		"running":   {actor: "box-a", confirmation: "builder", state: v1.LogicalBoxRunning, want: "only a hibernated box"},
		"detached":  {actor: "box-a", confirmation: "builder", state: v1.LogicalBoxDetached, want: "only a hibernated box"},
	} {
		t.Run(name, func(t *testing.T) {
			candidate := box
			candidate.State = test.state
			if err := validateAgentBoxWake(test.actor, candidate, test.protected, test.confirmation); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v, want %q", err, test.want)
			}
		})
	}
}

func TestAgentBoxWakeRequiresRestartCapabilityAndAllowedMCPTool(t *testing.T) {
	for name, test := range map[string]struct {
		restart bool
		tools   []string
	}{
		"missing grant": {tools: []string{"restart_agent_box"}},
		"missing tool":  {restart: true, tools: []string{"list_agent_boxes"}},
	} {
		t.Run(name, func(t *testing.T) {
			store, mock := testStore(t)
			manage, _ := json.Marshal(v1.ManageAgentBoxesGrant{Restart: test.restart})
			mcp, _ := json.Marshal(v1.MCPToolsGrant{Enabled: true, AllowedTools: test.tools})
			mock.ExpectQuery("FROM box_role_assignments").WithArgs("account-a", "box-a").WillReturnRows(sqlmock.NewRows([]string{"permission", "config"}).AddRow(v1.RolePermissionManageAgentBoxes, manage).AddRow(v1.RolePermissionMCPTools, mcp))
			request := httptest.NewRequest(http.MethodPost, "/v1/agent-desktop/boxes/box-b/wake", strings.NewReader(`{"confirmation":"builder"}`))
			request.SetPathValue("box", "box-b")
			request.Header.Set("Idempotency-Key", "wake-one")
			response := httptest.NewRecorder()
			(&Server{Store: store}).agentBoxWakeHandler(response, request, Principal{AccountID: "account-a", Subject: "desktop-box:box-a"})
			if response.Code != http.StatusForbidden {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestAgentBoxScreenshotRequiresInspectionCapabilityAndAllowedTool(t *testing.T) {
	for name, test := range map[string]struct {
		inspect bool
		tools   []string
	}{
		"missing inspection grant": {tools: []string{"get_agent_box_screenshot"}},
		"missing screenshot tool":  {inspect: true, tools: []string{"get_agent_box"}},
	} {
		t.Run(name, func(t *testing.T) {
			store, mock := testStore(t)
			manage, _ := json.Marshal(v1.ManageAgentBoxesGrant{Inspect: test.inspect})
			mcp, _ := json.Marshal(v1.MCPToolsGrant{Enabled: true, AllowedTools: test.tools})
			mock.ExpectQuery("FROM box_role_assignments").WithArgs("account-a", "box-a").WillReturnRows(sqlmock.NewRows([]string{"permission", "config"}).AddRow(v1.RolePermissionManageAgentBoxes, manage).AddRow(v1.RolePermissionMCPTools, mcp))
			request := httptest.NewRequest(http.MethodGet, "/v1/agent-desktop/boxes/box-b/screenshot", nil)
			request.SetPathValue("box", "box-b")
			response := httptest.NewRecorder()
			(&Server{Store: store}).agentBoxScreenshotHandler(response, request, Principal{AccountID: "account-a", Subject: "desktop-box:box-a"})
			if response.Code != http.StatusForbidden {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestAgentBoxScreenshotRejectsProtectedTarget(t *testing.T) {
	store, mock := testStore(t)
	manage, _ := json.Marshal(v1.ManageAgentBoxesGrant{Inspect: true})
	mcp, _ := json.Marshal(v1.MCPToolsGrant{Enabled: true, AllowedTools: []string{"get_agent_box_screenshot"}})
	mock.ExpectQuery("FROM box_role_assignments").WithArgs("account-a", "box-a").WillReturnRows(sqlmock.NewRows([]string{"permission", "config"}).AddRow(v1.RolePermissionManageAgentBoxes, manage).AddRow(v1.RolePermissionMCPTools, mcp))
	boxRows := func() *sqlmock.Rows {
		return sqlmock.NewRows([]string{"id", "account_id", "owner_user_id", "name", "provider", "provider_credential", "default_agent", "roles", "state", "volume_id", "volume_name", "slot_id", "assignment_generation", "lease_owner", "lease_expires_at", "restoration_state", "failure_reason", "created_at", "updated_at", "tools"}).
			AddRow("box-b", "account-a", "owner-a", "builder", "railway", "primary", "codex", []byte(`[]`), "running", "volume-b", "volume-b", "slot-b", 1, "", nil, "", "", time.Now(), time.Now(), []byte(`[]`))
	}
	mock.ExpectQuery("FROM logical_boxes WHERE account_id=").WithArgs("account-a", "builder").WillReturnRows(boxRows())
	mock.ExpectQuery("FROM logical_boxes WHERE account_id=").WithArgs("account-a", "box-b").WillReturnRows(boxRows())
	mock.ExpectQuery("SELECT EXISTS\\(SELECT 1 FROM box_protection").WithArgs("account-a", "box-b").WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	request := httptest.NewRequest(http.MethodGet, "/v1/agent-desktop/boxes/builder/screenshot", nil)
	request.SetPathValue("box", "builder")
	response := httptest.NewRecorder()
	(&Server{Store: store}).agentBoxScreenshotHandler(response, request, Principal{AccountID: "account-a", Subject: "desktop-box:box-a"})
	if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), "protected") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestAgentBoxWakeRetryUsesExistingAllocationForSameTarget(t *testing.T) {
	for _, test := range []struct {
		name       string
		allocated  string
		wantStatus int
	}{
		{name: "same target", allocated: "box-b", wantStatus: http.StatusAccepted},
		{name: "different target", allocated: "box-c", wantStatus: http.StatusConflict},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, mock := testStore(t)
			manage, _ := json.Marshal(v1.ManageAgentBoxesGrant{Restart: true})
			mcp, _ := json.Marshal(v1.MCPToolsGrant{Enabled: true, AllowedTools: []string{"restart_agent_box"}})
			mock.ExpectQuery("FROM box_role_assignments").WithArgs("account-a", "box-a").WillReturnRows(sqlmock.NewRows([]string{"permission", "config"}).AddRow(v1.RolePermissionManageAgentBoxes, manage).AddRow(v1.RolePermissionMCPTools, mcp))
			boxRows := func() *sqlmock.Rows {
				return sqlmock.NewRows([]string{"id", "account_id", "owner_user_id", "name", "provider", "provider_credential", "default_agent", "roles", "state", "volume_id", "volume_name", "slot_id", "assignment_generation", "lease_owner", "lease_expires_at", "restoration_state", "failure_reason", "created_at", "updated_at", "tools"}).
					AddRow("box-b", "account-a", "owner-a", "builder", "railway", "primary", "codex", []byte(`[]`), "running", "volume-b", "volume-b", "slot-b", 1, "", nil, "", "", time.Now(), time.Now(), []byte(`[]`))
			}
			mock.ExpectQuery("FROM logical_boxes WHERE account_id=").WithArgs("account-a", "builder").WillReturnRows(boxRows())
			mock.ExpectQuery("FROM logical_boxes WHERE account_id=").WithArgs("account-a", "box-b").WillReturnRows(boxRows())
			mock.ExpectQuery("SELECT EXISTS\\(SELECT 1 FROM box_protection").WithArgs("account-a", "box-b").WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
			mock.ExpectQuery("FROM allocation_requests r JOIN logical_boxes b").WithArgs("account-a", "agent-wake:box-a:wake-one").WillReturnRows(sqlmock.NewRows([]string{"id", "idempotency_key", "state", "logical_box_id", "name", "slot_id", "service_id", "assignment_generation", "fencing_token", "lease_owner", "lease_expires_at", "phase", "retry_count", "failure_reason", "created_at", "updated_at"}).AddRow("request-one", "agent-wake:box-a:wake-one", "ready", test.allocated, "builder", "slot-b", "service-b", 1, "", "", nil, "", 0, "", time.Now(), time.Now()))
			if test.allocated == "box-b" {
				mock.ExpectQuery("SELECT COALESCE\\(session_choice,''\\) FROM allocation_requests").WithArgs("account-a", "request-one").WillReturnRows(sqlmock.NewRows([]string{"session_choice"}).AddRow(""))
			}
			request := httptest.NewRequest(http.MethodPost, "/v1/agent-desktop/boxes/builder/wake", strings.NewReader(`{"confirmation":"builder"}`))
			request.SetPathValue("box", "builder")
			request.Header.Set("Idempotency-Key", "wake-one")
			response := httptest.NewRecorder()
			(&Server{Store: store}).agentBoxWakeHandler(response, request, Principal{AccountID: "account-a", Subject: "desktop-box:box-a"})
			if response.Code != test.wantStatus {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestValidateAgentBoxContextClearRequiresRunningOtherUnprotectedBoxAndExactName(t *testing.T) {
	box := v1.LogicalBox{ID: "box-b", Name: "builder", State: v1.LogicalBoxRunning}
	if err := validateAgentBoxContextClear("box-a", box, false, "builder"); err != nil {
		t.Fatal(err)
	}
	for name, test := range map[string]struct {
		actor        string
		protected    bool
		confirmation string
		state        v1.LogicalBoxState
		want         string
	}{
		"self":      {actor: "box-b", confirmation: "builder", state: v1.LogicalBoxRunning, want: "cannot clear its own context"},
		"protected": {actor: "box-a", protected: true, confirmation: "builder", state: v1.LogicalBoxRunning, want: "protected"},
		"name":      {actor: "box-a", confirmation: "Builder", state: v1.LogicalBoxRunning, want: "exactly match"},
		"state":     {actor: "box-a", confirmation: "builder", state: v1.LogicalBoxHibernated, want: "only a running box"},
	} {
		t.Run(name, func(t *testing.T) {
			candidate := box
			candidate.State = test.state
			if err := validateAgentBoxContextClear(test.actor, candidate, test.protected, test.confirmation); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v, want %q", err, test.want)
			}
		})
	}
}

func TestValidateAgentBoxCompactRequiresRunningOtherUnprotectedBoxAndExactName(t *testing.T) {
	box := v1.LogicalBox{ID: "box-b", Name: "builder", State: v1.LogicalBoxRunning}
	if err := validateAgentBoxCompact("box-a", box, false, "builder"); err != nil {
		t.Fatal(err)
	}
	for name, test := range map[string]struct {
		actor        string
		protected    bool
		confirmation string
		state        v1.LogicalBoxState
		want         string
	}{
		"self":      {actor: "box-b", confirmation: "builder", state: v1.LogicalBoxRunning, want: "cannot compact its own context"},
		"protected": {actor: "box-a", protected: true, confirmation: "builder", state: v1.LogicalBoxRunning, want: "protected"},
		"name":      {actor: "box-a", confirmation: "Builder", state: v1.LogicalBoxRunning, want: "exactly match"},
		"state":     {actor: "box-a", confirmation: "builder", state: v1.LogicalBoxHibernated, want: "only a running box"},
	} {
		t.Run(name, func(t *testing.T) {
			candidate := box
			candidate.State = test.state
			if err := validateAgentBoxCompact(test.actor, candidate, test.protected, test.confirmation); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v, want %q", err, test.want)
			}
		})
	}
}
