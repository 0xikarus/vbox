package controller

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/boxruntime"
	"github.com/0xikarus/vmbox-service/internal/provider"
	"github.com/DATA-DOG/go-sqlmock"
)

type desktopProvisionFixture struct {
	provider.Provider
	t       *testing.T
	enabled bool
	writes  int
	config  boxruntime.DesktopAgentConfig
}

type sharedChatRecoveryFixture struct {
	fakeProvider
	t         *testing.T
	recovered bool
	started   bool
}

func (p *sharedChatRecoveryFixture) Connection(context.Context, string) (provider.Connection, error) {
	return provider.Connection{Transport: "shared-worker", Endpoint: "shared-slot-1", Metadata: map[string]string{
		"accountId": "account", "boxId": "research", "workspaceId": "volume-1", "deploymentInstanceId": "current-worker",
	}}, nil
}

func (p *sharedChatRecoveryFixture) Start(context.Context, string) (provider.Box, error) {
	p.started = true
	return provider.Box{}, nil
}

func (p *sharedChatRecoveryFixture) Exec(_ context.Context, _ string, args []string, _ provider.ExecOptions) (provider.ExecResult, error) {
	if len(args) < 2 {
		p.t.Fatalf("unexpected worker command: %v", args)
	}
	switch args[1] {
	case "tmux-restore":
		if !p.started {
			p.t.Fatal("tmux restored before the shared workspace account")
		}
	case "native-bind":
		p.recovered = true
	case "native-sessions":
		if !p.recovered {
			return provider.ExecResult{}, fmt.Errorf("native server is not restored")
		}
	case "desktop-status":
		if !p.recovered {
			return provider.ExecResult{}, fmt.Errorf("desktop probe ran before shared runtime recovery")
		}
		return provider.ExecResult{Stdout: `{"enabled":false}`}, nil
	case "tmux-task":
		if !p.recovered {
			return provider.ExecResult{}, fmt.Errorf("task started before shared runtime recovery")
		}
	default:
		p.t.Fatalf("unexpected worker command: %v", args)
	}
	return provider.ExecResult{}, nil
}

func (p *desktopProvisionFixture) Exec(_ context.Context, _ string, args []string, opts provider.ExecOptions) (provider.ExecResult, error) {
	if len(args) > 1 && args[1] == "desktop-status" {
		return provider.ExecResult{Stdout: fmt.Sprintf(`{"enabled":%t}`, p.enabled)}, nil
	}
	if len(args) != 2 || args[1] != "sync-files" {
		p.t.Fatal("unexpected worker command")
	}
	data, err := io.ReadAll(opts.Stdin)
	if err != nil {
		return provider.ExecResult{}, err
	}
	defer clear(data)
	var request boxruntime.SyncRequest
	if json.Unmarshal(data, &request) != nil || len(request.Files) != 1 {
		p.t.Fatal("unexpected credential payload")
	}
	file := request.Files[0]
	if file.Path != "/data/home/.config/vmbox/desktop-agent.json" || file.Mode != "0600" {
		p.t.Fatal("credential destination/mode")
	}
	if json.Unmarshal(file.Data, &p.config) != nil {
		p.t.Fatal("invalid config")
	}
	if strings.Contains(strings.Join(args, " "), p.config.Token) {
		p.t.Fatal("credential in argv")
	}
	p.writes++
	return provider.ExecResult{Stdout: fmt.Sprintf("%x", sha256.Sum256(data))}, nil
}
func TestDesktopAgentProvisionPrivateConfigAndShellOnlySkip(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatal(err)
		}
		mock.ExpectBegin()
		tx, err := db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		if enabled {
			mock.ExpectExec("INSERT INTO desktop_agent_tokens").WithArgs("account", "box", "user", "fence", sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 1))
		}
		fixture := &desktopProvisionFixture{t: t, enabled: enabled}
		s := NewServer(&Store{DB: db}, nil)
		s.PublicURL = "https://controller.test"
		a := fleetAssignment{}
		a.Box.ID = "box"
		a.FencingToken = "fence"
		if err = s.provisionDesktopAgent(context.Background(), tx, Principal{AccountID: "account", UserID: "user"}, a, fixture); err != nil {
			t.Fatal(err)
		}
		if enabled && (fixture.writes != 1 || len(fixture.config.Token) != 64 || fixture.config.Assignment != nativeFence(a)) {
			t.Fatal("private configuration incomplete")
		}
		if !enabled && fixture.writes != 0 {
			t.Fatal("shell-only worker received credential")
		}
		mock.ExpectRollback()
		tx.Rollback()
		if err = mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
		db.Close()
	}
}

func TestProvisionAssignedDesktopAgentCommitsCurrentCredential(t *testing.T) {
	for _, state := range []string{"attaching", "running"} {
		t.Run(state, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			mock.ExpectBegin()
			mock.ExpectQuery("SELECT owner_user_id::text FROM logical_boxes").WithArgs("account", "box", state, int64(2), "fence").WillReturnRows(sqlmock.NewRows([]string{"owner_user_id"}).AddRow("user"))
			mock.ExpectExec("INSERT INTO desktop_agent_tokens").WithArgs("account", "box", "user", "fence", sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectCommit()
			fixture := &desktopProvisionFixture{t: t, enabled: true}
			s := NewServer(&Store{DB: db}, nil)
			s.PublicURL = "https://controller.test"
			a := fleetAssignment{FencingToken: "fence"}
			a.Box = v1.LogicalBox{ID: "box", AccountID: "account", AssignmentGeneration: 2}
			if err := s.provisionAssignedDesktopAgent(context.Background(), a, fixture, state); err != nil {
				t.Fatal(err)
			}
			if fixture.writes != 1 || fixture.config.Assignment != nativeFence(a) {
				t.Fatal("current assignment credential was not installed")
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestChatStartupRejectsStaleAssignmentBeforeWorkerAccess(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT id::text FROM logical_boxes").WithArgs("box", "account", "stale", int64(2)).WillReturnError(sql.ErrNoRows)
	mock.ExpectRollback()
	s := NewServer(&Store{DB: db}, nil)
	a := fleetAssignment{}
	a.Box.ID = "box"
	a.Box.AssignmentGeneration = 2
	a.FencingToken = "stale"
	// A nil provider deliberately fails if the stale request reaches transport.
	_, err = s.startAssignedBoxTaskRuntime(context.Background(), Principal{AccountID: "account"}, nil, a, v1.BoxTask{}, v1.BoxMessage{})
	if err == nil {
		t.Fatal("stale task startup accepted")
	}
	if err = mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestChatStartupRecoversReplacedSharedWorkerBeforeDesktopProbe(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	assignment := fleetAssignment{FencingToken: "fence"}
	assignment.Box = v1.LogicalBox{
		ID: "box", AccountID: "account", Name: "research", Provider: "shared-worker",
		State: v1.LogicalBoxRunning, VolumeID: "volume-1", SlotID: "slot", AssignmentGeneration: 3,
	}
	assignment.Slot = v1.ComputeSlot{
		ID: "slot", ServiceID: "shared-slot-1", State: v1.FleetSlotOccupied,
		DeploymentInstanceID: "previous-worker", AssignmentGeneration: 3,
	}

	// Recover the retained workspace under its current shared-worker incarnation.
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT assignment_generation FROM logical_boxes").
		WithArgs("account", "box", "slot", "fence").
		WillReturnRows(sqlmock.NewRows([]string{"assignment_generation"}).AddRow(int64(3)))
	mock.ExpectQuery("SELECT COALESCE\\(deployment_instance_id").
		WithArgs("account", "slot", int64(3), "fence").
		WillReturnRows(sqlmock.NewRows([]string{"deployment_instance_id"}).AddRow("previous-worker"))
	mock.ExpectExec("UPDATE compute_slots SET deployment_instance_id").
		WithArgs("account", "slot", "current-worker").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	// Only after recovery may task startup probe desktop capability and launch.
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT id::text FROM logical_boxes").
		WithArgs("box", "account", "fence", int64(3)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("box"))
	// The credential transaction must finish before the worker starts the task:
	// Codex reads its MCP policy through that newly issued credential.
	mock.ExpectCommit()
	created := time.Now().UTC()
	mock.ExpectQuery("SELECT created_at FROM box_messages").
		WithArgs("account", "message").
		WillReturnRows(sqlmock.NewRows([]string{"created_at"}).AddRow(created))
	mock.ExpectQuery("SELECT count\\(\\*\\) FROM box_messages").
		WithArgs("account", "task", created, "message").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectQuery("SELECT i.id::text,j.ordinal,i.download_token").
		WithArgs("account", "message").
		WillReturnRows(sqlmock.NewRows([]string{"id", "ordinal", "download_token"}))
	mock.ExpectExec("UPDATE logical_boxes SET metadata=").
		WithArgs("account", "box", "shell-session", "shell", "fence", int64(3)).
		WillReturnResult(sqlmock.NewResult(0, 1))

	fixture := &sharedChatRecoveryFixture{t: t}
	server := NewServer(&Store{DB: db}, nil)
	task := v1.BoxTask{ID: "task", Session: "shell-session", Agent: "shell"}
	message := v1.BoxMessage{ID: "message", TaskID: task.ID, Text: "printf hello"}
	if _, err := server.startAssignedBoxTaskRuntime(context.Background(), Principal{AccountID: "account"}, fixture, assignment, task, message); err != nil {
		t.Fatal(err)
	}
	if !fixture.started || !fixture.recovered {
		t.Fatalf("shared runtime was not recovered: started=%t recovered=%t", fixture.started, fixture.recovered)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
