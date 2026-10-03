package controller

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/DATA-DOG/go-sqlmock"
)

func controlInt(value int) *int { return &value }

func TestRemoteControlActionValidation(t *testing.T) {
	for _, test := range []struct {
		name       string
		request    remoteControlRequest
		wantAction string
		bad        bool
	}{
		{"screenshot", remoteControlRequest{Action: "screenshot"}, "screenshot", false},
		{"move", remoteControlRequest{Action: "move", X: controlInt(5), Y: controlInt(6)}, "move", false},
		{"double right click", remoteControlRequest{Action: "click", X: controlInt(5), Y: controlInt(6), Button: "right", Double: true}, "click", false},
		{"drag", remoteControlRequest{Action: "drag", X: controlInt(5), Y: controlInt(6), ToX: controlInt(9), ToY: controlInt(10)}, "drag", false},
		{"scroll", remoteControlRequest{Action: "scroll", X: controlInt(5), Y: controlInt(6), DY: -3}, "scroll", false},
		{"type", remoteControlRequest{Action: "type", Text: "hello"}, "type", false},
		{"keys", remoteControlRequest{Action: "keys", Keys: "ctrl+l"}, "key", false},
		{"missing point", remoteControlRequest{Action: "click", Y: controlInt(6)}, "", true},
		{"outside desktop", remoteControlRequest{Action: "move", X: controlInt(5000), Y: controlInt(1)}, "", true},
		{"scroll both axes", remoteControlRequest{Action: "scroll", X: controlInt(1), Y: controlInt(1), DX: 2, DY: 1}, "", true},
		{"scroll too far", remoteControlRequest{Action: "scroll", X: controlInt(1), Y: controlInt(1), DY: 21}, "", true},
		{"type secret unavailable", remoteControlRequest{Action: "type_secret", Text: "hidden"}, "", true},
		{"invalid key", remoteControlRequest{Action: "keys", Keys: "ctrl+F13"}, "", true},
		{"too much text", remoteControlRequest{Action: "type", Text: strings.Repeat("a", 16385)}, "", true},
		{"screenshot with action", remoteControlRequest{Action: "screenshot", X: controlInt(1)}, "", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			action, err := test.request.desktopAction()
			if (err != nil) != test.bad {
				t.Fatalf("action=%+v err=%v", action, err)
			}
			if !test.bad && action.Action != test.wantAction {
				t.Fatalf("action=%q want=%q", action.Action, test.wantAction)
			}
		})
	}
}

func TestRemoteControlTargetRules(t *testing.T) {
	for _, tc := range []struct {
		name      string
		actor     string
		protected bool
		state     v1.LogicalBoxState
		bad       bool
	}{
		{"other running", "box-a", false, v1.LogicalBoxRunning, false},
		{"self", "box-b", false, v1.LogicalBoxRunning, true},
		{"protected", "box-a", true, v1.LogicalBoxRunning, true},
		{"hibernated", "box-a", false, v1.LogicalBoxHibernated, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateRemoteControlTarget(tc.actor, v1.LogicalBox{ID: "box-b", State: tc.state}, tc.protected)
			if (err != nil) != tc.bad {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestRemoteControlRequiresControlGrantAndSelectedTool(t *testing.T) {
	for _, tc := range []struct {
		name    string
		control bool
		tools   []string
	}{
		{"missing capability", false, []string{"remote_control_box"}},
		{"missing tool", true, []string{"get_agent_box_screenshot"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, mock := testStore(t)
			manage, _ := json.Marshal(v1.ManageAgentBoxesGrant{Control: tc.control})
			mcp, _ := json.Marshal(v1.MCPToolsGrant{Enabled: true, AllowedTools: tc.tools})
			mock.ExpectQuery("FROM box_role_assignments").WithArgs("account-a", "box-a").WillReturnRows(sqlmock.NewRows([]string{"permission", "config"}).AddRow(v1.RolePermissionManageAgentBoxes, manage).AddRow(v1.RolePermissionMCPTools, mcp))
			r := httptest.NewRequest(http.MethodPost, "/v1/agent-desktop/boxes/box-b/control", strings.NewReader(`{"action":"screenshot"}`))
			r.SetPathValue("box", "box-b")
			w := httptest.NewRecorder()
			(&Server{Store: store}).agentBoxControlHandler(w, r, Principal{AccountID: "account-a", Subject: "desktop-box:box-a"})
			if w.Code != 403 {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestRemoteControlPolicyAddsScreenshotCompanion(t *testing.T) {
	store, mock := testStore(t)
	manage, _ := json.Marshal(v1.ManageAgentBoxesGrant{Control: true})
	mcp, _ := json.Marshal(v1.MCPToolsGrant{Enabled: true, AllowedTools: []string{"remote_control_box"}})
	mock.ExpectQuery("FROM box_role_assignments").WithArgs("account-a", "box-a").WillReturnRows(sqlmock.NewRows([]string{"permission", "config"}).AddRow(v1.RolePermissionManageAgentBoxes, manage).AddRow(v1.RolePermissionMCPTools, mcp))
	tools, err := store.EffectiveAgentToolNames(context.Background(), "account-a", "box-a")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"remote_control_box", "get_agent_box_screenshot"} {
		if !slicesContains(tools, name) {
			t.Fatalf("missing %s in %v", name, tools)
		}
	}
	if slicesContains(tools, "get_agent_box") {
		t.Fatalf("inspection granted unexpectedly: %v", tools)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func slicesContains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func TestRemoteControlRateLimitAndAuditMetadata(t *testing.T) {
	store, mock := testStore(t)
	mock.ExpectBegin()
	mock.ExpectExec("pg_advisory_xact_lock").WithArgs("account-a:box-a", "box-b").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("FROM remote_control_actions WHERE").WithArgs("account-a", "box-a", "box-b").WillReturnRows(sqlmock.NewRows([]string{"second", "minute"}).AddRow(9, 299))
	mock.ExpectExec("INSERT INTO remote_control_actions").WithArgs(sqlmock.AnyArg(), "account-a", "box-a", "box-b", "type", nil, nil, nil, nil, 0, 0, 5).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	id, limited, err := store.reserveRemoteControl(context.Background(), "account-a", "box-a", "box-b", remoteControlRequest{Action: "type", Text: "hello"})
	if err != nil || limited || id == "" {
		t.Fatalf("id=%s limited=%v err=%v", id, limited, err)
	}
	mock.ExpectBegin()
	mock.ExpectExec("pg_advisory_xact_lock").WithArgs("account-a:box-a", "box-b").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("FROM remote_control_actions WHERE").WithArgs("account-a", "box-a", "box-b").WillReturnRows(sqlmock.NewRows([]string{"second", "minute"}).AddRow(10, 300))
	mock.ExpectRollback()
	_, limited, err = store.reserveRemoteControl(context.Background(), "account-a", "box-a", "box-b", remoteControlRequest{Action: "click"})
	if err != nil || !limited {
		t.Fatalf("limited=%v err=%v", limited, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

type controlBodyMatcher int

func (wanted controlBodyMatcher) Match(value driver.Value) bool {
	text, ok := value.(string)
	if !ok {
		return false
	}
	message := v1.BoxMessage{Text: text}
	decodeBoxMessageControl(&message)
	return message.Control != nil && message.Control.Actions == int(wanted)
}

func TestRemoteControlNoticeUpdatesSameMessageInSession(t *testing.T) {
	store, mock := testStore(t)
	first := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	const notice = "11111111-1111-4111-8111-111111111111"
	for count := 1; count <= 2; count++ {
		mock.ExpectBegin()
		mock.ExpectExec("UPDATE remote_control_actions SET status='done'").WithArgs("action-id", "account-a").WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectExec("INSERT INTO audit_log").WithArgs("action-id", "account-a").WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectQuery("INSERT INTO remote_control_sessions").WithArgs("account-a", "box-a", "box-b", sqlmock.AnyArg()).WillReturnRows(sqlmock.NewRows([]string{"notice_id", "actions", "first", "last"}).AddRow(notice, count, first, first.Add(time.Duration(count)*time.Second)))
		mock.ExpectQuery("SELECT id::text FROM box_tasks").WithArgs("account-a", "box-b").WillReturnRows(sqlmock.NewRows([]string{"task"}).AddRow("task-id"))
		mock.ExpectExec("INSERT INTO box_messages").WithArgs(notice, "account-a", "task-id", controlBodyMatcher(count), "remote-control:"+notice, sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectCommit()
		if err := store.finishRemoteControl(context.Background(), "account-a", "box-a", "Manager", "box-b", "action-id"); err != nil {
			t.Fatal(err)
		}
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestOwnerLogicalBoxResponsesIncludeLastRemoteControl(t *testing.T) {
	for _, path := range []string{"/v1/logical-boxes", "/v1/logical-boxes/box-b"} {
		t.Run(path, func(t *testing.T) {
			store, mock := testStore(t)
			boxRows := sqlmock.NewRows([]string{"id", "account_id", "owner_user_id", "name", "provider", "provider_credential", "default_agent", "roles", "state", "volume_id", "volume_name", "slot_id", "assignment_generation", "lease_owner", "lease_expires_at", "restoration_state", "failure_reason", "created_at", "updated_at", "tools"}).
				AddRow("box-b", "account-a", "owner-a", "builder", "railway", "primary", "codex", []byte(`[]`), "running", "volume-b", "volume-b", "slot-b", 1, "", nil, "", "", time.Now(), time.Now(), []byte(`[]`))
			if path == "/v1/logical-boxes" {
				mock.ExpectQuery("FROM logical_boxes WHERE account_id=").WithArgs("account-a").WillReturnRows(boxRows)
			} else {
				mock.ExpectQuery("FROM logical_boxes WHERE account_id=").WithArgs("account-a", "box-b").WillReturnRows(boxRows)
			}
			stamp := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
			mock.ExpectQuery("FROM remote_control_sessions r LEFT JOIN logical_boxes b").WithArgs("account-a", "box-b").WillReturnRows(sqlmock.NewRows([]string{"actor", "name", "actions", "started", "ended"}).AddRow("box-a", "Manager", 4, stamp, stamp.Add(time.Minute)))
			r := httptest.NewRequest(http.MethodGet, path, nil)
			r.SetPathValue("id", "box-b")
			w := httptest.NewRecorder()
			s := &Server{Store: store}
			p := Principal{AccountID: "account-a", Role: "owner", UserID: "owner-a"}
			if path == "/v1/logical-boxes" {
				s.listLogicalBoxes(w, r, p)
			} else {
				s.getLogicalBox(w, r, p)
			}
			if w.Code != 200 || !strings.Contains(w.Body.String(), `"lastRemoteControl":`) || !strings.Contains(w.Body.String(), `"actions":4`) {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
