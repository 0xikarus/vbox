package boxruntime

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestDesktopMulticallRunsInParallelAndKeepsInputOrder(t *testing.T) {
	calls, err := parseDesktopMultiCalls(json.RawMessage(`{"calls":[{"name":"get_contacts","arguments":{}},{"name":"get_run_budget","arguments":{}},{"name":"get_thread_history","arguments":{"threadId":"synthetic"}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan string, 2)
	release := make(chan struct{})
	done := make(chan []map[string]any, 1)
	go func() {
		done <- runDesktopMultiCalls(context.Background(), "synthetic", calls, map[string]bool{"get_contacts": true, "get_run_budget": true}, func(_ context.Context, _, name string, _ json.RawMessage) (map[string]any, error) {
			started <- name
			<-release
			return map[string]any{"name": name}, nil
		})
	}()
	for range 2 {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("independent calls did not start concurrently")
		}
	}
	close(release)
	select {
	case results := <-done:
		for index, name := range []string{"get_contacts", "get_run_budget", "get_thread_history"} {
			if results[index]["name"] != name {
				t.Fatalf("result order: %v", results)
			}
		}
		if results[0]["isError"] != false || results[1]["isError"] != false || results[2]["isError"] != true {
			t.Fatalf("result statuses: %v", results)
		}
		if !strings.Contains(results[2]["content"].([]map[string]any)[0]["text"].(string), "not allowed") {
			t.Fatalf("permission error missing: %v", results[2])
		}
	case <-time.After(time.Second):
		t.Fatal("multicall did not finish")
	}
}

func TestDesktopMulticallRejectsNestedAndMalformedCalls(t *testing.T) {
	for _, raw := range []string{
		`{"calls":[{"name":"multicall","arguments":{}},{"name":"get_contacts","arguments":{}}]}`,
		`{"calls":[{"name":"get_contacts","arguments":null},{"name":"get_run_budget","arguments":{}}]}`,
		`{"calls":[{"name":"get_contacts","arguments":{}}]}`,
	} {
		if _, err := parseDesktopMultiCalls(json.RawMessage(raw)); err == nil {
			t.Fatalf("accepted invalid multicall: %s", raw)
		}
	}
}
