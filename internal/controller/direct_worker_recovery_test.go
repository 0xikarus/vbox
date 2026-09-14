package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/provider"
	"github.com/DATA-DOG/go-sqlmock"
)

type pendingRecoveryProvider struct {
	fakeProvider
	mode  string
	calls int
}

func (p *pendingRecoveryProvider) Connection(context.Context, string) (provider.Connection, error) {
	return provider.Connection{Transport: "openssh", Endpoint: "bootstrap-deployment@ssh.railway.com", Metadata: map[string]string{"deploymentInstanceId": "bootstrap-deployment"}}, nil
}

func (p *pendingRecoveryProvider) ExecConnection(_ context.Context, _ provider.Connection, _ []string, options provider.ExecOptions) (provider.ExecResult, error) {
	p.calls++
	var payload bytes.Buffer
	if options.Stdin != nil {
		_, _ = payload.ReadFrom(options.Stdin)
	}
	if bytes.Contains(payload.Bytes(), []byte("VMBOX_RECOVERY_IDENTITY")) {
		switch p.mode {
		case "partial":
			return provider.ExecResult{Stdout: "worker-agent-installation-recovery-started\n"}, nil
		case "ambiguous":
			return provider.ExecResult{}, errors.New("connection lost")
		case "absent", "credentialed-absent":
			return provider.ExecResult{ExitCode: 66, Stdout: "worker-agent-installation-absent\n"}, nil
		}
	}
	if p.mode == "absent" && bytes.Contains(payload.Bytes(), []byte("VMBOX_WORKER_PAYLOAD")) {
		return provider.ExecResult{Stdout: "worker-agent-installed\n"}, nil
	}
	return provider.ExecResult{}, errors.New("unexpected recovery operation")
}

func TestPendingWorkerRecoveryIsPinnedAndReissuesOnlyAfterProvenAbsence(t *testing.T) {
	for _, mode := range []string{"partial", "ambiguous", "absent", "credentialed-absent"} {
		t.Run(mode, func(t *testing.T) {
			store, mock := testStore(t)
			server := NewServer(store, nil)
			server.PublicURL = "https://controller.example"
			server.WorkerAgent = []byte("agent-fixture")
			a := fleetAssignment{
				Box:          v1.LogicalBox{ID: "box", AccountID: "account", Provider: "railway", State: "attaching", AssignmentGeneration: 3},
				Slot:         v1.ComputeSlot{ID: "slot", ServiceID: "service"},
				FencingToken: "fence",
			}
			baseline, _ := json.Marshal(v1.SessionInventory{Assignment: nativeFence(a), State: "live", Sessions: []v1.Session{}})
			enrollment, credential := true, false
			if mode == "credentialed-absent" {
				enrollment, credential = false, true
			}
			mock.ExpectQuery(`SELECT w.id::text,w.account_id::text,w.slot_id::text`).
				WithArgs("account", "slot").
				WillReturnRows(sqlmock.NewRows([]string{"id", "account_id", "slot_id", "service_id", "incarnation", "connection_epoch", "transport_enabled", "migration_sessions", "bootstrap_deployment_id", "enrollment", "credential"}).
					AddRow("worker", "account", "slot", "service", "", int64(5), false, baseline, "bootstrap-deployment", enrollment, credential))
			if enrollment {
				mock.ExpectExec(`UPDATE direct_workers SET enrollment_expires_at`).
					WithArgs("worker", "account", "slot", int64(5)).
					WillReturnResult(sqlmock.NewResult(0, 1))
			}
			expectAssignmentLock := func() {
				mock.ExpectBegin()
				mock.ExpectQuery(`SELECT b.assignment_generation`).
					WithArgs("box", "account", "slot", int64(3), "fence", v1.LogicalBoxState("attaching")).
					WillReturnRows(sqlmock.NewRows([]string{"assignment_generation"}).AddRow(int64(3)))
				mock.ExpectRollback()
			}
			expectAssignmentLock()
			if mode == "absent" {
				mock.ExpectBegin()
				mock.ExpectQuery(`SELECT b.assignment_generation`).
					WithArgs("box", "account", "slot", int64(3), "fence", v1.LogicalBoxState("attaching")).
					WillReturnRows(sqlmock.NewRows([]string{"assignment_generation"}).AddRow(int64(3)))
				mock.ExpectExec(`UPDATE direct_workers SET enrollment_hash`).
					WithArgs(sqlmock.AnyArg(), "worker", "account", "slot", int64(5), baseline, "bootstrap-deployment").
					WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectCommit()
				expectAssignmentLock()
			}
			prov := &pendingRecoveryProvider{mode: mode}
			_, err := server.recoverWorkerForAssignment(context.Background(), "account", a, prov)
			if mode == "ambiguous" || mode == "credentialed-absent" {
				if err == nil || prov.calls != 1 {
					t.Fatalf("ambiguous recovery changed course: calls=%d err=%v", prov.calls, err)
				}
			} else if err != nil {
				t.Fatal(err)
			} else if want := map[string]int{"partial": 1, "absent": 2}[mode]; prov.calls != want {
				t.Fatalf("recovery calls=%d want=%d", prov.calls, want)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
