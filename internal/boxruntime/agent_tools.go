package boxruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

func desktopAgentAPI(ctx context.Context, assignment, method, path string, input, output any) error {
	return desktopAgentAPIWithKey(ctx, assignment, method, path, "", input, output)
}

func desktopAgentAPIWithKey(ctx context.Context, assignment, method, path, idempotencyKey string, input, output any) error {
	config, err := readDesktopAgentConfig(assignment)
	if err != nil {
		return err
	}
	var body io.Reader
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(config.Controller, "/")+path, body)
	if err != nil {
		return fmt.Errorf("controller request unavailable")
	}
	request.Header.Set("Authorization", "DesktopAgent "+config.Token)
	if idempotencyKey != "" {
		request.Header.Set("Idempotency-Key", idempotencyKey)
	}
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	transport := &http.Transport{Proxy: nil}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("controller request failed")
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("controller response unavailable")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var problem struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(data, &problem)
		if problem.Error != "" {
			return fmt.Errorf("%s", problem.Error)
		}
		return fmt.Errorf("controller rejected request")
	}
	if output != nil && len(data) > 0 {
		if err := json.Unmarshal(data, output); err != nil {
			return fmt.Errorf("controller response invalid")
		}
	}
	return nil
}

func desktopToolJSON(value any) (map[string]any, error) {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, err
	}
	return map[string]any{"content": []map[string]any{{"type": "text", "text": string(encoded)}}, "structuredContent": value}, nil
}

func desktopAgentToolPolicy(ctx context.Context, assignment string) (map[string]bool, error) {
	var response struct {
		Tools []string `json:"tools"`
	}
	if err := desktopAgentAPI(ctx, assignment, http.MethodGet, "/v1/agent-desktop/tool-policy", nil, &response); err != nil {
		return nil, err
	}
	allowed := make(map[string]bool, len(response.Tools))
	for _, name := range response.Tools {
		allowed[name] = true
	}
	return allowed, nil
}
