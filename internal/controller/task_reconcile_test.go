package controller

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

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

func TestCodexTaskPaneExited(t *testing.T) {
	task := v1.BoxTask{Session: "codex-one", CreatedAt: time.Now().Add(-time.Hour)}
	for _, tc := range []struct {
		name, stdout, stderr string
		code                 int
		want                 bool
	}{
		{"running", "0\tnode\n", "", 0, false},
		{"dead pane", "1\tnode\n", "", 0, true},
		{"fell back to shell", "0\tbash\n", "", 0, true},
		{"pane vanished", "", "can't find pane", 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &sessionProbeProvider{result: provider.ExecResult{Stdout: tc.stdout, Stderr: tc.stderr, ExitCode: tc.code}}
			dead, err := codexTaskPaneExited(context.Background(), p, "slot", task)
			if err != nil || dead != tc.want {
				t.Fatalf("dead=%v err=%v", dead, err)
			}
			if !reflect.DeepEqual(p.argv, []string{"tmux", "display-message", "-p", "-t", "=codex-one:0.0", "#{pane_dead}\t#{pane_current_command}"}) {
				t.Fatalf("argv=%v", p.argv)
			}
		})
	}
	task.CreatedAt = time.Now()
	p := &sessionProbeProvider{result: provider.ExecResult{Stdout: "0\tbash\n"}}
	if dead, err := codexTaskPaneExited(context.Background(), p, "slot", task); err != nil || dead {
		t.Fatalf("new task shell must not be retired: %v %v", dead, err)
	}
}
