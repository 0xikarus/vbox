package controller

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestProfileUsageGroupsRunningBoxesByImportedProfile(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	rows := sqlmock.NewRows([]string{"account_id", "id", "name", "provider", "provider_credential", "default_agent", "profiles", "service_id", "assignment_generation"}).
		AddRow("account-a", "box-1", "one", "railway", "primary", "claude", []byte(`[{"application":"claude","name":"personal"}]`), "service-1", int64(3)).
		AddRow("account-a", "box-2", "two", "railway", "primary", "claude", []byte(`[{"application":"claude","name":"personal"}]`), "service-2", int64(4)).
		AddRow("account-a", "box-3", "three", "railway", "primary", "codex", []byte(`[{"application":"codex","name":"work"}]`), "service-3", int64(5)).
		AddRow("account-a", "box-4", "four", "railway", "primary", "codex", []byte(`[{"application":"claude","name":"wrong-agent"}]`), "service-4", int64(6))
	mock.ExpectQuery("SELECT b.account_id::text").WithArgs("account-a").WillReturnRows(rows)

	server := &Server{Store: &Store{DB: db}}
	candidates, err := server.liveProfileUsageCandidates(context.Background(), "account-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 2 {
		t.Fatalf("got %d candidates, want 2: %+v", len(candidates), candidates)
	}
	if candidates[0].Application != "claude" || candidates[0].Name != "personal" || strings.Join(candidates[0].Boxes, ",") != "one,two" || candidates[0].ServiceID != "service-1" {
		t.Fatalf("shared profile should have one probe and both box names: %+v", candidates[0])
	}
	if candidates[1].Application != "codex" || candidates[1].Name != "work" || strings.Join(candidates[1].Boxes, ",") != "three" {
		t.Fatalf("independent profile missing: %+v", candidates[1])
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestProfileUsageOverviewReadsOnlyCachedOwnerData(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	profileRows := sqlmock.NewRows([]string{"account_id", "id", "name", "provider", "provider_credential", "default_agent", "profiles", "service_id", "assignment_generation"}).
		AddRow("account-a", "box-1", "one", "railway", "primary", "claude", []byte(`[{"application":"claude","name":"personal"}]`), "service-1", int64(3))
	mock.ExpectQuery("SELECT b.account_id::text").WithArgs("account-a").WillReturnRows(profileRows)
	mock.ExpectQuery("SELECT account_id::text,application,name FROM login_profiles").WithArgs("account-a").
		WillReturnRows(sqlmock.NewRows([]string{"account_id", "application", "name"}).AddRow("account-a", "claude", "personal"))
	checked := time.Date(2026, 9, 24, 1, 0, 0, 0, time.UTC)
	snapshot := []byte(`{"windows":[{"name":"session","usedPercent":4}],"balances":[],"rateCaps":[]}`)
	mock.ExpectQuery("SELECT checked_at,observed_at").WithArgs("account-a", "claude", "personal").
		WillReturnRows(sqlmock.NewRows([]string{"checked_at", "observed_at", "snapshot", "last_error"}).AddRow(checked, checked, snapshot, ""))

	server := &Server{Store: &Store{DB: db}}
	response := httptest.NewRecorder()
	server.profileUsageOverview(response, httptest.NewRequest("GET", "/v1/profile-usage", nil), Principal{AccountID: "account-a", Role: "owner"})
	if response.Code != 200 || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("unexpected response %d, cache %q: %s", response.Code, response.Header().Get("Cache-Control"), response.Body)
	}
	var body struct {
		Profiles []profileUsageOverviewRow `json:"profiles"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Profiles) != 1 || body.Profiles[0].Snapshot == nil || body.Profiles[0].Snapshot.Windows[0].UsedPercent == nil || *body.Profiles[0].Snapshot.Windows[0].UsedPercent != 4 {
		t.Fatalf("cached limit missing: %+v", body.Profiles)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestProfileUsageIncludesSavedProfilesWithoutRunningBoxes(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	live := sqlmock.NewRows([]string{"account_id", "id", "name", "provider", "provider_credential", "default_agent", "profiles", "service_id", "assignment_generation"}).
		AddRow("account-a", "box-1", "one", "railway", "primary", "claude", []byte(`[{"application":"claude","name":"personal"}]`), "service-1", int64(3))
	mock.ExpectQuery("SELECT b.account_id::text").WithArgs("account-a").WillReturnRows(live)
	mock.ExpectQuery("SELECT account_id::text,application,name FROM login_profiles").WithArgs("account-a").
		WillReturnRows(sqlmock.NewRows([]string{"account_id", "application", "name"}).
			AddRow("account-a", "claude", "personal").
			AddRow("account-a", "codex", "sleeping").
			AddRow("account-a", "opencode", "unused"))
	server := &Server{Store: &Store{DB: db}}
	candidates, err := server.savedProfileUsageCandidates(context.Background(), "account-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 3 || candidates[0].ServiceID != "service-1" || candidates[1].ServiceID != "" || candidates[2].ServiceID != "" {
		t.Fatalf("saved profiles or live preference missing: %+v", candidates)
	}
	if len(candidates[1].Boxes) != 0 || len(candidates[2].Boxes) != 0 {
		t.Fatalf("profiles without running boxes have live boxes: %+v", candidates)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestSavedProfileUsageUsesOnlyIsolatedClaudeCredential(t *testing.T) {
	bin := t.TempDir()
	command := `#!/bin/sh
test -f "$CLAUDE_CONFIG_DIR/.credentials.json" || exit 2
test ! -e "$CLAUDE_CONFIG_DIR/settings.json" || exit 3
test "$HOME" != /data/home || exit 4
printf '%s\n' '{"type":"assistant","local_command_run":{"command":"usage"},"usage_report":{"rate_limits":{"limits":[{"kind":"session","group":"session","percent":4}]}}}'
`
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(command), 0o700); err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{
		".credentials.json": []byte(`{"claudeAiOauth":{"accessToken":"synthetic-secret"}}`),
		"settings.json":     []byte(`{"hooks":{"danger":"must-not-run"}}`),
	}
	snapshot, err := runSavedProfileUsage(context.Background(), "claude", "", "", files, bin+":/usr/bin:/bin")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Source != "saved profile" || len(snapshot.Windows) != 1 || snapshot.Windows[0].UsedPercent == nil || *snapshot.Windows[0].UsedPercent != 4 {
		t.Fatalf("saved Claude usage missing: %+v", snapshot)
	}
	encoded, _ := json.Marshal(snapshot)
	if strings.Contains(string(encoded), "synthetic-secret") {
		t.Fatal("credential appeared in usage result")
	}
}

func TestProfileUsageProbeJSONDecodesNormalizedFields(t *testing.T) {
	response, err := decodeProfileUsage([]byte(`{"windows":[{"name":"session","usedPercent":4}],"balances":[],"rateCaps":[],"error":""}`), "live box")
	if err != nil {
		t.Fatal(err)
	}
	if response.Source != "live box" || len(response.Windows) != 1 || response.Windows[0].Name != "session" || response.Windows[0].UsedPercent == nil || *response.Windows[0].UsedPercent != 4 {
		t.Fatalf("normalized response not decoded: %+v", response)
	}
}
