package cli

import (
	"strings"
	"testing"
)

func TestCreationMeterTracksStagesNotTime(t *testing.T) {
	m := &creationMeter{}
	for _, tc := range []struct {
		phase   string
		percent int
	}{
		{"Uploading selected claude profile…", 5},
		{"attaching · creation-volume-requested", 20},
		{"attaching · creation-initializing", 40},
		{"attaching · creation-initializing", 40},
		{"unknown phase", 40},
		{"attaching · creation-volume-requested", 40},
		{"hibernated · saved", 75},
		{"requesting-allocation", 80},
		{"waiting-for-runtime", 90},
		{"restoring-workspace-metadata", 95},
	} {
		got := m.render(tc.phase)
		if m.percent != tc.percent || !strings.Contains(got, tc.phase) || strings.Contains(got, "100%") {
			t.Fatalf("%q: %s", tc.phase, got)
		}
	}
	if got := m.complete(); !strings.Contains(got, "[====================] 100%") {
		t.Fatal(got)
	}
}
