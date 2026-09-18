package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/config"
)

type openCodeAPIProvider struct {
	ID, Label, VerifyURL, ModelsURL string
}

var defaultOpenCodeAPIProviders = []openCodeAPIProvider{
	{ID: "openrouter", Label: "OpenRouter", VerifyURL: "https://openrouter.ai/api/v1/key", ModelsURL: "https://openrouter.ai/api/v1/models"},
	{ID: "venice", Label: "Venice", VerifyURL: "https://api.venice.ai/api/v1/api_keys/rate_limits", ModelsURL: "https://api.venice.ai/api/v1/models?type=text"},
}

type openCodeAPIUploadEntry struct {
	provider, name, key, model, choice *formField
	verifiedInput                      string
	saved                              bool
}

func (a *App) openCodeAPIProviderChoices() []openCodeAPIProvider {
	if len(a.openCodeAPIProviders) > 0 {
		return a.openCodeAPIProviders
	}
	return defaultOpenCodeAPIProviders
}

func openCodeAPIProviderByLabel(providers []openCodeAPIProvider, label string) (openCodeAPIProvider, bool) {
	for _, provider := range providers {
		if provider.Label == label {
			return provider, true
		}
	}
	return openCodeAPIProvider{}, false
}

func newOpenCodeAPIUploadEntry(providers []openCodeAPIProvider) *openCodeAPIUploadEntry {
	labels := make([]string, 0, len(providers))
	for _, provider := range providers {
		labels = append(labels, provider.Label)
	}
	entry := &openCodeAPIUploadEntry{
		provider: &formField{Label: "OpenCode provider", Choices: labels},
		name:     &formField{Label: "Profile name"},
		key:      &formField{Label: "API key", Secret: true},
		model:    &formField{Label: "Model", List: true, ChoiceNoun: "models"},
		choice:   &formField{Label: "Upload entry", Value: "Upload", Choices: []string{"Skip", "Upload"}, Checkbox: true},
	}
	if len(labels) > 0 {
		entry.provider.Value = labels[0]
	}
	entry.model.When = func() bool { return len(entry.model.Choices) > 0 }
	return entry
}

func (a *App) queryOpenCodeAPIModels(ctx context.Context, provider openCodeAPIProvider, key string) ([]string, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, fmt.Errorf("enter the %s API key", provider.Label)
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	var verification struct {
		Data struct {
			AccessPermitted *bool `json:"accessPermitted"`
		} `json:"data"`
	}
	if err := a.getOpenCodeProviderJSON(ctx, provider, provider.VerifyURL, key, &verification); err != nil {
		return nil, err
	}
	if provider.ID == "venice" && (verification.Data.AccessPermitted == nil || !*verification.Data.AccessPermitted) {
		return nil, fmt.Errorf("Venice rejected this API key")
	}

	var catalog struct {
		Data []struct {
			ID                  string   `json:"id"`
			Type                string   `json:"type"`
			SupportedParameters []string `json:"supported_parameters"`
			Architecture        struct {
				OutputModalities []string `json:"output_modalities"`
			} `json:"architecture"`
			ModelSpec struct {
				Offline      bool `json:"offline"`
				Capabilities struct {
					SupportsFunctionCalling bool `json:"supportsFunctionCalling"`
				} `json:"capabilities"`
			} `json:"model_spec"`
		} `json:"data"`
	}
	if err := a.getOpenCodeProviderJSON(ctx, provider, provider.ModelsURL, key, &catalog); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	models := make([]string, 0, len(catalog.Data))
	for _, candidate := range catalog.Data {
		id := strings.TrimSpace(candidate.ID)
		if id == "" {
			continue
		}
		supported := false
		switch provider.ID {
		case "openrouter":
			supported = containsString(candidate.Architecture.OutputModalities, "text") &&
				(containsString(candidate.SupportedParameters, "tools") || containsString(candidate.SupportedParameters, "tool_choice"))
		case "venice":
			supported = candidate.Type == "text" && candidate.ModelSpec.Capabilities.SupportsFunctionCalling && !candidate.ModelSpec.Offline
		}
		model := provider.ID + "/" + id
		if supported && !seen[model] {
			seen[model] = true
			models = append(models, model)
		}
	}
	sort.Strings(models)
	if len(models) == 0 {
		return nil, fmt.Errorf("%s returned no available text models with tool calling", provider.Label)
	}
	return models, nil
}

func (a *App) getOpenCodeProviderJSON(ctx context.Context, provider openCodeAPIProvider, endpoint, key string, target any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("could not prepare %s credential check", provider.Label)
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Accept", "application/json")
	response, err := a.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("could not reach %s to verify the API key", provider.Label)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
			return fmt.Errorf("%s rejected this API key", provider.Label)
		}
		return fmt.Errorf("%s credential check returned HTTP %d", provider.Label, response.StatusCode)
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 8<<20))
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("%s returned an invalid credential-check response", provider.Label)
	}
	return nil
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func (a *App) saveOpenCodeAPIKeyProfile(ctx context.Context, c config.Context, token, name string, provider openCodeAPIProvider, key, model string) (v1.LoginProfile, error) {
	var result v1.LoginProfile
	modelPrefix := provider.ID + "/"
	if !strings.HasPrefix(model, modelPrefix) || strings.TrimPrefix(model, modelPrefix) == "" {
		return result, fmt.Errorf("choose a %s model", provider.Label)
	}
	modelID := strings.TrimPrefix(model, modelPrefix)
	auth, err := json.Marshal(map[string]any{provider.ID: map[string]string{"type": "api", "key": key}})
	if err != nil {
		return result, fmt.Errorf("could not encode OpenCode credentials")
	}
	configuration, err := json.Marshal(map[string]any{
		"$schema": "https://opencode.ai/config.json",
		"model":   model,
		"provider": map[string]any{
			provider.ID: map[string]any{"models": map[string]any{modelID: map[string]any{}}},
		},
	})
	if err != nil {
		clear(auth)
		return result, fmt.Errorf("could not encode OpenCode configuration")
	}
	defer clear(auth)
	defer clear(configuration)
	request := v1.SaveLoginProfileRequest{Files: map[string][]byte{"auth.json": auth, "opencode.json": configuration}}
	_, err = a.request(ctx, c, token, http.MethodPut, "/v1/login-profiles/opencode/"+url.PathEscape(name), request, &result, nil)
	return result, err
}
