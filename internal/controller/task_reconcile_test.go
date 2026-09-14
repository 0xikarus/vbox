package controller

import (
	"context"
	"errors"
	"reflect"
	"testing"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/provider"
	"github.com/DATA-DOG/go-sqlmock"
)

type sessionProbeProvider struct {
	fakeProvider
	result provider.ExecResult
	err    error
}

func (p *sessionProbeProvider) Exec(_ context.Context, _ string, argv []string, _ provider.ExecOptions) (provider.ExecResult, error) {
	p.argv = argv
	return p.result, p.err
}

func TestTaskSessionProbeDistinguishesExitFromConnectionFailure(t *testing.T) {
	for _, tc := range []struct {
		name               string
		code               int
		stderr             string
		err                error
		missing, wantError bool
	}{
		{name: "alive"},
		{name: "missing session", code: 1, stderr: "can't find session: codex-one", missing: true},
		{name: "last session exited", code: 1, stderr: "no server running on /tmp/tmux-10001/default", missing: true},
		{name: "no socket", code: 1, stderr: "error connecting to /tmp/tmux-10001/default (No such file or directory)", missing: true},
		{name: "permission denied", code: 1, stderr: "error connecting to /tmp/tmux-10001/default (Permission denied)", wantError: true},
		{name: "SSH disconnected", code: 255, wantError: true},
		{name: "request failed", err: errors.New("timeout"), wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &sessionProbeProvider{result: provider.ExecResult{ExitCode: tc.code, Stderr: tc.stderr}, err: tc.err}
			missing, err := taskSessionMissing(context.Background(), p, "slot", "codex-one")
			if missing != tc.missing || (err != nil) != tc.wantError {
				t.Fatalf("missing=%v err=%v", missing, err)
			}
			if !reflect.DeepEqual(p.argv, []string{"tmux", "has-session", "-t", "=codex-one"}) {
				t.Fatalf("argv=%v", p.argv)
			}
		})
	}
}

func TestMissingTaskUpdateIsScopedAndFenced(t *testing.T) {
	store, mock := testStore(t)
	mock.ExpectExec("UPDATE box_tasks t SET state='failed'.*FROM logical_boxes b WHERE t.account_id=\\$1 AND t.id=\\$2 AND t.state='active'.*b.state='running' AND b.slot_id=\\$3 AND b.assignment_generation=\\$4").
		WithArgs("account-a", "task-exited", "slot-a", int64(7)).WillReturnResult(sqlmock.NewResult(0, 1))
	err := store.failMissingTask(context.Background(), "account-a", v1.BoxTask{ID: "task-exited"}, v1.LogicalBox{SlotID: "slot-a", AssignmentGeneration: 7})
	if err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
