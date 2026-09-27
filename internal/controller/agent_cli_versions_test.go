package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestAgentCLIVersionsValidateAndSelect(t *testing.T) {
	versions := AgentCLIVersions{Claude: "latest", Codex: "0.130.0", OpenCode: "1.2.3-beta.1"}
	if err := versions.validate(); err != nil {
		t.Fatal(err)
	}
	for agent, want := range map[string]string{"claude": versions.Claude, "codex": versions.Codex, "opencode": versions.OpenCode, "shell": ""} {
		if got := versions.ForAgent(agent); got != want {
			t.Fatalf("%s version=%q, want %q", agent, got, want)
		}
	}
	for _, bad := range []string{"LATEST", "2.1", "2.1.280;touch /tmp/pwned", " 2.1.280"} {
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
	server := &Server{Store: &Store{DB: db, agentCLILatestResolve: func(_ context.Context, agent string) (string, error) {
		if agent != "claude" {
			t.Fatalf("unexpected latest lookup for %s", agent)
		}
		return "2.1.281", nil
	}, agentCLIPackageVersionCheck: func(_ context.Context, agent, version string) error {
		if version == "999.999.999" {
			return fmt.Errorf("%s %s is not published to npm", agent, version)
		}
		if version == "888.888.888" {
			return errAgentCLIRegistryUnavailable
		}
		return nil
	}}}
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
	mock.ExpectExec("INSERT INTO agent_cli_versions").WithArgs("account-a", "latest", "", "").WillReturnResult(sqlmock.NewResult(0, 1))
	latest := httptest.NewRecorder()
	server.agentCLIVersionsHandler(latest, httptest.NewRequest(http.MethodPut, "/v1/agent-cli-versions", strings.NewReader(`{"claude":"latest"}`)), principal)
	if latest.Code != http.StatusOK || !strings.Contains(latest.Body.String(), `"claude":"latest"`) {
		t.Fatalf("latest PUT returned %d: %s", latest.Code, latest.Body.String())
	}
	bad := httptest.NewRecorder()
	server.agentCLIVersionsHandler(bad, httptest.NewRequest(http.MethodPut, "/v1/agent-cli-versions", strings.NewReader(`{"claude":"LATEST"}`)), principal)
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("invalid PUT returned %d: %s", bad.Code, bad.Body.String())
	}
	missing := httptest.NewRecorder()
	server.agentCLIVersionsHandler(missing, httptest.NewRequest(http.MethodPut, "/v1/agent-cli-versions", strings.NewReader(`{"claude":"999.999.999"}`)), principal)
	if missing.Code != http.StatusBadRequest || !strings.Contains(missing.Body.String(), "not published") {
		t.Fatalf("unpublished version PUT returned %d: %s", missing.Code, missing.Body.String())
	}
	unavailable := httptest.NewRecorder()
	server.agentCLIVersionsHandler(unavailable, httptest.NewRequest(http.MethodPut, "/v1/agent-cli-versions", strings.NewReader(`{"codex":"888.888.888"}`)), principal)
	if unavailable.Code != http.StatusBadGateway {
		t.Fatalf("registry outage PUT returned %d: %s", unavailable.Code, unavailable.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestAgentCLIVersionCatalogOwnerAPI(t *testing.T) {
	server := &Server{Store: &Store{agentCLIPackageCatalog: func(_ context.Context, agent string) (AgentCLIVersionCatalog, error) {
		if agent != "codex" {
			t.Fatalf("unexpected catalog lookup for %s", agent)
		}
		return AgentCLIVersionCatalog{Agent: agent, Latest: "0.130.0", Versions: []string{"0.129.0", "0.130.0"}}, nil
	}}}
	r := httptest.NewRequest(http.MethodGet, "/v1/agent-cli-versions/catalog/codex", nil)
	r.SetPathValue("agent", "codex")
	w := httptest.NewRecorder()
	server.agentCLIVersionCatalogHandler(w, r, Principal{Role: "owner"})
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"latest":"0.130.0"`) {
		t.Fatalf("catalog returned %d: %s", w.Code, w.Body.String())
	}
	r.SetPathValue("agent", "unknown")
	w = httptest.NewRecorder()
	server.agentCLIVersionCatalogHandler(w, r, Principal{Role: "owner"})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("unknown agent returned %d", w.Code)
	}
	server.Store.agentCLIPackageCatalog = func(context.Context, string) (AgentCLIVersionCatalog, error) {
		return AgentCLIVersionCatalog{}, errAgentCLIRegistryUnavailable
	}
	r.SetPathValue("agent", "codex")
	w = httptest.NewRecorder()
	server.agentCLIVersionCatalogHandler(w, r, Principal{Role: "owner"})
	if w.Code != http.StatusBadGateway {
		t.Fatalf("registry outage returned %d", w.Code)
	}
}

func TestAgentCLIVersionLookupChecksExactPublishedRelease(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.EscapedPath() {
		case "/@openai%2Fcodex/0.130.0":
			_, _ = w.Write([]byte(`{"name":"@openai/codex","version":"0.130.0"}`))
		case "/@openai%2Fcodex/latest":
			_, _ = w.Write([]byte(`{"name":"@openai/codex","version":"0.131.0"}`))
		case "/opencode-ai/1.2.3":
			_, _ = w.Write([]byte(`{"name":"opencode-ai","version":"wrong"}`))
		case "/@anthropic-ai%2Fclaude-code/999.999.999":
			http.NotFound(w, r)
		case "/unavailable/@openai%2Fcodex/0.130.0":
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
		default:
			t.Errorf("unexpected npm registry request: %s", r.URL.EscapedPath())
			http.Error(w, "unexpected", http.StatusInternalServerError)
		}
	}))
	defer server.Close()
	for _, tc := range []struct {
		agent, version string
		wantErr        string
	}{
		{"codex", "0.130.0", ""},
		{"claude", "999.999.999", "not published"},
		{"opencode", "1.2.3", "lookup unavailable"},
	} {
		err := checkAgentCLIPackageVersionAt(context.Background(), server.Client(), server.URL, tc.agent, tc.version)
		if tc.wantErr == "" && err != nil || tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
			t.Fatalf("%s %s: error=%v, want %q", tc.agent, tc.version, err, tc.wantErr)
		}
	}
	if err := checkAgentCLIPackageVersionAt(context.Background(), server.Client(), server.URL, "codex", "latest"); err == nil {
		t.Fatal("invalid version contacted registry or passed validation")
	}
	if version, err := resolveAgentCLIPackageVersionAt(context.Background(), server.Client(), server.URL, "codex", "latest"); err != nil || version != "0.131.0" {
		t.Fatalf("latest resolved to %q: %v", version, err)
	}
	if !errors.Is(checkAgentCLIPackageVersionAt(context.Background(), server.Client(), server.URL+"/unavailable", "codex", "0.130.0"), errAgentCLIRegistryUnavailable) {
		t.Fatal("registry server failure was not reported as unavailable")
	}
}

func TestAgentCLIVersionCatalogReadsAbbreviatedNpmMetadata(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() != "/@openai%2Fcodex" || r.Header.Get("Accept") != "application/vnd.npm.install-v1+json" {
			t.Errorf("unexpected catalog request: %s %s", r.URL.EscapedPath(), r.Header.Get("Accept"))
		}
		_, _ = w.Write([]byte(`{"name":"@openai/codex","dist-tags":{"latest":"0.130.0"},"versions":{"0.130.0":{"name":"@openai/codex","version":"0.130.0"},"0.129.0":{"name":"@openai/codex","version":"0.129.0"},"bad":{"name":"@openai/codex","version":"bad"}}}`))
	}))
	defer server.Close()
	catalog, err := listAgentCLIPackageVersionsAt(context.Background(), server.Client(), server.URL, "codex")
	if err != nil || catalog.Agent != "codex" || catalog.Latest != "0.130.0" || len(catalog.Versions) != 2 || catalog.Versions[0] != "0.129.0" {
		t.Fatalf("catalog=%+v error=%v", catalog, err)
	}
}

func TestAgentCLIVersionCatalogRejectsMissingLatestRelease(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"name":"@openai/codex","dist-tags":{"latest":"0.131.0"},"versions":{"0.130.0":{"name":"@openai/codex","version":"0.130.0"}}}`))
	}))
	defer server.Close()
	_, err := listAgentCLIPackageVersionsAt(context.Background(), server.Client(), server.URL, "codex")
	if !errors.Is(err, errAgentCLIRegistryUnavailable) {
		t.Fatalf("missing latest release returned %v", err)
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
