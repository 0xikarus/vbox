package workerprotocol

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func pair(t *testing.T) (*Peer, *Peer) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	accepted := make(chan *Peer, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		p := New(ctx, c, false)
		accepted <- p
		<-p.Done()
	}))
	t.Cleanup(server.Close)
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	a := New(ctx, c, true)
	b := <-accepted
	t.Cleanup(func() { a.Close(); b.Close() })
	return a, b
}
func request(id string) Request {
	return Request{OperationID: id, Argv: []string{"test"}, Binding: Binding{AccountID: "disposable-account", BoxID: "disposable-box", SlotID: "disposable-slot", Assignment: "1", Incarnation: "agent-1"}}
}

func TestMuxTransfersMoreThanWindowAndHalfCloses(t *testing.T) {
	a, b := pair(t)
	out, err := a.Open(context.Background(), request("op-1"))
	if err != nil {
		t.Fatal(err)
	}
	in, err := b.Accept(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	payload := bytes.Repeat([]byte("some terminal data\x00\xff"), 40000)
	result := make(chan error, 1)
	go func() {
		_, err := out.Write(payload)
		if err == nil {
			err = out.CloseWrite()
		}
		result <- err
	}()
	got, err := io.ReadAll(in)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatal("stream data changed")
	}
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	go func() { in.Write([]byte("reply")); in.CloseWrite() }()
	got, err = io.ReadAll(out)
	if err != nil || string(got) != "reply" {
		t.Fatalf("reply %q: %v", got, err)
	}
}

func TestSlowStreamDoesNotBlockAnotherStream(t *testing.T) {
	a, b := pair(t)
	slow, err := a.Open(context.Background(), request("slow"))
	if err != nil {
		t.Fatal(err)
	}
	receiver, err := b.Accept(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	stopped := make(chan struct{})
	go func() { slow.Write(make([]byte, chunkSize*(window+2))); close(stopped) }()
	fast, err := a.Open(context.Background(), request("fast"))
	if err != nil {
		t.Fatal(err)
	}
	other, err := b.Accept(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	go func() { fast.Write([]byte("live")); fast.CloseWrite() }()
	got, err := io.ReadAll(other)
	if err != nil || string(got) != "live" {
		t.Fatalf("other stream %q: %v", got, err)
	}
	receiver.Close()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("cancelled sender stuck")
	}
}

func TestDisconnectDoesNotReportCleanEOF(t *testing.T) {
	a, b := pair(t)
	out, err := a.Open(context.Background(), request("ambiguous"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = b.Accept(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	b.Close()
	_, err = io.ReadAll(out)
	if err == nil || err == io.EOF {
		t.Fatalf("disconnect looked successful: %v", err)
	}
}

func TestMuxRejectsDuplicateStreamAndWindowOverflow(t *testing.T) {
	for _, duplicate := range []bool{false, true} {
		a, b := pair(t)
		out, err := a.Open(context.Background(), request("invalid"))
		if err != nil {
			t.Fatal(err)
		}
		_, err = b.Accept(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if duplicate {
			req := request("duplicate")
			if err = b.receive(frame{Kind: "open", ID: out.ID, Request: &req}); err == nil {
				t.Fatal("accepted duplicate")
			}
		} else {
			for range window {
				if err = b.receive(frame{Kind: "data", ID: out.ID, Data: []byte("x")}); err != nil {
					t.Fatal(err)
				}
			}
			if err = b.receive(frame{Kind: "data", ID: out.ID, Data: []byte("x")}); err == nil {
				t.Fatal("accepted receive-window overflow")
			}
		}
	}
}

func TestBindingControlCannotCarryExecutableArguments(t *testing.T) {
	a, b := pair(t)
	req := request("assignment")
	next := req.Binding
	next.Assignment = "next"
	req.Rebind = &next
	if _, err := a.Open(context.Background(), req); err == nil {
		t.Fatal("mixed execution/control request accepted")
	}
	req.Argv = nil
	out, err := a.Open(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	in, err := b.Accept(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	if in.Request.Rebind == nil || *in.Request.Rebind != next || len(in.Request.Argv) != 0 {
		t.Fatal("binding control altered in transit")
	}
	// A consumer that only supports execution must reject control rather than
	// indexing an empty argv or executing arbitrary control input.
	go Execute(context.Background(), in, Journal{Directory: t.TempDir()}, func(context.Context, Binding) error { return nil })
	if _, err := ReadOutput(out, nil, nil); err == nil {
		t.Fatal("execution-only consumer accepted control")
	}
}
