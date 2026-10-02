package controller

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestObservedActivityQuietWindowResetsOnNewBusyTurn(t *testing.T) {
	now := time.Now().UTC()
	busy := sql.NullBool{Bool: true, Valid: true}
	observed := sql.NullTime{Time: now.Add(-5 * time.Second), Valid: true}
	oldEvidence := sql.NullTime{Time: now.Add(-3 * time.Minute), Valid: true}
	oldBusy := sql.NullTime{Time: now.Add(-4 * time.Minute), Valid: true}
	if status, source, _ := observedActivityStatus(now, busy, oldBusy, observed, oldEvidence, sql.NullTime{}, "", "working"); status != "Idle" || source != "quiet" {
		t.Fatalf("unchanged transcript should be quiet: %s/%s", status, source)
	}
	newBusy := sql.NullTime{Time: now.Add(-10 * time.Second), Valid: true}
	if status, source, _ := observedActivityStatus(now, busy, newBusy, observed, oldEvidence, sql.NullTime{}, "", "working"); status != "Working" || source != "fallback" {
		t.Fatalf("new busy turn should reset quiet window: %s/%s", status, source)
	}
}

func TestBoxActivityReturnsOneScopedBatchAndExpiresState(t *testing.T) {
	store, mock := testStore(t)
	now := time.Now().UTC()
	columns := []string{"box_id", "agent_busy", "agent_busy_updated_at", "mascot_mood", "mascot_activity", "mascot_phrase", "mascot_observed_at", "mascot_phrase_at", "mascot_evidence_changed_at"}
	mock.ExpectQuery(`(?s)FROM logical_boxes b LEFT JOIN LATERAL .*WHERE b.account_id=\$1 AND \(\$2='owner' OR b.owner_user_id=\$3\)`).
		WithArgs("account-a", "user", "user-a").
		WillReturnRows(sqlmock.NewRows(columns).
			AddRow("box-fresh", true, now.Add(-5*time.Second), "happy", "working", "Editing chat.js", now.Add(-5*time.Second), now.Add(-5*time.Second), now.Add(-5*time.Second)).
			AddRow("box-old-mood", true, now.Add(-2*time.Minute), "angry", "working", "Running tests", now.Add(-2*time.Minute), now.Add(-2*time.Minute), now.Add(-2*time.Minute)).
			AddRow("box-old-phrase", true, now.Add(-11*time.Minute), "happy", "working", "Old phrase", now.Add(-11*time.Minute), now.Add(-11*time.Minute), now.Add(-11*time.Minute)).
			AddRow("box-idle", false, now.Add(-5*time.Second), "idle", "idle", "Old idle phrase", now.Add(-5*time.Second), now.Add(-3*time.Minute), now.Add(-5*time.Second)).
			// A phrase was produced three minutes ago; the latest observation
			// was unsure five seconds ago. Details retains the phrase, but the
			// list must return to "working…".
			AddRow("box-empty-run", true, now.Add(-5*time.Second), "idle", "working", "Last good phrase", now.Add(-5*time.Second), now.Add(-3*time.Minute), now.Add(-5*time.Second)).
			AddRow("box-quiet", true, now.Add(-4*time.Minute), "idle", "idle", "Last good phrase", now.Add(-5*time.Second), now.Add(-4*time.Minute), now.Add(-3*time.Minute)).
			AddRow("box-unknown", nil, nil, nil, nil, nil, nil, nil, nil).
			AddRow("box-hibernated", nil, nil, nil, nil, nil, nil, nil, nil))
	response := httptest.NewRecorder()
	chatTestServer(store).boxActivityHandler(response, httptest.NewRequest(http.MethodGet, "/v1/box-activity", nil), Principal{AccountID: "account-a", UserID: "user-a", Role: "user"})
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("status=%d cache=%q body=%s", response.Code, response.Header().Get("Cache-Control"), response.Body.String())
	}
	var activities []boxActivity
	if err := json.Unmarshal(response.Body.Bytes(), &activities); err != nil || len(activities) != 8 {
		t.Fatalf("activities=%+v err=%v", activities, err)
	}
	if activities[0].BoxID != "box-fresh" || activities[0].Busy == nil || !*activities[0].Busy || activities[0].Phrase != "Editing chat.js" || activities[0].StatusSource != "specific" || activities[0].Mood != "happy" || activities[0].ObservedAt == nil || activities[0].BusySince == nil {
		t.Fatalf("fresh activity: %+v", activities[0])
	}
	if activities[1].Phrase != "" || activities[1].Status != "Unknown" || activities[1].StatusSource != "stale" || activities[1].Mood != "" || activities[1].ObservedAt != nil || activities[1].LastMood != "angry" || activities[1].LastActivity != "working" || activities[1].LastObservedAt == nil || activities[1].LastPhraseAt == nil {
		t.Fatalf("old mood should remain available for details: %+v", activities[1])
	}
	if activities[2].Phrase != "" || activities[2].Status != "Unknown" || activities[2].Mood != "" || activities[2].ObservedAt != nil || activities[2].LastPhrase != "Old phrase" || activities[2].LastPhraseAt == nil {
		t.Fatalf("expired phrase: %+v", activities[2])
	}
	if activities[3].Busy == nil || *activities[3].Busy || activities[3].Status != "Idle" || activities[3].LastPhrase != "Old idle phrase" || activities[4].Phrase != "Working" || activities[4].StatusSource != "fallback" || activities[4].LastPhrase != "Last good phrase" || activities[4].LastPhraseAt == nil || activities[4].LastObservedAt == nil || !activities[4].LastPhraseAt.Before(*activities[4].LastObservedAt) || activities[5].Phrase != "Idle" || activities[5].StatusSource != "quiet" || activities[6].Busy != nil || activities[7].Busy != nil || activities[7].Mood != "" {
		t.Fatalf("idle, empty-run, quiet, unknown, or hibernated activity leaked: %+v %+v %+v %+v %+v", activities[3], activities[4], activities[5], activities[6], activities[7])
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
