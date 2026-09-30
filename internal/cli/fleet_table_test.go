package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
)

func TestFleetTableKeepsIdentifiersCompact(t *testing.T) {
	const service = "01287a75-a23d-4677-be20-ba24e97c4cb0"
	const deployment = "f58c993d-b7a4-4eb8-bc77-123456789012"
	status := v1.FleetStatus{Provider: "railway", Slots: []v1.ComputeSlot{
		{Ordinal: 1, State: "occupied", LogicalBoxName: "web-proof-0907", ServiceID: service, DeploymentInstanceID: deployment, Region: "europe-west4", Health: "healthy", LeaseOwner: "web"},
		{Ordinal: 2, State: "starting", Region: "europe-west4", Health: "starting"},
	}}
	var output bytes.Buffer
	writeFleetStatus(&output, status)
	text := output.String()
	for _, want := range []string{"01287a75…", "f58c993d…", "europe-west4", "web-proof-0907", "—", "vbox fleet status --json"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in %s", want, text)
		}
	}
	if strings.Contains(text, service) || strings.Contains(text, deployment) || strings.Contains(text, "lease=") {
		t.Fatal("table still contains verbose infrastructure details")
	}
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "1 ") || strings.HasPrefix(line, "2 ") {
			if len([]rune(line)) > 110 {
				t.Fatalf("table row too wide: %s", line)
			}
		}
	}
	raw, err := json.Marshal(status)
	if err != nil || !strings.Contains(string(raw), service) || !strings.Contains(string(raw), deployment) {
		t.Fatal("display truncation modified full JSON identifiers")
	}
	if strings.ContainsAny(fleetCell("name\n\x1b[31m", 24), "\n\x1b") {
		t.Fatal("untrusted field injected terminal controls")
	}
}
