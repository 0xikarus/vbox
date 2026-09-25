package boxruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

const codexTUIProxyPrefix = "vmbox-internal-codex-proxy-"

func codexTUIProxyPort(session string) int {
	hash := fnv.New32a()
	_, _ = hash.Write([]byte("codex-tui:" + session))
	return 40_000 + int(hash.Sum32()%10_000)
}

func codexTUIProxyURL(session string) string {
	return fmt.Sprintf("ws://127.0.0.1:%d", codexTUIProxyPort(session))
}

type codexTUIState struct {
	mu         sync.Mutex
	generation uint64
	live       map[uint64]bool
	threadID   string
	root       string
	session    string
}

func (s *codexTUIState) begin() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.generation++
	if s.live == nil {
		s.live = make(map[uint64]bool)
	}
	if len(s.live) == 0 {
		s.threadID = ""
		_ = os.Remove(codexThreadFile(s.root, s.session))
	}
	s.live[s.generation] = true
	return s.generation
}

func (s *codexTUIState) bind(generation uint64, threadID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.live[generation] {
		return nil
	}
	if err := rememberCodexThread(s.root, s.session, threadID); err != nil {
		return err
	}
	s.threadID = threadID
	return nil
}

func (s *codexTUIState) end(generation uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.live, generation)
	if len(s.live) == 0 {
		s.threadID = ""
		_ = os.Remove(codexThreadFile(s.root, s.session))
	}
}

func (s *codexTUIState) snapshot() (bool, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.live) > 0, s.threadID
}

// ServeCodexTUIProxy observes the thread/start and thread/resume replies on
// the TUI's own connection. The app server has no API for querying which of
// several loaded threads a remote TUI currently displays.
func ServeCodexTUIProxy(ctx context.Context, root, session string) error {
	if err := validateTmuxToken("session", session); err != nil {
		return err
	}
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", codexTUIProxyPort(session)))
	if err != nil {
		return err
	}
	state := &codexTUIState{root: root, session: session}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /state", func(w http.ResponseWriter, _ *http.Request) {
		connected, threadID := state.snapshot()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(struct {
			Connected bool   `json:"connected"`
			ThreadID  string `json:"threadId"`
		}{connected, threadID})
	})
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		upstream, _, err := websocket.Dial(r.Context(), codexAppServerURL(session), nil)
		if err != nil {
			log.Printf("Codex TUI proxy backend unavailable: %v", err)
			http.Error(w, "Codex app server unavailable", http.StatusBadGateway)
			return
		}
		defer upstream.CloseNow()
		terminal, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			log.Printf("Codex TUI proxy connection rejected: %v", err)
			return
		}
		defer terminal.CloseNow()
		terminal.SetReadLimit(32 << 20)
		upstream.SetReadLimit(32 << 20)
		generation := state.begin()
		defer state.end(generation)
		proxyCtx, cancel := context.WithCancel(r.Context())
		defer cancel()
		requests := make(chan string, 64)
		failures := make(chan error, 2)
		go func() { failures <- forwardCodexTUI(proxyCtx, terminal, upstream, requests) }()
		go func() { failures <- forwardCodexServer(proxyCtx, upstream, terminal, requests, state, generation) }()
		if err := <-failures; err != nil {
			log.Printf("Codex TUI proxy connection closed: %v", err)
		}
		cancel()
		_ = terminal.Close(websocket.StatusNormalClosure, "")
		_ = upstream.Close(websocket.StatusNormalClosure, "")
	})
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() { <-ctx.Done(); _ = server.Close() }()
	err = server.Serve(listener)
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

func forwardCodexTUI(ctx context.Context, from, to *websocket.Conn, requests chan<- string) error {
	for {
		kind, data, err := from.Read(ctx)
		if err != nil {
			return err
		}
		var message struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if json.Unmarshal(data, &message) == nil && len(message.ID) > 0 && (message.Method == "thread/start" || message.Method == "thread/resume" || message.Method == "thread/fork") {
			select {
			case requests <- string(message.ID):
			default:
				return fmt.Errorf("Codex TUI thread request buffer full")
			}
		}
		if err := to.Write(ctx, kind, data); err != nil {
			return err
		}
	}
}

func forwardCodexServer(ctx context.Context, from, to *websocket.Conn, requests <-chan string, state *codexTUIState, generation uint64) error {
	pending := make(map[string]bool)
	for {
		kind, data, err := from.Read(ctx)
		if err != nil {
			return err
		}
		for {
			select {
			case id := <-requests:
				pending[id] = true
			default:
				goto drained
			}
		}
	drained:
		var message struct {
			ID     json.RawMessage `json:"id"`
			Result struct {
				Thread struct {
					ID        string `json:"id"`
					Ephemeral bool   `json:"ephemeral"`
				} `json:"thread"`
			} `json:"result"`
		}
		if json.Unmarshal(data, &message) == nil && pending[string(message.ID)] {
			delete(pending, string(message.ID))
			// Codex can start an auxiliary ephemeral thread on the TUI's
			// connection after a real turn. It cannot accept queued chat and
			// does not replace the conversation shown in the terminal.
			if message.Result.Thread.ID != "" && !message.Result.Thread.Ephemeral {
				if err := state.bind(generation, message.Result.Thread.ID); err != nil {
					return err
				}
			}
		}
		if err := to.Write(ctx, kind, data); err != nil {
			return err
		}
	}
}

func codexTUIProxyState(ctx context.Context, session string) (bool, string, error) {
	url := strings.Replace(codexTUIProxyURL(session), "ws://", "http://", 1) + "/state"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false, "", err
	}
	response, err := (&http.Client{Timeout: 2 * time.Second}).Do(req)
	if err != nil {
		return false, "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return false, "", fmt.Errorf("Codex TUI proxy unavailable")
	}
	var state struct {
		Connected bool   `json:"connected"`
		ThreadID  string `json:"threadId"`
	}
	if err := json.NewDecoder(response.Body).Decode(&state); err != nil {
		return false, "", err
	}
	return state.Connected, state.ThreadID, nil
}

var EnsureCodexTUIProxy = func(ctx context.Context, root, session string) error {
	if _, _, err := codexTUIProxyState(ctx, session); err == nil {
		return nil
	}
	name := codexTUIProxyPrefix + session
	if _, err := tmuxCommand(ctx, "", "has-session", "-t", "="+name); err == nil {
		_, _ = tmuxCommand(ctx, "", "kill-session", "-t", "="+name)
	}
	if _, err := tmuxCommand(ctx, "", "new-session", "-d", "-s", name, "-c", WorkspaceDirectory(), "--", "vmbox-runtime", "codex-tui-proxy", session); err != nil {
		return fmt.Errorf("start Codex TUI proxy: %w", err)
	}
	deadline := time.NewTimer(agentReadyTimeout)
	defer deadline.Stop()
	for {
		if _, _, err := codexTUIProxyState(ctx, session); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("Codex TUI proxy did not start")
		case <-time.After(agentReadyPollInterval):
		}
	}
}
