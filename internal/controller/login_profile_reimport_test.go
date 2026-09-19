package controller

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/secrets"
	"github.com/DATA-DOG/go-sqlmock"
)

func TestValidateBoxProfileRefsRejectsDuplicateOversizedAndUnavailable(t *testing.T) {
	s, _ := testStore(t)
	server := &Server{Store: s}
	twoAgents := []v1.LoginProfileRef{{Application: "claude", Name: "work"}, {Application: "codex", Name: "work"}}
	if err := server.validateBoxProfileRefs(context.Background(), "a", twoAgents); err == nil || !strings.Contains(err.Error(), "at most one agent profile") {
		t.Fatalf("multiple agent profiles were not rejected: %v", err)
	}
	if err := validateBoxProfileSelection([]v1.LoginProfileRef{{Application: "claude", Name: "work"}, {Application: "github", Name: "gh-work"}}); err != nil {
		t.Fatalf("one agent plus one GitHub profile was rejected: %v", err)
	}
	if err := validateBoxProfileSelection([]v1.LoginProfileRef{{Application: "github", Name: "work"}, {Application: "github", Name: "personal"}}); err == nil || !strings.Contains(err.Error(), "at most one GitHub profile") {
		t.Fatalf("multiple GitHub profiles were not rejected: %v", err)
	}
	duplicate := []v1.LoginProfileRef{{Application: "claude", Name: "work"}, {Application: "claude", Name: "work"}}
	if err := server.validateBoxProfileRefs(context.Background(), "a", duplicate); err == nil || !strings.Contains(err.Error(), "twice") {
		t.Fatalf("duplicate profile selection accepted: %v", err)
	}
	oversized := make([]v1.LoginProfileRef, 0, 9)
	for i := 0; i < 9; i++ {
		oversized = append(oversized, v1.LoginProfileRef{Application: "codex", Name: string(rune('a' + i))})
	}
	if err := server.validateBoxProfileRefs(context.Background(), "a", oversized); err == nil {
		t.Fatal("oversized profile selection accepted")
	}
	if err := server.validateBoxProfileRefs(context.Background(), "a", []v1.LoginProfileRef{{Application: "codex", Name: "work", Model: "bad\nmodel"}}); err == nil || !strings.Contains(err.Error(), "control characters") {
		t.Fatalf("invalid model was not rejected before store lookup: %v", err)
	}
	if err := server.validateBoxProfileRefs(context.Background(), "a", []v1.LoginProfileRef{{Application: "codex", Name: "work", Model: "gpt-5.6-sol", ReasoningEffort: "ultra"}}); err == nil || !strings.Contains(err.Error(), "reasoning effort") {
		t.Fatalf("invalid reasoning effort was not rejected before store lookup: %v", err)
	}
	if err := server.validateBoxProfileRefs(context.Background(), "a", []v1.LoginProfileRef{{Application: "codex", Name: "work", ReasoningEffort: "high"}}); err == nil || !strings.Contains(err.Error(), "choose a model") {
		t.Fatalf("reasoning effort without model was not rejected: %v", err)
	}
	// No encryption envelope: the profile cannot be loaded, so it must be rejected.
	if err := server.validateBoxProfileRefs(context.Background(), "a", []v1.LoginProfileRef{{Application: "codex", Name: "work"}}); err == nil {
		t.Fatal("unavailable profile accepted")
	}
}

func TestProfileSyncRequestAppliesPerBoxReasoningEffort(t *testing.T) {
	store, mock := testStore(t)
	var err error
	store.Envelope, err = secrets.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	profile := v1.SaveLoginProfileRequest{Files: map[string][]byte{
		"auth.json":   []byte(`{"OPENAI_API_KEY":"synthetic-only"}`),
		"config.toml": []byte("model = \"gpt-5.6-sol\"\n[projects.x]\ntrust_level = \"trusted\"\n"),
	}}
	plain, _ := json.Marshal(profile)
	sealed, err := store.Envelope.Seal(profileEncryptionScope("a", "codex", "work"), plain)
	if err != nil {
		t.Fatal(err)
	}
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT encrypted_value FROM login_profiles").WithArgs("a", "codex", "work").WillReturnRows(sqlmock.NewRows([]string{"encrypted_value"}).AddRow(sealed))
	tx, err := store.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{Store: store}
	request, _, _, err := server.profileSyncRequest(context.Background(), tx, "a", []v1.LoginProfileRef{{Application: "codex", Name: "work", Model: "gpt-5.6-terra", ReasoningEffort: "high"}})
	if err != nil {
		t.Fatal(err)
	}
	var got string
	for _, file := range request.Files {
		if file.Path == "/data/home/.codex/config.toml" {
			got = string(file.Data)
		}
	}
	if !strings.Contains(got, "model = \"gpt-5.6-terra\"") || !strings.Contains(got, "model_reasoning_effort = \"high\"\n[projects.x]") {
		t.Fatalf("per-box model and effort were not provisioned together: %s", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCreateLogicalBoxRejectsMultipleAgentProfiles(t *testing.T) {
	s, _ := testStore(t)
	server := &Server{Store: s}
	request := httptest.NewRequest(http.MethodPost, "/v1/logical-boxes", strings.NewReader(`{
		"name":"one-profile-only",
		"loginProfiles":[
			{"application":"claude","name":"work"},
			{"application":"codex","name":"work"}
		]
	}`))
	response := httptest.NewRecorder()

	server.createLogicalBoxHandler(response, request, Principal{AccountID: "a", UserID: "u", Role: "owner"})

	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "at most one agent profile") {
		t.Fatalf("multiple profile creation status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestBoxLoginProfileStateReadsImportedPendingAndVerified(t *testing.T) {
	s, m := testStore(t)
	imported, _ := json.Marshal([]v1.LoginProfileRef{{Application: "codex", Name: "work"}})
	pending, _ := json.Marshal([]v1.LoginProfileRef{{Application: "claude", Name: "personal"}})
	m.ExpectQuery(`SELECT COALESCE\(metadata->'importedLoginProfiles'`).WithArgs("a", "box-1").
		WillReturnRows(sqlmock.NewRows([]string{"imported", "verified", "pending"}).AddRow(imported, true, pending))
	server := &Server{Store: s}
	state, err := server.boxLoginProfileState(context.Background(), "a", "box-1")
	if err != nil || !state.Verified || len(state.Imported) != 1 || state.Imported[0].Name != "work" || len(state.Pending) != 1 || state.Pending[0].Application != "claude" {
		t.Fatalf("state projection: %v %+v", err, state)
	}
	// A missing box is an error, never fabricated state.
	m.ExpectQuery(`SELECT COALESCE\(metadata->'importedLoginProfiles'`).WithArgs("a", "ghost").WillReturnError(sql.ErrNoRows)
	if _, err := server.boxLoginProfileState(context.Background(), "a", "ghost"); err == nil {
		t.Fatal("missing box reported credential state")
	}
	if err := m.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestProvisionPendingBoxProfilesSkipsWithoutPending(t *testing.T) {
	s, m := testStore(t)
	m.ExpectQuery(`SELECT COALESCE\(metadata->'importedLoginProfiles'`).WithArgs("a", "box-1").
		WillReturnRows(sqlmock.NewRows([]string{"imported", "verified", "pending"}).AddRow([]byte(`[]`), true, []byte(`[]`)))
	server := &Server{Store: s}
	if err := server.provisionPendingBoxProfiles(context.Background(), nil, "a", fleetAssignment{Box: v1.LogicalBox{ID: "box-1", AccountID: "a"}}); err != nil {
		t.Fatalf("no pending selection must be a no-op: %v", err)
	}
	if err := m.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestPutBoxLoginProfilesRejectsUnknownBox(t *testing.T) {
	s, m := testStore(t)
	m.ExpectQuery(`SELECT id::text,account_id::text,owner_user_id::text,name,provider`).WithArgs("a", "ghost").
		WillReturnError(sql.ErrNoRows)
	server := &Server{Store: s}
	request := httptest.NewRequest(http.MethodPut, "/", strings.NewReader(`{"profiles":[{"application":"codex","name":"work"}]}`))
	request.SetPathValue("id", "ghost")
	response := httptest.NewRecorder()
	server.putBoxLoginProfiles(response, request, Principal{AccountID: "a", UserID: "u", Role: "owner"})
	if response.Code != http.StatusNotFound {
		t.Fatalf("unknown box status=%d body=%s", response.Code, response.Body.String())
	}
	if err := m.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestPutBoxLoginProfilesRejectsInvalidPayload(t *testing.T) {
	s, _ := testStore(t)
	server := &Server{Store: s}
	request := httptest.NewRequest(http.MethodPut, "/", strings.NewReader(`{"profiles":"not-a-list"}`))
	request.SetPathValue("id", "box-1")
	response := httptest.NewRecorder()
	server.putBoxLoginProfiles(response, request, Principal{AccountID: "a", UserID: "u", Role: "owner"})
	if response.Code != http.StatusBadRequest {
		t.Fatalf("invalid payload status=%d", response.Code)
	}
}

func TestSelectedAgentProfileControlsBoxHarness(t *testing.T) {
	if got := selectedProfileAgent("claude", []v1.LoginProfileRef{{Application: "codex", Name: "work"}}); got != "codex" {
		t.Fatalf("Codex profile left box on %q", got)
	}
	if got := selectedProfileAgent("claude", []v1.LoginProfileRef{{Application: "codex", Name: "work"}, {Application: "github", Name: "gh-work"}}); got != "codex" {
		t.Fatalf("GitHub profile prevented Codex from selecting the box harness: %q", got)
	}
	if got := selectedProfileAgent("claude", []v1.LoginProfileRef{{Application: "github", Name: "gh-work"}, {Application: "codex", Name: "work"}}); got != "codex" {
		t.Fatalf("profile order changed the selected harness: %q", got)
	}
	if got := selectedProfileAgent("opencode", nil); got != "opencode" {
		t.Fatalf("clearing a profile unexpectedly changed harness to %q", got)
	}
	if got := selectedProfileAgent("claude", []v1.LoginProfileRef{{Application: "github", Name: "work"}}); got != "claude" {
		t.Fatalf("non-agent profile unexpectedly changed harness to %q", got)
	}
}

func TestFailedProfileApplyRestoresPreviousSelection(t *testing.T) {
	requested := []v1.LoginProfileRef{{Application: "claude", Name: "expired"}}
	previous := []v1.LoginProfileRef{{Application: "codex", Name: "working"}}
	providerFailure := errors.New("provider rejected profile")
	var calls [][]v1.LoginProfileRef
	err := applyBoxLoginProfilesWithRollback(requested, previous, func(refs []v1.LoginProfileRef) error {
		calls = append(calls, append([]v1.LoginProfileRef(nil), refs...))
		if len(calls) == 1 {
			return providerFailure
		}
		return nil
	})
	if !errors.Is(err, providerFailure) {
		t.Fatalf("original profile error was lost: %v", err)
	}
	if len(calls) != 2 || calls[0][0].Application != "claude" || calls[1][0].Application != "codex" {
		t.Fatalf("profile apply sequence = %+v", calls)
	}
}

func TestProfileSyncRemovesOtherHarnessCredentials(t *testing.T) {
	written := map[string]bool{"/data/home/.codex/auth.json": true}
	removed := profileRemovalPaths(written)
	for _, path := range []string{
		"/data/home/.claude/.credentials.json",
		"/data/home/.claude/settings.json",
		"/data/home/.claude.json",
		"/data/home/.codex/config.toml",
		"/data/home/.local/share/opencode/auth.json",
		"/data/home/.config/opencode/opencode.json",
		"/data/home/.config/opencode/opencode.jsonc",
	} {
		if !removed[path] {
			t.Fatalf("stale profile path %s would survive", path)
		}
	}
	if removed["/data/home/.codex/auth.json"] {
		t.Fatal("selected credential would be removed after writing")
	}
}
