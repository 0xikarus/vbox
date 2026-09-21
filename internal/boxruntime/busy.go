package boxruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// DesktopSetBusy reports activity through the assignment-scoped desktop-agent
// credential. The controller persists the state; a worker-local marker would
// survive crashes and restores without proving that the agent is still busy.
var DesktopSetBusy = desktopSetBusy

func desktopSetBusy(ctx context.Context, assignment, session string, busy bool) error {
	if err := validateTmuxToken("session", session); err != nil {
		return err
	}
	config, err := readDesktopAgentConfig(assignment)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(map[string]any{"session": session, "busy": busy})
	if err != nil {
		return fmt.Errorf("agent activity unavailable")
	}
	endpoint := strings.TrimRight(config.Controller, "/") + "/v1/agent-desktop/busy"
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(payload)))
	if err != nil {
		return fmt.Errorf("agent activity unavailable")
	}
	request.Header.Set("Authorization", "DesktopAgent "+config.Token)
	request.Header.Set("Content-Type", "application/json")
	transport := &http.Transport{Proxy: nil}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("agent activity request failed")
	}
	defer response.Body.Close()
	body, readErr := io.ReadAll(io.LimitReader(response.Body, 4097))
	if readErr != nil || len(body) > 4096 || response.StatusCode != http.StatusOK {
		return fmt.Errorf("agent activity rejected; check the chat session and box assignment")
	}
	return nil
}
