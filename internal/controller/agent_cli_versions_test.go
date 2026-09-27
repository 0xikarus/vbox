package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestAgentCLIVersionsValidateAndSelect(t *testing.T) {
	versions := AgentCLIVersions{Claude: "2.1.280", Codex: "0.130.0", OpenCode: "1.2.3-beta.1"}
	if err := versions.validate(); err != nil {
		t.Fatal(err)
	}
	for agent, want := range map[string]string{"claude": versions.Claude, "codex": versions.Codex, "opencode": versions.OpenCode, "shell": ""} {
		if got := versions.ForAgent(agent); got != want {
			t.Fatalf("%s version=%q, want %q", agent, got, want)
		}
	}
	for _, bad := range []string{"latest", "2.1", "2.1.280;touch /tmp/pwned", " 2.1.280"} {
		if err := (AgentCLIVersions{Claude: bad}).validate(); err == nil {
			t.Fatalf("accepted invalid version %q", bad)
		}
	}
}

func TestAgentCLIVersionsOwnerAPI(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	server := &Server{Store: &Store{DB: db}}
	principal := Principal{AccountID: "account-a", UserID: "user-a", Role: "owner"}
	mock.ExpectQuery("SELECT claude_version,codex_version,opencode_version FROM agent_cli_versions").WithArgs("account-a").WillReturnRows(sqlmock.NewRows([]string{"claude_version", "codex_version", "opencode_version"}))
	get := httptest.NewRecorder()
	server.agentCLIVersionsHandler(get, httptest.NewRequest(http.MethodGet, "/v1/agent-cli-versions", nil), principal)
	if get.Code != http.StatusOK || !strings.Contains(get.Body.String(), `"claude":""`) {
		t.Fatalf("GET returned %d: %s", get.Code, get.Body.String())
	}
	input := AgentCLIVersions{Claude: "2.1.280", Codex: "0.130.0", OpenCode: "1.2.3"}
	encoded, _ := json.Marshal(input)
	mock.ExpectExec("INSERT INTO agent_cli_versions").WithArgs("account-a", input.Claude, input.Codex, input.OpenCode).WillReturnResult(sqlmock.NewResult(0, 1))
	put := httptest.NewRecorder()
	server.agentCLIVersionsHandler(put, httptest.NewRequest(http.MethodPut, "/v1/agent-cli-versions", bytes.NewReader(encoded)), principal)
	if put.Code != http.StatusOK {
		t.Fatalf("PUT returned %d: %s", put.Code, put.Body.String())
	}
	bad := httptest.NewRecorder()
	server.agentCLIVersionsHandler(bad, httptest.NewRequest(http.MethodPut, "/v1/agent-cli-versions", strings.NewReader(`{"claude":"latest"}`)), principal)
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("invalid PUT returned %d: %s", bad.Code, bad.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestAgentCLIVersionsStoreNoSetting(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectQuery("SELECT claude_version,codex_version,opencode_version FROM agent_cli_versions").WithArgs("account-a").WillReturnRows(sqlmock.NewRows([]string{"claude_version", "codex_version", "opencode_version"}))
	got, err := (&Store{DB: db}).AgentCLIVersions(context.Background(), "account-a")
	if err != nil || got != (AgentCLIVersions{}) {
		t.Fatalf("versions=%+v, error=%v", got, err)
	}
}
