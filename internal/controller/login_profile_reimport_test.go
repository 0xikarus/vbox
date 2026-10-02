package controller

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
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

func TestLoginProfileListExposesOnlyAccountIdentity(t *testing.T) {
	claims := base64.RawURLEncoding.EncodeToString([]byte(`{"email":"codex@example.test"}`))
	for _, tc := range []struct {
		app, name string
		files     map[string][]byte
		email     string
		host      string
		user      string
	}{
		{"claude", "work", map[string][]byte{".claude.json": []byte(`{"oauthAccount":{"emailAddress":"claude@example.test"},"token":"synthetic-secret"}`)}, "claude@example.test", "", ""},
		{"codex", "work", map[string][]byte{"auth.json": []byte(`{"tokens":{"id_token":"x.` + claims + `.y","access_token":"synthetic-secret"}}`)}, "codex@example.test", "", ""},
		{"github", "work", map[string][]byte{"credential.json": []byte(`{"host":"github.com","user":"synthetic-user","token":"synthetic-secret"}`)}, "", "github.com", "synthetic-user"},
	} {
		t.Run(tc.app, func(t *testing.T) {
			profile := v1.LoginProfile{Application: tc.app, Name: tc.name}
			profilePublicMetadata(&profile, tc.files)
			if profile.Email != tc.email || profile.Host != tc.host || profile.User != tc.user {
				t.Fatalf("public identity=%+v", profile)
			}
			encoded, _ := json.Marshal(profile)
			if strings.Contains(string(encoded), "synthetic-secret") {
				t.Fatal("credential leaked into public metadata")
			}
		})
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
		WillReturnRows(sqlmock.NewRows([]string{"imported", "verified", "pending", "pending_set"}).AddRow(imported, true, pending, true))
	server := &Server{Store: s}
	state, err := server.boxLoginProfileState(context.Background(), "a", "box-1")
	if err != nil || !state.Verified || !state.PendingSet || len(state.Imported) != 1 || state.Imported[0].Name != "work" || len(state.Pending) != 1 || state.Pending[0].Application != "claude" {
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
		WillReturnRows(sqlmock.NewRows([]string{"imported", "verified", "pending", "pending_set"}).AddRow([]byte(`[]`), true, []byte(`[]`), false))
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

func sealTestProfile(t *testing.T, store *Store, app, name string, files map[string][]byte) string {
	t.Helper()
	plain, err := json.Marshal(v1.SaveLoginProfileRequest{Files: files})
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := store.Envelope.Seal(profileEncryptionScope("a", app, name), plain)
	clear(plain)
	if err != nil {
		t.Fatal(err)
	}
	return sealed
}

func TestProfileSyncKeepsTheOtherCredentialSlot(t *testing.T) {
	agent := v1.LoginProfileRef{Application: "codex", Name: "work", Model: "gpt-5.6-sol", ReasoningEffort: "high"}
	for _, tc := range []struct {
		name       string
		refs       []v1.LoginProfileRef
		keepGitHub bool
	}{
		{"change agent keeping GitHub", []v1.LoginProfileRef{{Application: "codex", Name: "new"}, {Application: "github", Name: "work"}}, true},
		{"change GitHub keeping agent model", []v1.LoginProfileRef{agent, {Application: "github", Name: "personal"}}, true},
		{"remove GitHub keeping agent", []v1.LoginProfileRef{agent}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, mock := testStore(t)
			var err error
			store.Envelope, err = secrets.New(make([]byte, 32))
			if err != nil {
				t.Fatal(err)
			}
			mock.ExpectBegin()
			for _, ref := range tc.refs {
				files := map[string][]byte{"auth.json": []byte(`{"OPENAI_API_KEY":"synthetic-only"}`), "config.toml": []byte("model = \"gpt-5.6-sol\"\n")}
				if ref.Application == "github" {
					files = map[string][]byte{"credential.json": []byte(`{"host":"github.com","user":"synthetic","token":"synthetic-only"}`)}
				}
				sealed := sealTestProfile(t, store, ref.Application, ref.Name, files)
				mock.ExpectQuery("SELECT encrypted_value FROM login_profiles").WithArgs("a", ref.Application, ref.Name).
					WillReturnRows(sqlmock.NewRows([]string{"encrypted_value"}).AddRow(sealed))
			}
			tx, err := store.DB.Begin()
			if err != nil {
				t.Fatal(err)
			}
			request, _, _, err := (&Server{Store: store}).profileSyncRequest(context.Background(), tx, "a", tc.refs)
			if err != nil {
				t.Fatal(err)
			}
			written := map[string]bool{}
			for _, file := range request.Files {
				written[file.Path] = true
			}
			if !written["/data/home/.codex/auth.json"] || !written["/data/home/.codex/config.toml"] {
				t.Fatalf("agent files missing: %+v", written)
			}
			if slices.Contains(request.Remove, "/data/home/.codex/auth.json") || slices.Contains(request.Remove, "/data/home/.codex/config.toml") {
				t.Fatalf("agent files removed: %+v", request.Remove)
			}
			githubPath := "/data/home/.config/gh/hosts.yml"
			if written[githubPath] != tc.keepGitHub || slices.Contains(request.Remove, githubPath) == tc.keepGitHub {
				t.Fatalf("GitHub slot transfer wrong: written=%v removed=%v", written[githubPath], request.Remove)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestProfileSyncTransfersOnlyChangedSlot(t *testing.T) {
	agent := v1.LoginProfileRef{Application: "codex", Name: "work", Model: "gpt-5.6-sol", ReasoningEffort: "high"}
	previous := []v1.LoginProfileRef{agent, {Application: "github", Name: "work"}}
	for _, tc := range []struct {
		name      string
		requested []v1.LoginProfileRef
		slot      string
		write     string
		remove    string
	}{
		{"change agent", []v1.LoginProfileRef{{Application: "codex", Name: "new"}, previous[1]}, "agent", "/data/home/.codex/auth.json", "/data/home/.claude/.credentials.json"},
		{"change GitHub", []v1.LoginProfileRef{agent, {Application: "github", Name: "personal"}}, "github", "/data/home/.config/gh/hosts.yml", ""},
		{"remove GitHub", []v1.LoginProfileRef{agent}, "github", "", "/data/home/.config/gh/hosts.yml"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, mock := testStore(t)
			var err error
			store.Envelope, err = secrets.New(make([]byte, 32))
			if err != nil {
				t.Fatal(err)
			}
			slots := changedBoxProfileSlots(previous, tc.requested)
			if len(slots) != 1 || !slots[tc.slot] {
				t.Fatalf("wrong changed slots: %v", slots)
			}
			mock.ExpectBegin()
			for _, ref := range tc.requested {
				if !slots[profileRefSlot(ref)] {
					continue
				}
				files := map[string][]byte{"auth.json": []byte(`{"OPENAI_API_KEY":"synthetic-only"}`), "config.toml": []byte("model = \"gpt-5.6-sol\"\n")}
				if ref.Application == "github" {
					files = map[string][]byte{"credential.json": []byte(`{"host":"github.com","user":"synthetic","token":"synthetic-only"}`)}
				}
				sealed := sealTestProfile(t, store, ref.Application, ref.Name, files)
				mock.ExpectQuery("SELECT encrypted_value FROM login_profiles").WithArgs("a", ref.Application, ref.Name).
					WillReturnRows(sqlmock.NewRows([]string{"encrypted_value"}).AddRow(sealed))
			}
			tx, err := store.DB.Begin()
			if err != nil {
				t.Fatal(err)
			}
			request, _, _, err := (&Server{Store: store}).profileSyncRequestForSlots(context.Background(), tx, "a", tc.requested, slots)
			if err != nil {
				t.Fatal(err)
			}
			written := map[string]bool{}
			for _, file := range request.Files {
				written[file.Path] = true
				if profilePathSlot(file.Path) != tc.slot {
					t.Fatalf("unrelated slot file written: %s", file.Path)
				}
			}
			if tc.write != "" && !written[tc.write] {
				t.Fatalf("expected file missing: %s", tc.write)
			}
			if tc.remove != "" && !slices.Contains(request.Remove, tc.remove) {
				t.Fatalf("expected removal missing: %s", tc.remove)
			}
			for _, path := range request.Remove {
				if profilePathSlot(path) != tc.slot {
					t.Fatalf("unrelated slot file removed: %s", path)
				}
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestApplyBoxLoginProfilesChangesOneSlotWithoutTouchingOther(t *testing.T) {
	agent := v1.LoginProfileRef{Application: "codex", Name: "work", Model: "gpt-5.6-sol", ReasoningEffort: "high"}
	previous := []v1.LoginProfileRef{agent, {Application: "github", Name: "work"}}
	for _, tc := range []struct {
		name      string
		requested []v1.LoginProfileRef
		slot      string
	}{
		{"agent", []v1.LoginProfileRef{{Application: "codex", Name: "new"}, previous[1]}, "agent"},
		{"GitHub", []v1.LoginProfileRef{agent, {Application: "github", Name: "personal"}}, "github"},
		{"remove GitHub", []v1.LoginProfileRef{agent}, "github"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, mock := testStore(t)
			var err error
			store.Envelope, err = secrets.New(make([]byte, 32))
			if err != nil {
				t.Fatal(err)
			}
			slots := changedBoxProfileSlots(previous, tc.requested)
			mock.ExpectBegin()
			mock.ExpectQuery(`SELECT id::text FROM logical_boxes`).WithArgs("a", "box-1", "slot", int64(1), "fence", "volume").
				WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("box-1"))
			for _, ref := range tc.requested {
				if !slots[profileRefSlot(ref)] {
					continue
				}
				files := map[string][]byte{"auth.json": []byte(`{"OPENAI_API_KEY":"synthetic-only"}`), "config.toml": []byte("model = \"gpt-5.6-sol\"\n")}
				if ref.Application == "github" {
					files = map[string][]byte{"credential.json": []byte(`{"host":"github.com","user":"synthetic","token":"synthetic-only"}`)}
				}
				sealed := sealTestProfile(t, store, ref.Application, ref.Name, files)
				mock.ExpectQuery("SELECT encrypted_value FROM login_profiles").WithArgs("a", ref.Application, ref.Name).
					WillReturnRows(sqlmock.NewRows([]string{"encrypted_value"}).AddRow(sealed))
			}
			encoded, _ := json.Marshal(tc.requested)
			mock.ExpectExec(`UPDATE logical_boxes SET default_agent`).WithArgs("a", "box-1", encoded, "codex").
				WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectCommit()
			transport := &profileTestTransport{volume: "volume"}
			assignment := fleetAssignment{Box: v1.LogicalBox{ID: "box-1", AccountID: "a", DefaultAgent: "codex", VolumeID: "volume", AssignmentGeneration: 1}, Slot: v1.ComputeSlot{ID: "slot", ServiceID: "service"}, FencingToken: "fence"}
			if err := (&Server{Store: store}).applyBoxLoginProfiles(context.Background(), transport, assignment, tc.requested, slots, false); err != nil {
				t.Fatalf("apply %s: %v", tc.name, err)
			}
			if transport.writes != 1 {
				t.Fatalf("transfer count=%d", transport.writes)
			}
			for _, file := range transport.files {
				if profilePathSlot(file.Path) != tc.slot {
					t.Fatalf("unrelated file written: %s", file.Path)
				}
			}
			for _, path := range transport.removes {
				if profilePathSlot(path) != tc.slot {
					t.Fatalf("unrelated file removed: %s", path)
				}
			}
			if tc.name == "remove GitHub" && !slices.Contains(transport.removes, "/data/home/.config/gh/hosts.yml") {
				t.Fatal("GitHub hosts file was not removed")
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestGitHubOnlyReplacementRollbackKeepsAgentRef(t *testing.T) {
	agent := v1.LoginProfileRef{Application: "codex", Name: "work", Model: "gpt-5.6-sol", ReasoningEffort: "high"}
	previous := []v1.LoginProfileRef{agent, {Application: "github", Name: "work"}}
	requested := []v1.LoginProfileRef{agent, {Application: "github", Name: "personal"}}
	if agentProfileChanged(previous, requested) || agentProfileChanged(previous, previous[:1]) {
		t.Fatal("GitHub-only changes would restart agent sessions")
	}
	var calls [][]v1.LoginProfileRef
	err := applyBoxLoginProfilesWithRollback(requested, previous, func(refs []v1.LoginProfileRef) error {
		calls = append(calls, slices.Clone(refs))
		if len(calls) == 1 {
			return errors.New("rejected")
		}
		return nil
	})
	if err == nil || len(calls) != 2 || !slices.Equal(calls[0], requested) || !slices.Equal(calls[1], previous) {
		t.Fatalf("GitHub rollback did not restore full previous refs: %v %+v", err, calls)
	}
}

func TestHibernatedGitHubRemovalIsSavedAndAppliedOnNextStart(t *testing.T) {
	store, mock := testStore(t)
	var err error
	store.Envelope, err = secrets.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	agent := v1.LoginProfileRef{Application: "codex", Name: "work", Model: "gpt-5.6-sol", ReasoningEffort: "high"}
	previous, _ := json.Marshal([]v1.LoginProfileRef{agent, {Application: "github", Name: "work"}})
	pending, _ := json.Marshal([]v1.LoginProfileRef{agent})
	stateQuery := func(pendingJSON []byte, pendingSet bool) {
		mock.ExpectQuery(`SELECT COALESCE\(metadata->'importedLoginProfiles'`).WithArgs("a", "box-1").
			WillReturnRows(sqlmock.NewRows([]string{"imported", "verified", "pending", "pending_set"}).AddRow(previous, true, pendingJSON, pendingSet))
	}
	mock.ExpectQuery(`SELECT id::text,account_id::text,owner_user_id::text,name,provider`).WithArgs("a", "box-1").
		WillReturnRows(logicalBoxRows("box-1", "hibernated"))
	stateQuery([]byte(`[]`), false)
	mock.ExpectExec(`UPDATE logical_boxes SET default_agent`).WithArgs("a", "box-1", pending, "codex").
		WillReturnResult(sqlmock.NewResult(0, 1))
	stateQuery(pending, true)
	server := &Server{Store: store}
	request := httptest.NewRequest(http.MethodPut, "/", strings.NewReader(`{"profiles":[{"application":"codex","name":"work","model":"gpt-5.6-sol","reasoningEffort":"high"}]}`))
	request.SetPathValue("id", "box-1")
	response := httptest.NewRecorder()
	server.putBoxLoginProfiles(response, request, Principal{AccountID: "a", UserID: "u", Role: "owner"})
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"pendingSet":true`) {
		t.Fatalf("hibernated selection was not saved: status=%d body=%s", response.Code, response.Body.String())
	}

	stateQuery(pending, true)
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT id::text FROM logical_boxes`).WithArgs("a", "box-1", "slot", int64(1), "fence", "volume").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("box-1"))
	mock.ExpectExec(`UPDATE logical_boxes SET default_agent`).WithArgs("a", "box-1", pending, "codex").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	transport := &profileTestTransport{volume: "volume"}
	assignment := fleetAssignment{Box: v1.LogicalBox{ID: "box-1", AccountID: "a", DefaultAgent: "codex", VolumeID: "volume", AssignmentGeneration: 1}, Slot: v1.ComputeSlot{ID: "slot", ServiceID: "service"}, FencingToken: "fence"}
	if err := server.provisionPendingBoxProfiles(context.Background(), transport, "a", assignment); err != nil {
		t.Fatalf("pending selection was not applied on wake: %v", err)
	}
	if transport.writes != 1 || len(transport.files) != 0 || !slices.Contains(transport.removes, "/data/home/.config/gh/hosts.yml") || slices.Contains(transport.removes, "/data/home/.codex/auth.json") {
		t.Fatalf("wrong wake transfer: writes=%d files=%v remove=%v", transport.writes, transport.files, transport.removes)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestEmptyPendingSelectionStillProvisionsOnWake(t *testing.T) {
	store, mock := testStore(t)
	mock.ExpectQuery(`SELECT COALESCE\(metadata->'importedLoginProfiles'`).WithArgs("a", "box-1").
		WillReturnRows(sqlmock.NewRows([]string{"imported", "verified", "pending", "pending_set"}).
			AddRow(`[{"application":"github","name":"work"}]`, true, `[]`, true))
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT id::text FROM logical_boxes`).WithArgs("a", "box-1", "slot", int64(1), "fence", "volume").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("box-1"))
	mock.ExpectExec(`UPDATE logical_boxes SET default_agent`).WithArgs("a", "box-1", []byte(`[]`), "codex").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	transport := &profileTestTransport{volume: "volume"}
	assignment := fleetAssignment{Box: v1.LogicalBox{ID: "box-1", AccountID: "a", DefaultAgent: "codex", VolumeID: "volume", AssignmentGeneration: 1}, Slot: v1.ComputeSlot{ID: "slot", ServiceID: "service"}, FencingToken: "fence"}
	if err := (&Server{Store: store}).provisionPendingBoxProfiles(context.Background(), transport, "a", assignment); err != nil {
		t.Fatalf("queued empty selection was skipped: %v", err)
	}
	if transport.writes != 1 || len(transport.files) != 0 || !slices.Contains(transport.removes, "/data/home/.config/gh/hosts.yml") {
		t.Fatalf("queued GitHub removal was not transferred: writes=%d files=%v remove=%v", transport.writes, transport.files, transport.removes)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
