package controller

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/provider"
	"github.com/DATA-DOG/go-sqlmock"
)

func viewerBoxRows(state string) *sqlmock.Rows {
	now := time.Now()
	return sqlmock.NewRows([]string{"id", "account_id", "owner_user_id", "name", "provider", "provider_credential", "default_agent", "role", "state", "volume_id", "volume_name", "slot_id", "assignment_generation", "lease_owner", "lease_expires_at", "restoration_state", "failure_reason", "created_at", "updated_at", "tools"}).AddRow("box", "account", "owner", "fixture", "fake", "primary", "codex", "worker", state, "vol", "vol", "", 1, "", nil, "", "", now, now, "[]")
}
func expectViewerAssignment(m sqlmock.Sqlmock, state, fence string) {
	m.ExpectQuery("FROM logical_boxes").WithArgs("account", "box").WillReturnRows(viewerBoxRows(state))
	m.ExpectQuery("SELECT COALESCE\\(fencing_token").WithArgs("account", "box").WillReturnRows(sqlmock.NewRows([]string{"fencing_token"}).AddRow(fence))
}

type viewerReuseProvider struct {
	fakeProvider
	calls   int
	partial bool
	changed bool
}

func (p *viewerReuseProvider) Exec(_ context.Context, _ string, argv []string, _ provider.ExecOptions) (provider.ExecResult, error) {
	p.calls++
	if len(argv) != 3 || argv[1] != "native-sessions" {
		return provider.ExecResult{ExitCode: 99}, nil
	}
	inv := v1.SessionInventory{State: "live", Assignment: argv[2], Partial: p.partial, Sessions: []v1.Session{{Name: "codex-live", ID: "$1", Incarnation: "incarnation"}}}
	if p.changed {
		inv.Assignment = "wrong"
	}
	data, _ := json.Marshal(inv)
	return provider.ExecResult{Stdout: string(data)}, nil
}
func TestViewerReuseDoesNotUploadRuntimeOrLockBox(t *testing.T) {
	for _, tc := range []struct {
		name                         string
		partial, wrongFence, changed bool
		status                       int
	}{{name: "existing", status: 200}, {name: "partial", partial: true, status: 409}, {name: "wrong worker assignment", wrongFence: true, status: 409}, {name: "assignment changed", changed: true, status: 409}} {
		t.Run(tc.name, func(t *testing.T) {
			store, m := testStore(t)
			p := &viewerReuseProvider{partial: tc.partial, changed: tc.wrongFence}
			s := &Server{Store: store, WorkerRuntime: []byte("must not be uploaded"), Resolve: func(context.Context, string, string, string) (provider.Provider, error) { return p, nil }}
			m.ExpectQuery("FROM logical_boxes").WithArgs("account", "box").WillReturnRows(viewerBoxRows("running"))
			expectViewerAssignment(m, "running", "fence")
			m.ExpectQuery("SELECT COALESCE\\(metadata").WithArgs("account", "box").WillReturnRows(sqlmock.NewRows([]string{"primarySession"}).AddRow("codex-live"))
			if !tc.partial && !tc.wrongFence {
				fence := "fence"
				if tc.changed {
					fence = "new"
				}
				expectViewerAssignment(m, "running", fence)
			}
			r := httptest.NewRequest("POST", "/", strings.NewReader(`{"agent":"codex","reuseExisting":true}`))
			r.SetPathValue("id", "box")
			w := httptest.NewRecorder()
			s.interactiveStartHandler(w, r, Principal{AccountID: "account", UserID: "owner", Role: "owner"})
			if w.Code != tc.status {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			if tc.status == 200 && !strings.Contains(w.Body.String(), "codex-live") {
				t.Fatalf("missing reused session: %s", w.Body.String())
			}
			if p.calls != 1 {
				t.Fatalf("worker operations=%d; want only inventory", p.calls)
			}
			if err := m.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestDesktopViewerDoesNotEnumerateTmuxSessions(t *testing.T) {
	for _, state := range []string{"running", "hibernated"} {
		t.Run(state, func(t *testing.T) {
			store, m := testStore(t)
			s := &Server{Store: store}
			expectViewerAssignment(m, state, "fence")
			inv, err := s.observeViewer(t.Context(), Principal{AccountID: "account", Role: "owner"}, "box", true)
			if state == "running" && (err != nil || inv.Assignment == "" || inv.LogicalBoxID != "box") {
				t.Fatalf("inventory=%+v error=%v", inv, err)
			}
			if state != "running" && err == nil {
				t.Fatal("stopped desktop allowed")
			}
			if err := m.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
