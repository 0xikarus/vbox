// Package modelcatalog queries agent catalogs without starting a conversation.
package modelcatalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type Choice struct {
	ID               string   `json:"id"`
	Label            string   `json:"label"`
	Reasoning        bool     `json:"reasoning"`
	ReasoningEfforts []string `json:"reasoningEfforts,omitempty"`
	InputCost        float64  `json:"inputCost,omitempty"`
	OutputCost       float64  `json:"outputCost,omitempty"`
	Context          int      `json:"context,omitempty"`
}

var ErrClaudeLogin = errors.New("saved Claude access token was rejected")

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

// The saved Claude Code OAuth access token can read Anthropic's Models API.
// This lists current model IDs and capabilities; subscription and organization
// restrictions are still enforced by Claude Code when the box uses a model.
func QueryClaude(ctx context.Context, client *http.Client, files map[string][]byte, endpoint string) ([]Choice, error) {
	var credential struct {
		OAuth struct {
			Access string `json:"accessToken"`
		} `json:"claudeAiOauth"`
	}
	if json.Unmarshal(files[".credentials.json"], &credential) != nil || strings.TrimSpace(credential.OAuth.Access) == "" {
		var settings struct {
			Env struct {
				Key string `json:"ANTHROPIC_API_KEY"`
			} `json:"env"`
		}
		if json.Unmarshal(files["settings.json"], &settings) != nil || settings.Env.Key == "" {
			return nil, fmt.Errorf("Claude profile has no access token or API key")
		}
		credential.OAuth.Access = settings.Env.Key
		return queryClaude(ctx, client, endpoint, "x-api-key", credential.OAuth.Access)
	}
	return queryClaude(ctx, client, endpoint, "Authorization", "Bearer "+credential.OAuth.Access)
}

func queryClaude(ctx context.Context, client *http.Client, endpoint, header, key string) ([]Choice, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set(header, key)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("Accept", "application/json")
	response, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Claude catalog request failed")
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		return nil, fmt.Errorf("%w (HTTP %d)", ErrClaudeLogin, response.StatusCode)
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Claude catalog returned HTTP %d", response.StatusCode)
	}
	var catalog struct {
		HasMore bool `json:"has_more"`
		Data    []struct {
			ID             string `json:"id"`
			DisplayName    string `json:"display_name"`
			MaxInputTokens int    `json:"max_input_tokens"`
			Capabilities   struct {
				Effort map[string]json.RawMessage `json:"effort"`
			} `json:"capabilities"`
		} `json:"data"`
	}
	if json.NewDecoder(io.LimitReader(response.Body, 8<<20)).Decode(&catalog) != nil || catalog.HasMore {
		return nil, fmt.Errorf("invalid or incomplete Claude model catalog")
	}
	models := make([]Choice, 0, len(catalog.Data))
	seen := make(map[string]bool, len(catalog.Data))
	for _, model := range catalog.Data {
		id := strings.TrimSpace(model.ID)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		label := strings.TrimSpace(model.DisplayName)
		if label == "" {
			label = id
		}
		efforts := make([]string, 0, 4)
		for _, level := range []string{"low", "medium", "high", "xhigh"} {
			var capability struct {
				Supported bool `json:"supported"`
			}
			if json.Unmarshal(model.Capabilities.Effort[level], &capability) == nil && capability.Supported {
				efforts = append(efforts, level)
			}
		}
		models = append(models, Choice{ID: id, Label: label, Context: model.MaxInputTokens, Reasoning: len(efforts) > 0, ReasoningEfforts: efforts})
	}
	if len(models) == 0 {
		return nil, fmt.Errorf("Claude returned no models")
	}
	return models, nil
}

func QueryCodex(ctx context.Context, files map[string][]byte, executable string) ([]Choice, error) {
	auth := files["auth.json"]
	if len(auth) == 0 {
		return nil, fmt.Errorf("Codex profile has no authentication")
	}
	home, err := os.MkdirTemp("", "vmbox-codex-models-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(home)
	if err := os.Chmod(home, 0o700); err != nil {
		return nil, err
	}
	for _, name := range []string{"auth.json", "config.toml"} {
		data := files[name]
		if len(data) == 0 {
			continue
		}
		if err := os.WriteFile(filepath.Join(home, name), data, 0o600); err != nil {
			return nil, err
		}
	}
	cmd := exec.CommandContext(ctx, executable, "app-server", "--listen", "stdio://")
	cmd.Env = append(os.Environ(), "CODEX_HOME="+home)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	defer func() {
		_ = stdin.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	}()
	encoder := json.NewEncoder(stdin)
	requests := []any{
		map[string]any{"id": 1, "method": "initialize", "params": map[string]any{"clientInfo": map[string]string{"name": "vmbox-model-catalog", "version": "1"}}},
		map[string]any{"method": "initialized"},
		map[string]any{"id": 2, "method": "model/list", "params": map[string]any{"includeHidden": false, "limit": 100}},
	}
	for _, request := range requests {
		if err := encoder.Encode(request); err != nil {
			return nil, err
		}
	}
	decoder := json.NewDecoder(io.LimitReader(stdout, 8<<20))
	for {
		var response struct {
			ID     int             `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  json.RawMessage `json:"error"`
		}
		if err := decoder.Decode(&response); err != nil {
			return nil, err
		}
		if response.ID != 2 {
			continue
		}
		if len(response.Error) > 0 && string(response.Error) != "null" {
			return nil, fmt.Errorf("Codex model list failed")
		}
		var result struct {
			Data []struct {
				ID                        string `json:"id"`
				Model                     string `json:"model"`
				DisplayName               string `json:"displayName"`
				Hidden                    bool   `json:"hidden"`
				SupportedReasoningEfforts []struct {
					ReasoningEffort string `json:"reasoningEffort"`
				} `json:"supportedReasoningEfforts"`
			} `json:"data"`
		}
		if json.Unmarshal(response.Result, &result) != nil {
			return nil, fmt.Errorf("invalid Codex model catalog")
		}
		models := make([]Choice, 0, len(result.Data))
		seen := map[string]bool{}
		for _, model := range result.Data {
			id := strings.TrimSpace(model.Model)
			if id == "" {
				id = strings.TrimSpace(model.ID)
			}
			if id == "" || model.Hidden || seen[id] {
				continue
			}
			seen[id] = true
			label := strings.TrimSpace(model.DisplayName)
			if label == "" {
				label = id
			}
			efforts := make([]string, 0, len(model.SupportedReasoningEfforts))
			for _, option := range model.SupportedReasoningEfforts {
				if effort := strings.TrimSpace(option.ReasoningEffort); effort != "" {
					efforts = append(efforts, effort)
				}
			}
			models = append(models, Choice{ID: id, Label: label, Reasoning: len(efforts) > 0, ReasoningEfforts: uniqueStrings(efforts)})
		}
		if len(models) == 0 {
			return nil, fmt.Errorf("Codex returned no selectable models")
		}
		return models, nil
	}
}
