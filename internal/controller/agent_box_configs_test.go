package controller

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/DATA-DOG/go-sqlmock"
)

func TestAgentBoxProfileOptionsOnlyListsCreatableHarnesses(t *testing.T) {
	profiles := []v1.LoginProfile{
		{Application: "codex", Name: "work"},
		{Application: "opencode", Name: "venice", Model: "venice/deepseek-v4.1-flash"},
		{Application: "github", Name: "account"},
	}
	got := agentBoxProfileOptions([]string{"opencode"}, profiles)
	if len(got) != 2 || got[0].Name != "venice" || got[1].Name != "account" {
		t.Fatalf("filtered profiles=%+v", got)
	}
}

func TestAgentBoxConfigsRequireCreateGrant(t *testing.T) {
	store, mock := testStore(t)
	mock.ExpectQuery("FROM box_role_assignments").WithArgs("account-a", "box-a").
		WillReturnRows(sqlmock.NewRows([]string{"permission", "config"}))
	r := httptest.NewRequest(http.MethodGet, "/v1/agent-desktop/box-configs", nil)
	w := httptest.NewRecorder()
	(&Server{Store: store}).agentBoxConfigsHandler(w, r, Principal{AccountID: "account-a", Subject: "desktop-box:box-a"})
	if w.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestAgentBoxProfileSelectionMatchesHarness(t *testing.T) {
	valid := []v1.LoginProfileRef{{Application: "opencode", Name: "venice", Model: "venice/deepseek-v4.1-flash"}, {Application: "github", Name: "account"}}
	if err := validateAgentBoxProfiles("opencode", valid); err != nil {
		t.Fatal(err)
	}
	if err := validateAgentBoxProfiles("codex", valid); err == nil {
		t.Fatal("cross-harness credential import accepted")
	}
	if err := validateAgentBoxProfiles("opencode", append(slices.Clone(valid), valid[0])); err == nil {
		t.Fatal("duplicate agent profile accepted")
	}
}
