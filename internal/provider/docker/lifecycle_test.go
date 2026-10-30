package docker

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/0xikarus/vmbox-service/internal/procexec"
	"github.com/0xikarus/vmbox-service/internal/provider"
)

func TestDeleteCleansOwnedOrphansWhenContainerIsMissing(t *testing.T) {
	owner := provider.Owner{AccountID: "standalone", BoxID: "testbox", Lease: "test-lease"}
	labels, _ := json.Marshal(map[string]string{labelAccount: owner.AccountID, labelBox: owner.BoxID, labelLease: owner.Lease})
	runner := &procexec.FakeRunner{Results: []procexec.Result{
		{ExitCode: 1, Stderr: []byte("No such container: vmbox-testbox")},
		{Stdout: labels}, {Stdout: labels}, {}, {},
	}}
	if err := New(Config{}, runner).Delete(context.Background(), "testbox", owner); err != nil {
		t.Fatal(err)
	}
	if len(runner.Calls) != 5 {
		t.Fatalf("got %d lifecycle calls", len(runner.Calls))
	}
	if strings.Join(runner.Calls[3].Argv, " ") != "docker network rm vmbox-testbox-net" || strings.Join(runner.Calls[4].Argv, " ") != "docker volume rm vmbox-testbox-data" {
		t.Fatal("owned orphans were not removed")
	}
}

func TestDeleteRejectsForeignResourceBeforeRemovingAnything(t *testing.T) {
	owner := provider.Owner{AccountID: "standalone", BoxID: "testbox", Lease: "test-lease"}
	runner := &procexec.FakeRunner{Results: []procexec.Result{
		{ExitCode: 1, Stderr: []byte("No such container: vmbox-testbox")},
		{Stdout: []byte(`{"dev.vmbox.account":"different","dev.vmbox.box":"testbox","dev.vmbox.lease":"test-lease"}`)},
	}}
	if err := New(Config{}, runner).Delete(context.Background(), "testbox", owner); err == nil {
		t.Fatal("foreign resource accepted")
	}
	for _, c := range runner.Calls {
		for _, a := range c.Argv {
			if a == "rm" {
				t.Fatal("resource removed before ownership validation")
			}
		}
	}
}

func TestDeployPreservesAgentConfigurationAndExplicitPort(t *testing.T) {
	owner := provider.Owner{AccountID: "standalone", BoxID: "testbox", Lease: "test-lease"}
	labels, _ := json.Marshal(map[string]string{labelAccount: owner.AccountID, labelBox: owner.BoxID, labelLease: owner.Lease})
	inspect := []byte(`[{"Id":"testbox","Config":{"Image":"standalone:test","Cmd":["vmbox-standalone"],"Env":["VMBOX_AGENT=claude","VMBOX_MODEL=test-model"],"Labels":{"dev.vmbox.account":"standalone","dev.vmbox.box":"testbox","dev.vmbox.lease":"test-lease"}},"State":{"Status":"exited"},"HostConfig":{"NanoCpus":2000000000,"Memory":6442450944}}]`)
	runner := &procexec.FakeRunner{Results: []procexec.Result{
		{Stdout: inspect}, {Stdout: inspect}, {}, {},
		{ExitCode: 1, Stderr: []byte("No such container")}, {}, {}, {}, {Stdout: labels}, {Stdout: labels}, {}, {Stdout: inspect},
	}}
	_, err := New(Config{CreateStopped: true, StopOnSuccess: true, PublishGateway: true, GatewayAddress: "0.0.0.0", GatewayPort: 32781}, runner).Deploy(context.Background(), "testbox", "standalone:updated")
	if err != nil {
		t.Fatal(err)
	}
	var create string
	for _, call := range runner.Calls {
		line := strings.Join(call.Argv, " ")
		if strings.Contains(line, "container create") {
			create = line
		}
	}
	for _, required := range []string{"--restart on-failure", "--publish 0.0.0.0:32781:8080", "VMBOX_AGENT=claude", "VMBOX_MODEL=test-model", "src=vmbox-testbox-data", "standalone:updated"} {
		if !strings.Contains(create, required) {
			t.Errorf("replacement lost %s", required)
		}
	}
}
