// Package browser implements the worker-private Chromium bridge. Protocol payloads
// may contain credentials and must never be returned as diagnostic text.
package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/coder/websocket"
)

// Client serializes calls on a single browser connection. It is not an agent
// tool: callers expose only the specific, destination-checked operations needed.
type Client struct {
	mu   sync.Mutex
	conn *websocket.Conn
	next uint64
}

var errBrowserOperationRejected = errors.New("browser operation rejected")

// Endpoint reads Chromium's private discovery file. Never trust a host or URL
// from that file: Chromium is reachable only over this box's loopback interface.
func Endpoint(profile string) (string, error) {
	data, err := os.ReadFile(filepath.Join(profile, "DevToolsActivePort"))
	if err != nil || len(data) > 4096 {
		return "", fmt.Errorf("managed browser is unavailable")
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		return "", fmt.Errorf("invalid browser endpoint")
	}
	port, err := strconv.Atoi(lines[0])
	if err != nil || port < 1 || port > 65535 {
		return "", fmt.Errorf("invalid browser endpoint")
	}
	path := lines[1]
	if !strings.HasPrefix(path, "/devtools/browser/") || strings.ContainsAny(path, "?#\\ \r\t") || len(strings.TrimPrefix(path, "/devtools/browser/")) == 0 {
		return "", fmt.Errorf("invalid browser endpoint")
	}
	return "ws://127.0.0.1:" + strconv.Itoa(port) + path, nil
}

func Connect(ctx context.Context, profile string) (*Client, error) {
	endpoint, err := Endpoint(profile)
	if err != nil {
		return nil, err
	}
	// Disable proxy environment lookup, including for loopback connections.
	transport := &http.Transport{Proxy: nil}
	conn, _, err := websocket.Dial(ctx, endpoint, &websocket.DialOptions{HTTPClient: &http.Client{Transport: transport}})
	transport.CloseIdleConnections()
	if err != nil {
		return nil, fmt.Errorf("could not connect to managed browser")
	}
	conn.SetReadLimit(16 << 20)
	return &Client{conn: conn}, nil
}

func (c *Client) Close() { _ = c.conn.CloseNow() }

// Call discards unsolicited events, matches response IDs and keeps remote errors
// private. A canceled or broken call closes the connection; retries reconnect.
func (c *Client) Call(ctx context.Context, session, method string, params, result any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	c.next++
	request := struct {
		ID      uint64 `json:"id"`
		Session string `json:"sessionId,omitempty"`
		Method  string `json:"method"`
		Params  any    `json:"params,omitempty"`
	}{c.next, session, method, params}
	data, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("invalid browser request")
	}
	defer clear(data)
	if err = c.conn.Write(ctx, websocket.MessageText, data); err != nil {
		c.Close()
		return fmt.Errorf("browser request failed")
	}
	for {
		_, data, err = c.conn.Read(ctx)
		if err != nil {
			c.Close()
			return fmt.Errorf("browser response unavailable")
		}
		var response struct {
			ID     uint64          `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  json.RawMessage `json:"error"`
		}
		err = json.Unmarshal(data, &response)
		clear(data)
		if err != nil {
			c.Close()
			return fmt.Errorf("invalid browser response")
		}
		if response.ID != c.next {
			clear(response.Result)
			clear(response.Error)
			continue
		}
		defer clear(response.Result)
		defer clear(response.Error)
		if len(response.Error) > 0 {
			return errBrowserOperationRejected
		}
		if result != nil && json.Unmarshal(response.Result, result) != nil {
			return fmt.Errorf("invalid browser operation result")
		}
		return nil
	}
}

// Origin normalizes the secure web destination used for credential bindings.
func Origin(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil {
		return "", fmt.Errorf("secure browser origin required")
	}
	host := strings.ToLower(u.Host)
	if u.Port() == "443" {
		host = strings.TrimSuffix(host, ":443")
	}
	return "https://" + host, nil
}
