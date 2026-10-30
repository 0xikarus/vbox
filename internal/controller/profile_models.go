package controller

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/0xikarus/vmbox-service/internal/loginprofile"
	"github.com/0xikarus/vmbox-service/internal/modelcatalog"
)

type profileModelChoice = modelcatalog.Choice

type profileModelCatalog struct {
	Source string               `json:"source"`
	Models []profileModelChoice `json:"models"`
}

var errClaudeCatalogLogin = modelcatalog.ErrClaudeLogin

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
		var settings struct {
			Env struct {
				Key string `json:"ANTHROPIC_API_KEY"`
			} `json:"env"`
		}
		if json.Unmarshal(profile.Files["settings.json"], &settings) == nil && settings.Env.Key != "" && len(profile.Files[".credentials.json"]) == 0 {
			models, err := s.profileAPIKeyModels(ctx, profileAPIKeyRequest{Application: "claude", Provider: "anthropic", Key: settings.Env.Key})
			if err != nil {
				writeError(w, http.StatusBadGateway, fmt.Errorf("could not load Claude API models"))
				return
			}
			choices := make([]profileModelChoice, 0, len(models))
			for _, model := range models {
				choices = append(choices, profileModelChoice{ID: model, Label: model})
			}
			writeJSON(w, http.StatusOK, profileModelCatalog{Source: "Anthropic API catalog", Models: choices})
			return
		}
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
			writeError(w, http.StatusBadGateway, fmt.Errorf("could not load Claude models (%s); the saved model and documented choices remain selectable", err))
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

func queryClaudeModelCatalog(ctx context.Context, client *http.Client, files map[string][]byte, endpoint string) ([]profileModelChoice, error) {
	return modelcatalog.QueryClaude(ctx, client, files, endpoint)
}
func queryCodexModelCatalog(ctx context.Context, files map[string][]byte, executable string) ([]profileModelChoice, error) {
	return modelcatalog.QueryCodex(ctx, files, executable)
}

func queryOpenCodeModelCatalog(ctx context.Context, client *http.Client, provider, key, endpoint string) ([]profileModelChoice, error) {
	return queryOpenCodeModelCatalogFiltered(ctx, client, provider, key, endpoint, true)
}

func queryOpenCodeModelCatalogFiltered(ctx context.Context, client *http.Client, provider, key, endpoint string, requireTools bool) ([]profileModelChoice, error) {
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
			eligible = containsModelValue(model.Architecture.OutputModalities, "text") && (!requireTools || containsModelValue(model.SupportedParameters, "tools") || containsModelValue(model.SupportedParameters, "tool_choice"))
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
		return nil, fmt.Errorf("provider returned no compatible text models")
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
