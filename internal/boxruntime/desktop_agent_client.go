package boxruntime

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/0xikarus/vmbox-service/internal/secrets"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type DesktopAgentConfig struct {
	Controller string `json:"controller"`
	Assignment string `json:"assignment"`
	Token      string `json:"token"`
}

func desktopSecretReference(ctx context.Context, assignment, key string) error {
	_, err := desktopSecretOperation(ctx, assignment, key, false, secrets.PasswordPolicy{})
	return err
}

func desktopSecretOperation(ctx context.Context, assignment, key string, ensure bool, policy secrets.PasswordPolicy, override ...string) (bool, error) {
	if !regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,63}$`).MatchString(key) {
		return false, fmt.Errorf("invalid secret reference")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return false, fmt.Errorf("desktop agent configuration unavailable")
	}
	path := filepath.Join(home, ".config", "vmbox", "desktop-agent.json")
	file, err := os.Open(path)
	if err != nil {
		return false, fmt.Errorf("desktop agent configuration unavailable")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 8192 {
		return false, fmt.Errorf("desktop agent configuration is not private")
	}
	data, err := io.ReadAll(io.LimitReader(file, 8193))
	if err != nil || len(data) > 8192 {
		return false, fmt.Errorf("desktop agent configuration unavailable")
	}
	defer clear(data)
	var config DesktopAgentConfig
	if json.Unmarshal(data, &config) != nil || config.Assignment != assignment || len(config.Token) != 64 {
		return false, fmt.Errorf("desktop agent configuration is stale")
	}
	if _, err = hex.DecodeString(config.Token); err != nil {
		return false, fmt.Errorf("desktop agent credential invalid")
	}
	u, err := url.Parse(config.Controller)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return false, fmt.Errorf("secure controller endpoint required")
	}
	if _, err = NativeSessions(ctx, assignment); err != nil {
		return false, err
	}
	operation := "type"
	var bodyReader io.Reader
	if ensure {
		operation = "ensure"
		if err := policy.Validate(); err != nil {
			return false, err
		}
		payload, _ := json.Marshal(struct {
			Purpose string `json:"purpose"`
			secrets.PasswordPolicy
		}{Purpose: "new_account_password", PasswordPolicy: policy})
		bodyReader = strings.NewReader(string(payload))
	}
	if len(override) > 0 {
		operation = override[0]
		if operation == "request" {
			bodyReader = nil
		}
	}
	endpoint := strings.TrimRight(config.Controller, "/") + "/v1/agent-desktop/secrets/" + url.PathEscape(key) + "/" + operation
	request, err := http.NewRequestWithContext(ctx, "POST", endpoint, bodyReader)
	if err != nil {
		return false, fmt.Errorf("secret request unavailable")
	}
	request.Header.Set("Authorization", "DesktopAgent "+config.Token)
	transport := &http.Transport{Proxy: nil}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 25 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return false, fmt.Errorf("secret entry request failed; inspect the browser before retrying")
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 4097))
	defer clear(body)
	if err != nil || len(body) > 4096 || response.StatusCode != http.StatusOK {
		return false, fmt.Errorf("secret entry rejected; check the reference, site, focused password field and takeover state")
	}
	var status struct {
		Inserted bool   `json:"inserted"`
		Created  bool   `json:"created"`
		Key      string `json:"key"`
		Status   string `json:"status"`
	}
	if json.Unmarshal(body, &status) != nil || (!ensure && !status.Inserted) || (ensure && status.Key != key) {
		return false, fmt.Errorf("secret entry outcome unavailable")
	}
	if operation == "request" {
		if status.Status == "cancelled" {
			return false, fmt.Errorf("private credential request cancelled")
		}
		return status.Status == "fulfilled", nil
	}
	return status.Created, nil
}
