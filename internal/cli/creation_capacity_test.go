package cli

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/config"
)

func TestCreationCapacityGuidance(t *testing.T) {
	for _, tc := range []struct {
		name         string
		fleet        v1.FleetStatus
		region, want string
	}{
		{"zero", v1.FleetStatus{}, "", "vbox fleet slots set 1"},
		{"starting", v1.FleetStatus{DesiredSlots: 2, ActualSlots: 1}, "", "still starting"},
		{"unhealthy", v1.FleetStatus{DesiredSlots: 2, ActualSlots: 2, UnhealthySlots: 2}, "", "unhealthy or stopped"},
		{"busy", v1.FleetStatus{DesiredSlots: 2, ActualSlots: 2, OccupiedSlots: 2}, "", "vbox hibernate BOX"},
		{"region", v1.FleetStatus{DesiredSlots: 2, ActualSlots: 2, FreeSlots: 2}, "eu", "selected location eu"},
		{"race", v1.FleetStatus{DesiredSlots: 2, ActualSlots: 2, FreeSlots: 2}, "", "capacity may have changed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := creationCapacityMessage(tc.fleet, tc.region)
			if !strings.Contains(got, tc.want) || !strings.Contains(got, "retry Create") {
				t.Fatal(got)
			}
		})
	}
}

func TestCreationCapacityChecksSelectedProviderAndPreservesFailure(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/v1/fleet/status" || r.URL.Query().Get("providerCredential") != "selected" {
			t.Error("wrong diagnostic target")
		}
		http.Error(w, "unavailable", 503)
	}))
	defer s.Close()
	a := &App{HTTP: s.Client()}
	cause := errors.New("no healthy free compute slot is available")
	err := a.creationCapacityError(context.Background(), config.Context{Controller: s.URL, ProviderCredential: "default"}, "test", v1.CreateLogicalBoxRequest{Provider: "railway", ProviderCredential: "selected"}, cause)
	if !errors.Is(err, cause) || !strings.Contains(err.Error(), "Could not check fleet status") {
		t.Fatal(err)
	}
}

func TestFormStatusWrapsActionableErrors(t *testing.T) {
	message := "Creation paused\nRun: vbox fleet slots set 1\nThen retry Create."
	lines := formStatusLines(message, 20)
	for _, line := range lines {
		if len([]rune(line)) > 20 {
			t.Fatal(line)
		}
	}
	if !strings.Contains(strings.Join(lines, " "), "vbox fleet slots set 1") {
		t.Fatal(lines)
	}
}
