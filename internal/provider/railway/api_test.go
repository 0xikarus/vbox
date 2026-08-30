package railway

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/0xikarus/vmbox-service/internal/procexec"
	"github.com/0xikarus/vmbox-service/internal/provider"
)

func TestResourceLimitsUseCurrentRailwayAPI(t *testing.T) {
	runner := &procexec.FakeRunner{Results: []procexec.Result{{}, {Stdout: []byte(`{"data":{"serviceInstanceLimits":{"vCPUs":2,"memoryGB":4}}}`)}}}
	p := New(Config{EnvironmentID: "environment"}, runner)
	if err := p.setResources(context.Background(), "service", provider.Resources{CPU: 2, MemoryMiB: 4096}); err != nil {
		t.Fatal(err)
	}
	call := runner.Calls[0]
	if len(call.Argv) < 5 || call.Argv[0] != "railway" || call.Argv[1] != "api" || !strings.Contains(call.Argv[2], "serviceInstanceLimitsUpdate") {
		t.Fatalf("resource mutation argv = %#v", call.Argv)
	}
	var variables struct {
		Input map[string]any `json:"input"`
	}
	if err := json.Unmarshal([]byte(call.Argv[4]), &variables); err != nil {
		t.Fatal(err)
	}
	if variables.Input["serviceId"] != "service" || variables.Input["environmentId"] != "environment" || variables.Input["vCPUs"] != float64(2) || variables.Input["memoryGB"] != float64(4) {
		t.Fatalf("resource variables = %#v", variables.Input)
	}
	resources, err := p.resources(context.Background(), "service")
	if err != nil || resources.CPU != 2 || resources.MemoryMiB != 4096 {
		t.Fatalf("resources=%+v err=%v", resources, err)
	}
}

func TestDecodeRailwayCostAggregatesMatchingEntries(t *testing.T) {
	data := []byte(`{"services":[{"name":"vmbox-worker","totalDollars":0.25},{"name":"vmbox-worker","totalDollars":0.5},{"name":"other","totalDollars":10}]}`)
	cost := decodeRailwayCost(data, "vmbox-worker")
	if !cost.Available || math.Abs(cost.Accrued-0.75) > 0.000001 || !strings.Contains(cost.Detail, "2") {
		t.Fatalf("cost = %+v", cost)
	}
}

func TestUsageReportsProjectTokenBillingScopeWithoutFailing(t *testing.T) {
	runner := &procexec.FakeRunner{Results: []procexec.Result{
		{Stdout: []byte(`[{"id":"service","name":"vmbox-worker","status":"SUCCESS"}]`)},
		{ExitCode: 1, Stderr: []byte("Unauthorized")},
	}}
	p := New(Config{ProjectID: "project", EnvironmentID: "environment"}, runner)
	usage, err := p.Usage(context.Background(), "worker")
	if err != nil {
		t.Fatal(err)
	}
	if usage.Cost.Available || !strings.Contains(usage.Cost.Detail, "project tokens") {
		t.Fatalf("usage = %+v", usage)
	}
}
