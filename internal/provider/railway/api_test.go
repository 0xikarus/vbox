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
	runner := &procexec.FakeRunner{Results: []procexec.Result{{}, {Stdout: []byte(`{"data":{"serviceInstanceLimits":{"containers":{"cpu":2,"memoryBytes":4000000000,"pidLimit":1000}}}}`)}}}
	p := newTestProvider(Config{EnvironmentID: "environment"}, runner)
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

func TestServiceInstanceIDSelectsRunningDeploymentReplica(t *testing.T) {
	runner := &procexec.FakeRunner{Results: []procexec.Result{{Stdout: []byte(`{"data":{"serviceInstance":{"latestDeployment":{"deploymentStopped":false,"instances":[{"id":"initializing","status":"INITIALIZING"},{"id":"running-replica","status":"RUNNING"}]}}}}`)}}}
	p := newTestProvider(Config{EnvironmentID: "environment"}, runner)
	instance, err := p.serviceInstanceID(context.Background(), "service")
	if err != nil || instance != "running-replica" {
		t.Fatalf("instance=%q err=%v", instance, err)
	}
	if call := strings.Join(runner.Calls[0].Argv, " "); !strings.Contains(call, "latestDeployment") || !strings.Contains(call, "instances") {
		t.Fatalf("deployment lookup did not request running replicas: %s", call)
	}
}

func TestServiceInstanceIDRejectsStoppedOrNonRunningDeployment(t *testing.T) {
	for _, response := range []string{
		`{"data":{"serviceInstance":{"latestDeployment":{"deploymentStopped":true,"instances":[{"id":"old","status":"RUNNING"}]}}}}`,
		`{"data":{"serviceInstance":{"latestDeployment":{"deploymentStopped":false,"instances":[{"id":"pending","status":"INITIALIZING"}]}}}}`,
	} {
		runner := &procexec.FakeRunner{Results: []procexec.Result{{Stdout: []byte(response)}}}
		p := newTestProvider(Config{EnvironmentID: "environment"}, runner)
		if instance, err := p.serviceInstanceID(context.Background(), "service"); err == nil || instance != "" {
			t.Fatalf("accepted inactive deployment instance=%q err=%v", instance, err)
		}
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
	p := newTestProvider(Config{ProjectID: "project", EnvironmentID: "environment"}, runner)
	usage, err := p.Usage(context.Background(), "worker")
	if err != nil {
		t.Fatal(err)
	}
	if usage.Cost.Available || !strings.Contains(usage.Cost.Detail, "project tokens") {
		t.Fatalf("usage = %+v", usage)
	}
}

func TestUsageBatchReadsBillingOnceForSeveralServices(t *testing.T) {
	runner := &procexec.FakeRunner{Results: []procexec.Result{
		{Stdout: []byte(`[{"id":"service-a","name":"vmbox-slot-a","status":"SUCCESS"},{"id":"service-b","name":"vmbox-slot-b","status":"SUCCESS"}]`)},
		{Stdout: []byte(`{"services":[{"name":"vmbox-slot-a","totalDollars":1.25},{"name":"vmbox-slot-b","totalDollars":2.5}]}`)},
	}}
	p := newTestProvider(Config{ProjectID: "project", EnvironmentID: "environment"}, runner)
	usage, err := p.UsageBatch(context.Background(), []string{"service-a", "service-b"})
	if err != nil {
		t.Fatal(err)
	}
	if len(runner.Calls) != 2 || runner.Calls[1].Argv[1] != "usage" {
		t.Fatalf("provider calls = %#v", runner.Calls)
	}
	if usage["service-a"].Cost.Accrued != 1.25 || usage["service-b"].Cost.Accrued != 2.5 {
		t.Fatalf("usage = %+v", usage)
	}
}

func TestConfigureServiceBatchesSettings(t *testing.T) {
	for _, region := range []string{"", "ams"} {
		t.Run("region="+region, func(t *testing.T) {
			runner := &procexec.FakeRunner{}
			p := newTestProvider(Config{EnvironmentID: "environment"}, runner)
			if err := p.configureService(context.Background(), "service", "example/image:tag", region, "sleep infinity"); err != nil {
				t.Fatal(err)
			}
			if len(runner.Calls) != 1 {
				t.Fatalf("got %d calls, want one", len(runner.Calls))
			}
			var variables struct {
				ServiceID     string `json:"serviceId"`
				EnvironmentID string `json:"environmentId"`
				Input         struct {
					Source struct {
						Image string `json:"image"`
					} `json:"source"`
					StartCommand string                     `json:"startCommand"`
					Regions      map[string]json.RawMessage `json:"multiRegionConfig"`
				} `json:"input"`
			}
			if err := json.Unmarshal([]byte(runner.Calls[0].Argv[4]), &variables); err != nil {
				t.Fatal(err)
			}
			if variables.ServiceID != "service" || variables.EnvironmentID != "environment" || variables.Input.Source.Image != "example/image:tag" || variables.Input.StartCommand != "sleep infinity" {
				t.Fatalf("unexpected settings: %+v", variables)
			}
			if region == "" && variables.Input.Regions != nil {
				t.Fatal("empty region must preserve provider placement")
			}
			if region != "" && (string(variables.Input.Regions[region]) != `{"numReplicas":1}` || string(variables.Input.Regions["pdx"]) != "null") {
				t.Fatalf("unexpected placement: %v", variables.Input.Regions)
			}
		})
	}
}

func TestConfigureServiceDoesNotRetryFailedMutation(t *testing.T) {
	runner := &procexec.FakeRunner{Results: []procexec.Result{{ExitCode: 1}}}
	p := newTestProvider(Config{EnvironmentID: "environment"}, runner)
	if err := p.configureService(context.Background(), "service", "image", "ams", "sleep infinity"); err == nil {
		t.Fatal("expected failure")
	}
	if len(runner.Calls) != 1 {
		t.Fatal("unexpected mutation retry")
	}
}

func TestSetResourceLimitsDoesNotDeployOrRestart(t *testing.T) {
	runner := &procexec.FakeRunner{Results: []procexec.Result{{}}}
	p := newTestProvider(Config{EnvironmentID: "environment"}, runner)
	if err := p.SetResourceLimits(context.Background(), "service", provider.Resources{CPU: 4, MemoryMiB: 12288}); err != nil {
		t.Fatal(err)
	}
	if len(runner.Calls) != 1 || !strings.Contains(runner.Calls[0].Argv[2], "serviceInstanceLimitsUpdate") {
		t.Fatalf("unexpected commands: %+v", runner.Calls)
	}
}
