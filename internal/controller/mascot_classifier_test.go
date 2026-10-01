package controller

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestClassifyMascotText(t *testing.T) {
	cases := []struct {
		name, text, mood, activity string
	}{
		{"neutral", "Inspecting the repository layout.", "idle", "idle"},
		{"work", "Running the build now", "idle", "working"},
		{"failure", "error: compilation failed", "angry", "idle"},
		{"recovery", "error: compilation failed\nFixed the issue. All tests passed.", "happy", "idle"},
		{"approval", "Please confirm which option to use", "waiting", "waiting"},
		{"humor", "Haha, that was funny 😂", "laughing", "idle"},
		{"quoted command", "$ echo 'error: fake'", "idle", "idle"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyMascotText(tc.text)
			if got.Mood != tc.mood || got.Activity != tc.activity {
				t.Fatalf("got %+v, want %s/%s", got, tc.mood, tc.activity)
			}
		})
	}
}

func TestMascotObservationScopesSessionAndStoresOnlyState(t *testing.T) {
	store, mock := testStore(t)
	mock.ExpectExec(`UPDATE box_tasks SET mascot_mood`).
		WithArgs("account-a", "box-a", "codex-chat", "angry", "idle").
		WillReturnResult(sqlmock.NewResult(0, 1))
	server := chatTestServer(store)
	request := httptest.NewRequest(http.MethodPost, "/v1/agent-desktop/mascot-observation", bytes.NewBufferString(`{"session":"codex-chat","text":"error: build failed"}`))
	request.SetPathValue("id", "box-a")
	response := httptest.NewRecorder()
	server.mascotObservationHandler(response, request, Principal{AccountID: "account-a", Role: "desktop-agent", Subject: "desktop-box:box-a"})
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"mood":"angry"`) {
		t.Fatalf("response %d: %s", response.Code, response.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestMascotStateExpiresAndFollowsActiveTask(t *testing.T) {
	store, mock := testStore(t)
	mock.ExpectQuery(`SELECT mascot_mood,mascot_activity,mascot_observed_at FROM box_tasks`).WithArgs("account-a", "box-a").
		WillReturnRows(sqlmock.NewRows([]string{"mascot_mood", "mascot_activity", "mascot_observed_at"}).AddRow("happy", "idle", time.Now().Add(-time.Minute)))
	if _, fresh, err := store.boxMascotState(context.Background(), "account-a", "box-a"); err != nil || fresh {
		t.Fatalf("expired state: fresh=%t err=%v", fresh, err)
	}
	mock.ExpectQuery(`SELECT mascot_mood,mascot_activity,mascot_observed_at FROM box_tasks`).WithArgs("account-a", "box-a").
		WillReturnRows(sqlmock.NewRows([]string{"mascot_mood", "mascot_activity", "mascot_observed_at"}).AddRow("happy", "idle", time.Now()))
	state, fresh, err := store.boxMascotState(context.Background(), "account-a", "box-a")
	if err != nil || !fresh || state.Mood != "happy" {
		t.Fatalf("fresh state: %+v fresh=%t err=%v", state, fresh, err)
	}
}
