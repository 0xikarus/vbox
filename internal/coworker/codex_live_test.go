package coworker

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type lockedOutput struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (o *lockedOutput) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.buffer.Write(p)
}
func (o *lockedOutput) text() string { o.mu.Lock(); defer o.mu.Unlock(); return o.buffer.String() }

// Opt-in: real installed Codex and existing authentication, synthetic controller
// transport only. No agent response text or completion state is fabricated.
func TestLiveCodexAdapter(t *testing.T) {
	if os.Getenv("VMBOX_TEST_LIVE_CODEX") != "1" {
		t.Skip("requires explicitly enabled live Codex")
	}
	base := t.TempDir()
	t.Chdir(base)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/coworker/events" {
			json.NewEncoder(w).Encode([]Event{{Sequence: 7, Sender: "test-owner", Kind: "owner_message", Data: json.RawMessage(`{"text":"What is todays date? Reply briefly. Do not change files."}`)}})
			return
		}
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if json.NewDecoder(r.Body).Decode(&req) != nil {
			w.WriteHeader(400)
			return
		}
		if len(req.ID) == 0 {
			w.WriteHeader(202)
			return
		}
		var result any = map[string]any{}
		if req.Method == "initialize" {
			result = map[string]any{"protocolVersion": "2025-11-25", "serverInfo": map[string]string{"name": "test-controller", "version": "1"}, "capabilities": map[string]any{"tools": map[string]any{}}}
		}
		if req.Method == "tools/list" {
			result = map[string]any{"tools": []any{}}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	path := filepath.Join(base, "state.json")
	var output lockedOutput
	done := make(chan error, 1)
	go func() {
		done <- (Client{URL: server.URL, Token: "synthetic-test-box-token", observeCodex: func(m codexMessage) {
			if os.Getenv("VMBOX_TEST_CODEX_TRACE") != "1" {
				return
			}
			var p struct {
				Item struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"item"`
				Delta string `json:"delta"`
			}
			json.Unmarshal(m.Params, &p)
			t.Logf("event=%s item=%s textBytes=%d deltaBytes=%d", m.Method, p.Item.Type, len(p.Item.Text), len(p.Delta))
		}}).Codex(ctx, path, "", &output)
	}()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case err := <-done:
			t.Fatalf("adapter stopped before completion checkpoint: %v", err)
		case <-ctx.Done():
			t.Fatal("real adapter date task timed out")
		case <-ticker.C:
			data, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			var state CodexState
			if json.Unmarshal(data, &state) == nil && state.After == 7 && !state.Pending {
				cancel()
				<-done
				if output.text() == "" {
					t.Fatal("agent completed with no visible response")
				}
				t.Logf("Real agent response: %s", output.text())
				return
			}
		}
	}
}
