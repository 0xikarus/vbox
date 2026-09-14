package workeragent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
)

func TestRuntimeObservationValidatesBindingAndBounds(t *testing.T) {
	a, binding := assignmentFixture(t)
	dir := t.TempDir()
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	inventory := v1.SessionInventory{Assignment: binding.Assignment, State: "live", Sessions: []v1.Session{{ID: "one", Name: "surviving", Incarnation: "runtime-one"}}}
	data, _ := json.Marshal(inventory)
	fixture := filepath.Join(dir, "sudo")
	write := func(output []byte) {
		// Fixture data contains no shell quoting or credentials.
		if err := os.WriteFile(fixture, append(append([]byte("#!/bin/sh\ncat <<'SNAPSHOT'\n"), output...), []byte("\nSNAPSHOT\n")...), 0700); err != nil {
			t.Fatal(err)
		}
	}
	write(data)
	got := a.observeRuntime(context.Background(), binding)
	if len(got) == 0 {
		t.Fatal("valid session observation unavailable")
	}
	inventory.Assignment = "stale"
	data, _ = json.Marshal(inventory)
	write(data)
	if got := a.observeRuntime(context.Background(), binding); got != nil {
		t.Fatal("stale runtime assignment accepted")
	}
	write([]byte("invalid"))
	if got := a.observeRuntime(context.Background(), binding); got != nil {
		t.Fatal("malformed runtime output accepted")
	}
	binding.BoxID = "compute-slot:" + binding.SlotID
	if got := a.observeRuntime(context.Background(), binding); got != nil {
		t.Fatal("free slot reported box sessions")
	}
}
