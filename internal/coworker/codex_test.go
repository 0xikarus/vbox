package coworker

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"
)

func TestCodexProtocolDeliveryState(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	fromServer, serverOut := io.Pipe()
	defer fromServer.Close()
	serverIn, toServer := io.Pipe()
	defer toServer.Close()
	events := make(chan Event, 1)
	events <- Event{Sequence: 7, Sender: "peer", Kind: "message", Data: json.RawMessage(`{"text":"hello"}`)}
	state := CodexState{}
	var snapshots []CodexState
	var output bytes.Buffer
	done := make(chan error, 1)
	go func() {
		done <- runCodexProtocol(ctx, fromServer, toServer, &output, events, &state, "", func(s CodexState) error { snapshots = append(snapshots, s); return nil })
	}()
	decoder := json.NewDecoder(serverIn)
	encoder := json.NewEncoder(serverOut)
	read := func(method string) {
		t.Helper()
		var m codexMessage
		if err := decoder.Decode(&m); err != nil {
			t.Fatal(err)
		}
		if m.Method != method {
			t.Fatalf("method=%s want=%s", m.Method, method)
		}
	}
	read("initialize")
	encoder.Encode(map[string]any{"id": 1, "result": map[string]any{}})
	read("initialized")
	read("thread/start")
	encoder.Encode(map[string]any{"id": 2, "result": map[string]any{"thread": map[string]string{"id": "thread-test"}}})
	read("turn/start")
	encoder.Encode(map[string]any{"id": 3, "result": map[string]any{"turn": map[string]string{"id": "turn-test"}}})
	encoder.Encode(map[string]any{"id": nil, "method": "item/completed", "params": map[string]any{"item": map[string]string{"id": "item-test", "type": "agentMessage", "text": "protocol fixture"}}})
	encoder.Encode(map[string]any{"method": "turn/completed", "params": map[string]any{"threadId": "thread-test", "turn": map[string]string{"id": "turn-test", "status": "completed"}}})
	serverOut.Close()
	<-done
	if output.String() != "protocol fixture\n" {
		t.Fatal("final-only item was not rendered")
	}
	if state.Pending || state.After != 7 || state.Thread != "thread-test" {
		t.Fatalf("delivery not recorded: %+v", state)
	}
	found := false
	for _, s := range snapshots {
		if s.Pending && s.PendingSequence == 7 && s.After == 0 {
			found = true
		}
	}
	if !found {
		t.Fatal("no pre-send pending checkpoint")
	}
}

func TestCodexRefusesUncertainReplay(t *testing.T) {
	err := runCodexProtocol(context.Background(), nil, nil, io.Discard, nil, &CodexState{Pending: true}, "", nil)
	if err == nil {
		t.Fatal("uncertain delivery replayed")
	}
}
