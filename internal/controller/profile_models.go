package controller

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/0xikarus/vmbox-service/internal/loginprofile"
)

type profileModelChoice struct {
	ID               string   `json:"id"`
	Label            string   `json:"label"`
	Reasoning        bool     `json:"reasoning"`
	ReasoningEfforts []string `json:"reasoningEfforts,omitempty"`
	InputCost        float64  `json:"inputCost,omitempty"`
	OutputCost       float64  `json:"outputCost,omitempty"`
	Context          int      `json:"context,omitempty"`
}

type profileModelCatalog struct {
	Source string               `json:"source"`
	Models []profileModelChoice `json:"models"`
}

var errClaudeCatalogLogin = errors.New("saved Claude access token was rejected")

// getLoginProfileModels queries the selected harness with the saved profile on
// demand. Codex ChatGPT logins are passed only to Codex app-server; they are not
// API keys and must never be sent to the public OpenAI models endpoint.
func (s *Server) getLoginProfileModels(w http.ResponseWriter, r *http.Request, p Principal) {
	w.Header().Set("Cache-Control", "no-store")
	application := r.PathValue("application")
	if application != "claude" && application != "codex" && application != "opencode" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("live model catalogs are available for Claude, Codex and OpenCode profiles only"))
		return
	}
	profile, err := s.Store.LoadLoginProfile(r.Context(), p, application, r.PathValue("name"))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, fmt.Errorf("login profile not found"))
		} else {
			writeError(w, http.StatusInternalServerError, fmt.Errorf("could not read login profile"))
		}
		return
	}
	defer func() {
		for _, data := range profile.Files {
			clear(data)
		}
	}()
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	if application == "claude" {
		client := s.HTTP
		if client == nil {
			client = http.DefaultClient
		}
		models, err := queryClaudeModelCatalog(ctx, client, profile.Files, "https://api.anthropic.com/v1/models?limit=1000")
		if err != nil {
			if errors.Is(err, errClaudeCatalogLogin) {
				writeError(w, http.StatusBadGateway, fmt.Errorf("saved Claude login was rejected by Anthropic; re-upload this profile to refresh it"))
				return
			}
			writeError(w, http.StatusBadGateway, fmt.Errorf("could not load Claude models; the saved model and documented choices remain selectable"))
			return
		}
		writeJSON(w, http.StatusOK, profileModelCatalog{Source: "Anthropic live catalog · model access checked when used", Models: models})
		return
	}
	if application == "codex" {
		models, err := queryCodexModelCatalog(ctx, profile.Files, "codex")
		if err != nil {
			writeError(w, http.StatusBadGateway, fmt.Errorf("could not load Codex models; the saved model remains selectable"))
			return
		}
		writeJSON(w, http.StatusOK, profileModelCatalog{Source: "Codex account catalog", Models: models})
		return
	}
	selected := loginprofile.Model("opencode", profile.Files)
	provider := strings.SplitN(selected, "/", 2)[0]
	if provider != "openrouter" && provider != "venice" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("this profile has no supported OpenRouter or Venice model catalog"))
		return
	}
	var credentials map[string]struct {
		Type string `json:"type"`
		Key  string `json:"key"`
	}
	if json.Unmarshal(profile.Files["auth.json"], &credentials) != nil || credentials[provider].Type != "api" || credentials[provider].Key == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("this profile has no supported provider API key"))
		return
	}
	endpoint := "https://openrouter.ai/api/v1/models"
	source := "OpenRouter live catalog"
	if provider == "venice" {
		endpoint = "https://api.venice.ai/api/v1/models?type=text"
		source = "Venice live catalog"
	}
	client := s.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	models, err := queryOpenCodeModelCatalog(ctx, client, provider, credentials[provider].Key, endpoint)
	if err != nil {
		writeError(w, http.StatusBadGateway, fmt.Errorf("could not load %s models; the saved model remains selectable", provider))
		return
	}
	writeJSON(w, http.StatusOK, profileModelCatalog{Source: source, Models: models})
}

// The saved Claude Code OAuth access token can read Anthropic's Models API.
// This lists current model IDs and capabilities; subscription and organization
// restrictions are still enforced by Claude Code when the box uses a model.
func queryClaudeModelCatalog(ctx context.Context, client *http.Client, files map[string][]byte, endpoint string) ([]profileModelChoice, error) {
	var credential struct {
		OAuth struct {
			Access string `json:"accessToken"`
		} `json:"claudeAiOauth"`
	}
	if json.Unmarshal(files[".credentials.json"], &credential) != nil || strings.TrimSpace(credential.OAuth.Access) == "" {
		return nil, fmt.Errorf("Claude profile has no access token")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+credential.OAuth.Access)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("Accept", "application/json")
	response, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Claude catalog request failed")
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		return nil, fmt.Errorf("%w (HTTP %d)", errClaudeCatalogLogin, response.StatusCode)
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
				Effort map[string]struct {
					Supported bool `json:"supported"`
				} `json:"effort"`
			} `json:"capabilities"`
		} `json:"data"`
	}
	if json.NewDecoder(io.LimitReader(response.Body, 8<<20)).Decode(&catalog) != nil || catalog.HasMore {
		return nil, fmt.Errorf("invalid or incomplete Claude model catalog")
	}
	models := make([]profileModelChoice, 0, len(catalog.Data))
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
			if model.Capabilities.Effort[level].Supported {
				efforts = append(efforts, level)
			}
		}
		models = append(models, profileModelChoice{ID: id, Label: label, Context: model.MaxInputTokens, Reasoning: len(efforts) > 0, ReasoningEfforts: efforts})
	}
	if len(models) == 0 {
		return nil, fmt.Errorf("Claude returned no models")
	}
	return models, nil
}

func queryCodexModelCatalog(ctx context.Context, files map[string][]byte, executable string) ([]profileModelChoice, error) {
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
	cmd := exec.CommandContext(ctx, executable, "app-server", "--stdio")
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
		models := make([]profileModelChoice, 0, len(result.Data))
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
			models = append(models, profileModelChoice{ID: id, Label: label, Reasoning: len(efforts) > 0, ReasoningEfforts: cleanUniqueStrings(efforts)})
		}
		if len(models) == 0 {
			return nil, fmt.Errorf("Codex returned no selectable models")
		}
		return models, nil
	}
}

func queryOpenCodeModelCatalog(ctx context.Context, client *http.Client, provider, key, endpoint string) ([]profileModelChoice, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Accept", "application/json")
	response, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("provider returned HTTP %d", response.StatusCode)
	}
	var catalog struct {
		Data []struct {
			ID                  string   `json:"id"`
			Name                string   `json:"name"`
			Type                string   `json:"type"`
			SupportedParameters []string `json:"supported_parameters"`
			Architecture        struct {
				OutputModalities []string `json:"output_modalities"`
				InputModalities  []string `json:"input_modalities"`
			} `json:"architecture"`
			ContextLength int `json:"context_length"`
			Pricing       struct {
				Prompt     string `json:"prompt"`
				Completion string `json:"completion"`
			} `json:"pricing"`
			ModelSpec struct {
				Offline      bool `json:"offline"`
				Capabilities struct {
					SupportsFunctionCalling bool `json:"supportsFunctionCalling"`
					SupportsReasoningEffort bool `json:"supportsReasoningEffort"`
				} `json:"capabilities"`
			} `json:"model_spec"`
		} `json:"data"`
	}
	if json.NewDecoder(io.LimitReader(response.Body, 8<<20)).Decode(&catalog) != nil {
		return nil, fmt.Errorf("invalid provider model catalog")
	}
	models := make([]profileModelChoice, 0, len(catalog.Data))
	seen := map[string]bool{}
	for _, model := range catalog.Data {
		id := strings.TrimSpace(model.ID)
		if id == "" || seen[id] {
			continue
		}
		eligible := false
		switch provider {
		case "openrouter":
			eligible = containsModelValue(model.Architecture.OutputModalities, "text") && (containsModelValue(model.SupportedParameters, "tools") || containsModelValue(model.SupportedParameters, "tool_choice"))
		case "venice":
			eligible = model.Type == "text" && model.ModelSpec.Capabilities.SupportsFunctionCalling && !model.ModelSpec.Offline
		}
		if !eligible {
			continue
		}
		seen[id] = true
		label := strings.TrimSpace(model.Name)
		if label == "" {
			label = id
		}
		reasoning := containsModelValue(model.SupportedParameters, "reasoning") || containsModelValue(model.SupportedParameters, "reasoning_effort")
		if provider == "venice" {
			reasoning = model.ModelSpec.Capabilities.SupportsReasoningEffort
		}
		choice := profileModelChoice{ID: provider + "/" + id, Label: label, Reasoning: reasoning, Context: model.ContextLength}
		if promptCost, err := strconv.ParseFloat(model.Pricing.Prompt, 64); err == nil && promptCost > 0 {
			choice.InputCost = promptCost * 1e6
		}
		if completionCost, err := strconv.ParseFloat(model.Pricing.Completion, 64); err == nil && completionCost > 0 {
			choice.OutputCost = completionCost * 1e6
		}
		models = append(models, choice)
	}
	sort.Slice(models, func(i, j int) bool { return models[i].ID < models[j].ID })
	if len(models) == 0 {
		return nil, fmt.Errorf("provider returned no tool-capable text models")
	}
	return models, nil
}

func containsModelValue(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
