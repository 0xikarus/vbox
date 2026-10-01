package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestBoxActivityReturnsOneScopedBatchAndExpiresState(t *testing.T) {
	store, mock := testStore(t)
	now := time.Now().UTC()
	columns := []string{"box_id", "agent_busy", "agent_busy_updated_at", "mascot_mood", "mascot_activity", "mascot_phrase", "mascot_observed_at"}
	mock.ExpectQuery(`(?s)FROM logical_boxes b LEFT JOIN LATERAL .*WHERE b.account_id=\$1 AND \(\$2='owner' OR b.owner_user_id=\$3\)`).
		WithArgs("account-a", "user", "user-a").
		WillReturnRows(sqlmock.NewRows(columns).
			AddRow("box-fresh", true, now.Add(-5*time.Second), "happy", "working", "Editing chat.js", now.Add(-5*time.Second)).
			AddRow("box-old-mood", true, now.Add(-2*time.Minute), "angry", "working", "Running tests", now.Add(-2*time.Minute)).
			AddRow("box-old-phrase", true, now.Add(-11*time.Minute), "happy", "working", "Old phrase", now.Add(-11*time.Minute)).
			AddRow("box-idle", false, now.Add(-5*time.Second), "idle", "idle", "Old idle phrase", now.Add(-5*time.Second)).
			AddRow("box-unknown", nil, nil, nil, nil, nil, nil).
			AddRow("box-hibernated", nil, nil, nil, nil, nil, nil))
	response := httptest.NewRecorder()
	chatTestServer(store).boxActivityHandler(response, httptest.NewRequest(http.MethodGet, "/v1/box-activity", nil), Principal{AccountID: "account-a", UserID: "user-a", Role: "user"})
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("status=%d cache=%q body=%s", response.Code, response.Header().Get("Cache-Control"), response.Body.String())
	}
	var activities []boxActivity
	if err := json.Unmarshal(response.Body.Bytes(), &activities); err != nil || len(activities) != 6 {
		t.Fatalf("activities=%+v err=%v", activities, err)
	}
	if activities[0].BoxID != "box-fresh" || activities[0].Busy == nil || !*activities[0].Busy || activities[0].Phrase != "Editing chat.js" || activities[0].Mood != "happy" || activities[0].ObservedAt == nil || activities[0].BusySince == nil {
		t.Fatalf("fresh activity: %+v", activities[0])
	}
	if activities[1].Phrase != "Running tests" || activities[1].Mood != "" || activities[1].ObservedAt != nil {
		t.Fatalf("old mood should retain only recent busy phrase: %+v", activities[1])
	}
	if activities[2].Phrase != "" || activities[2].Mood != "" || activities[2].ObservedAt != nil {
		t.Fatalf("expired phrase: %+v", activities[2])
	}
	if activities[3].Busy == nil || *activities[3].Busy || activities[3].Phrase != "" || activities[4].Busy != nil || activities[5].Busy != nil || activities[5].Mood != "" {
		t.Fatalf("idle, unknown, or hibernated activity leaked: %+v %+v %+v", activities[3], activities[4], activities[5])
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
