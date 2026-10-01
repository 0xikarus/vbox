package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
)

type profileAPIKeyRequest struct {
	Application     string `json:"application"`
	Provider        string `json:"provider"`
	Name            string `json:"name"`
	Key             string `json:"key"`
	Model           string `json:"model"`
	ReplaceExisting bool   `json:"replaceExisting"`
}

func (r profileAPIKeyRequest) valid() bool {
	if len(r.Key) < 8 || len(r.Key) > 2048 || strings.TrimSpace(r.Key) != r.Key || strings.ContainsAny(r.Key, "\r\n\x00") {
		return false
	}
	switch r.Application + "/" + r.Provider {
	case "codex/openai", "claude/anthropic", "opencode/openrouter", "opencode/venice":
		return true
	default:
		return false
	}
}

func decodeProfileAPIKeyRequest(w http.ResponseWriter, r *http.Request) (profileAPIKeyRequest, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	var request profileAPIKeyRequest
	if decodeJSON(r, &request) != nil || !request.valid() {
		writeError(w, 400, fmt.Errorf("choose a supported provider and enter its API key"))
		return request, false
	}
	return request, true
}

func (s *Server) profileKeyClient() *http.Client {
	base := s.HTTP
	if base == nil {
		base = http.DefaultClient
	}
	copy := *base
	copy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &copy
}

func profileKeyGET(ctx context.Context, client *http.Client, endpoint, provider, key string, target any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("could not prepare provider check")
	}
	if provider == "anthropic" {
		req.Header.Set("x-api-key", key)
		req.Header.Set("anthropic-version", "2023-06-01")
	} else {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	req.Header.Set("Accept", "application/json")
	response, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("could not reach %s for credential check", provider)
	}
	defer response.Body.Close()
	if response.StatusCode == 401 || response.StatusCode == 403 {
		return fmt.Errorf("%s rejected this API key", provider)
	}
	if response.StatusCode != 200 {
		return fmt.Errorf("%s credential check returned HTTP %d", provider, response.StatusCode)
	}
	if json.NewDecoder(io.LimitReader(response.Body, 8<<20)).Decode(target) != nil {
		return fmt.Errorf("%s returned an invalid credential-check response", provider)
	}
	return nil
}

func (s *Server) profileAPIKeyModels(ctx context.Context, req profileAPIKeyRequest) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	client := s.profileKeyClient()
	if req.Application == "opencode" {
		endpoint := "https://openrouter.ai/api/v1/key"
		modelsEndpoint := "https://openrouter.ai/api/v1/models"
		if req.Provider == "venice" {
			endpoint = "https://api.venice.ai/api/v1/api_keys/rate_limits"
			modelsEndpoint = "https://api.venice.ai/api/v1/models?type=text"
		}
		var checked struct {
			Data struct {
				AccessPermitted *bool `json:"accessPermitted"`
			} `json:"data"`
		}
		if err := profileKeyGET(ctx, client, endpoint, req.Provider, req.Key, &checked); err != nil {
			return nil, err
		}
		if req.Provider == "venice" && (checked.Data.AccessPermitted == nil || !*checked.Data.AccessPermitted) {
			return nil, fmt.Errorf("Venice rejected this API key")
		}
		choices, err := queryOpenCodeModelCatalog(ctx, client, req.Provider, req.Key, modelsEndpoint)
		if err != nil {
			return nil, fmt.Errorf("could not list %s models", req.Provider)
		}
		models := make([]string, 0, len(choices))
		for _, choice := range choices {
			models = append(models, choice.ID)
		}
		return models, nil
	}
	endpoint := "https://api.openai.com/v1/models"
	if req.Provider == "anthropic" {
		endpoint = "https://api.anthropic.com/v1/models?limit=1000"
	}
	var catalog struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := profileKeyGET(ctx, client, endpoint, req.Provider, req.Key, &catalog); err != nil {
		return nil, err
	}
	models := make([]string, 0, len(catalog.Data))
	seen := map[string]bool{}
	for _, item := range catalog.Data {
		id := strings.TrimSpace(item.ID)
		if id == "" || len(id) > 200 || seen[id] {
			continue
		}
		if req.Provider == "openai" && !strings.Contains(id, "gpt") && !strings.Contains(id, "codex") && !strings.HasPrefix(id, "o") {
			continue
		}
		seen[id] = true
		models = append(models, id)
	}
	sort.Strings(models)
	if len(models) == 0 {
		return nil, fmt.Errorf("%s returned no usable models", req.Provider)
	}
	return models, nil
}

func (s *Server) verifyProfileAPIKey(w http.ResponseWriter, r *http.Request, _ Principal) {
	w.Header().Set("Cache-Control", "no-store")
	req, ok := decodeProfileAPIKeyRequest(w, r)
	if !ok {
		return
	}
	models, err := s.profileAPIKeyModels(r.Context(), req)
	if err != nil {
		writeError(w, 400, err)
		return
	}
	writeJSON(w, 200, struct {
		Models []string `json:"models"`
	}{models})
}

func profileAPIKeyFiles(req profileAPIKeyRequest) (map[string][]byte, error) {
	files := map[string][]byte{}
	switch req.Application {
	case "codex":
		files["auth.json"], _ = json.Marshal(map[string]string{"OPENAI_API_KEY": req.Key})
		files["config.toml"] = []byte("model = " + fmt.Sprintf("%q", req.Model) + "\ncli_auth_credentials_store = \"file\"\n")
	case "claude":
		files["settings.json"], _ = json.Marshal(map[string]any{"model": req.Model, "env": map[string]string{"ANTHROPIC_API_KEY": req.Key}})
	case "opencode":
		files["auth.json"], _ = json.Marshal(map[string]any{req.Provider: map[string]string{"type": "api", "key": req.Key}})
		modelID := strings.TrimPrefix(req.Model, req.Provider+"/")
		files["opencode.json"], _ = json.Marshal(map[string]any{"$schema": "https://opencode.ai/config.json", "model": req.Model, "provider": map[string]any{req.Provider: map[string]any{"models": map[string]any{modelID: map[string]any{}}}}})
	default:
		return nil, fmt.Errorf("unsupported profile")
	}
	return files, nil
}

func (s *Server) saveProfileAPIKey(w http.ResponseWriter, r *http.Request, p Principal) {
	w.Header().Set("Cache-Control", "no-store")
	req, ok := decodeProfileAPIKeyRequest(w, r)
	if !ok {
		return
	}
	if !loginProfileName.MatchString(req.Name) {
		writeError(w, 400, fmt.Errorf("enter a valid profile name"))
		return
	}
	models, err := s.profileAPIKeyModels(r.Context(), req)
	if err != nil {
		writeError(w, 400, err)
		return
	}
	found := false
	for _, model := range models {
		if model == req.Model {
			found = true
			break
		}
	}
	if !found {
		writeError(w, 400, fmt.Errorf("choose a verified model"))
		return
	}
	files, err := profileAPIKeyFiles(req)
	if err != nil {
		writeError(w, 400, err)
		return
	}
	defer func() {
		for _, data := range files {
			clear(data)
		}
	}()
	profile, err := s.Store.SaveLoginProfile(r.Context(), p, req.Application, req.Name, v1.SaveLoginProfileRequest{Files: files, ReplaceExisting: req.ReplaceExisting})
	if err != nil {
		if errors.Is(err, errDuplicateLoginProfile) {
			writeError(w, 409, fmt.Errorf("these credentials are already saved in another profile"))
			return
		}
		writeError(w, 409, fmt.Errorf("could not save profile; check whether its name already exists"))
		return
	}
	status := http.StatusCreated
	if req.ReplaceExisting {
		status = http.StatusOK
	}
	writeJSON(w, status, profile)
}
