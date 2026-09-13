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
