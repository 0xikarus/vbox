package hostd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/0xikarus/vmbox-service/internal/provider"
	"net/http"
	"strings"
	"time"
)

func Heartbeat(ctx context.Context, client *http.Client, controller, token, id string, p provider.Provider) error {
	if controller == "" {
		return nil
	}
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		cap, err := p.Validate(ctx)
		if err == nil {
			data, _ := json.Marshal(map[string]any{"id": id, "capabilities": cap})
			req, reqErr := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(controller, "/")+"/v1/hosts/heartbeat", bytes.NewReader(data))
			if reqErr == nil {
				req.Header.Set("Authorization", "Bearer "+token)
				req.Header.Set("Content-Type", "application/json")
				resp, doErr := client.Do(req)
				if doErr == nil {
					resp.Body.Close()
					if resp.StatusCode < 200 || resp.StatusCode >= 300 {
						err = fmt.Errorf("controller heartbeat: %s", resp.Status)
					}
				} else {
					err = doErr
				}
			} else {
				err = reqErr
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
		_ = err
	}
}
