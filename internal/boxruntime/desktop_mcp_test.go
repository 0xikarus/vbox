package boxruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestDesktopMCPNegotiationAndInvalidCalls(t *testing.T) {
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25"}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"desktop_click","arguments":{"x":5}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"desktop_screenshot","arguments":{"action":"shell"}}}`,
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"shell","arguments":{}}}`,
	}, "\n")
	var out bytes.Buffer
	if err := ServeDesktopMCP(context.Background(), "invalid", strings.NewReader(input), &out); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 5 {
		t.Fatalf("unexpected responses: %s", out.String())
	}
	for i, line := range lines {
		var response struct {
			ID     int            `json:"id"`
			Result map[string]any `json:"result"`
		}
		if err := json.Unmarshal([]byte(line), &response); err != nil {
			t.Fatal(err)
		}
		if response.ID != i+1 {
			t.Fatal("response identity lost")
		}
		if i == 0 && response.Result["protocolVersion"] != "2025-11-25" {
			t.Fatal("version negotiation failed")
		}
		if i == 1 && len(response.Result["tools"].([]any)) != 10 {
			t.Fatal("tool inventory incomplete")
		}
		if i >= 2 && response.Result["isError"] != true {
			t.Fatal("invalid call accepted")
		}
	}
}

func TestDesktopInputValidation(t *testing.T) {
	for _, data := range []string{
		`{"action":"key","keys":["exec"]}`,
		`{"action":"move","x":1280,"y":0}`,
		`{"action":"drag","x":0,"y":0,"toX":-1}`,
		`{"action":"type","text":""}`,
		`{"action":"scroll","text":"sideways"}`,
		`{"action":"pause","command":"rm"}`,
	} {
		if _, err := DecodeDesktopAction([]byte(data)); err == nil {
			t.Fatalf("accepted %s", data)
		}
	}
	if _, err := DecodeDesktopAction([]byte(`{"action":"type","text":"$(not a shell)"}`)); err != nil {
		t.Fatal(err)
	}
}
