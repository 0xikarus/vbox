package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/0xikarus/vmbox-service/internal/config"
	"github.com/0xikarus/vmbox-service/internal/provider"
	"github.com/0xikarus/vmbox-service/internal/workerprotocol"
	"github.com/coder/websocket"
	"github.com/creack/pty"
	"golang.org/x/term"
)

func TestResolvedWorkerTerminalFramesAndCredentials(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	binding := workerprotocol.Binding{AccountID: "account", BoxID: "box", SlotID: "slot", Assignment: "fence", Incarnation: "agent"}
	payload := bytes.Repeat([]byte{0, 255, 'x', '\r', '\n'}, 20000)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/logical-boxes/box/worker/stream" || r.Header.Get("Authorization") != "Bearer replacement" {
			t.Error("incorrect controller authentication or route")
			http.Error(w, "denied", 403)
			return
		}
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		peer := workerprotocol.New(ctx, ws, false)
		defer peer.Close()
		stream, err := peer.Accept(ctx)
		if err != nil {
			t.Error(err)
			return
		}
		if stream.Request.Binding != binding || !reflect.DeepEqual(stream.Request.Argv, []string{"vmbox-runtime", "web-terminal", "fence", "$1", "tmux"}) {
			t.Errorf("invalid terminal handoff: %+v", stream.Request)
			return
		}
		decoder := json.NewDecoder(stream)
		var received bytes.Buffer
		for {
			var frame struct {
				Data []byte `json:"data"`
			}
			if err := decoder.Decode(&frame); err == io.EOF {
				break
			} else if err != nil {
				t.Error(err)
				return
			}
			received.Write(frame.Data)
		}
		if !bytes.Equal(received.Bytes(), payload) {
			t.Error("terminal input corrupted")
		}
		encoder := json.NewEncoder(stream)
		_ = encoder.Encode(workerprotocol.Output{Kind: "stdout", Data: []byte("terminal-output")})
		code := 7
		_ = encoder.Encode(workerprotocol.Output{Kind: "exit", ExitCode: &code})
		_ = stream.CloseWrite()
		select {
		case <-peer.Done():
		case <-ctx.Done():
		}
	}))
	defer server.Close()
	a := &App{HTTP: server.Client(), authReplacements: map[string]string{server.URL + "\x00old": "replacement"}}
	conn := provider.Connection{Transport: "controller-worker", Endpoint: "ignored-provider-address", Metadata: map[string]string{"accountId": binding.AccountID, "boxId": binding.BoxID, "slotId": binding.SlotID, "assignment": binding.Assignment, "connectionRevision": binding.Incarnation}}
	var output bytes.Buffer
	result, err := a.resolvedExec(ctx, config.Context{Controller: server.URL}, "old", conn, []string{"vmbox-runtime", "native-attach", "fence", "$1", "tmux"}, provider.ExecOptions{Interactive: true, Stdin: bytes.NewReader(payload), Stdout: &output}, false)
	if err != nil || result.ExitCode != 7 || output.String() != "terminal-output" || result.Stdout != "" {
		t.Fatalf("terminal result: %+v %q %v", result, output.String(), err)
	}
}

func TestWorkerTerminalInputCancellationPreservesNextAttachment(t *testing.T) {
	master, terminal, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer master.Close()
	defer terminal.Close()
	state, err := term.MakeRaw(int(terminal.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	defer term.Restore(int(terminal.Fd()), state)
	input, closeInput := framedWorkerInput(context.Background(), terminal)
	// Drain the initial real PTY size frame, proving the input pump is active.
	var size struct {
		Cols int `json:"cols"`
		Rows int `json:"rows"`
	}
	if err := json.NewDecoder(input).Decode(&size); err != nil {
		t.Fatal(err)
	}
	if size.Cols < 2 || size.Rows < 2 {
		t.Fatalf("invalid initial size: %+v", size)
	}
	done := make(chan struct{})
	go func() { closeInput(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("terminal input remained blocked after disconnect")
	}
	if _, err := master.Write([]byte("fresh-input")); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	data := make([]byte, 32)
	n, err := (workerTerminalReader{ctx: ctx, file: terminal}).Read(data)
	if err != nil || string(data[:n]) != "fresh-input" {
		t.Fatalf("old attachment consumed new input: %q %v", data[:n], err)
	}
}
