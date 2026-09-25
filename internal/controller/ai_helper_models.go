package controller

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/loginprofile"
)

func (s *Server) aiHelperProfile(ctx context.Context, p Principal, name string) (string, string, error) {
	profile, err := s.Store.LoadLoginProfile(ctx, p, "opencode", name)
	if err != nil {
		return "", "", err
	}
	defer func() {
		for _, data := range profile.Files {
			clear(data)
		}
	}()
	key, err := openRouterProfileCredential(profile)
	if err != nil {
		return "", "", err
	}
	model := loginprofile.Model("opencode", profile.Files)
	if !strings.HasPrefix(model, "openrouter/") || model == "openrouter/" {
		model = defaultAIHelperModel
	}
	if model != defaultAIHelperModel {
		model = strings.TrimPrefix(model, "openrouter/")
	}
	return key, model, nil
}

func firstAIHelperProfile(profiles []v1.LoginProfile) string {
	for _, item := range profiles {
		if item.Application == "opencode" && strings.HasPrefix(item.Model, "openrouter/") {
			return item.Name
		}
	}
	return ""
}

// Import only returns credentials to the authenticated owner, for a deliberate
// copy into the encrypted writing-helper setting. Both response and catalog are
// uncached so a removed or rotated profile is never shown from browser cache.
func (s *Server) getAIHelperOpenCodeProfile(w http.ResponseWriter, r *http.Request, p Principal) {
	w.Header().Set("Cache-Control", "no-store")
	key, model, err := s.aiHelperProfile(r.Context(), p, r.PathValue("name"))
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, fmt.Errorf("OpenCode profile not found"))
		return
	}
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, fmt.Errorf("this OpenCode profile has no OpenRouter key and model"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"key": key, "model": model})
}

func (s *Server) getAIHelperModels(w http.ResponseWriter, r *http.Request, p Principal) {
	w.Header().Set("Cache-Control", "no-store")
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	var key string
	if name := strings.TrimSpace(r.URL.Query().Get("profile")); name != "" {
		var err error
		key, _, err = s.aiHelperProfile(ctx, p, name)
		if err != nil {
			writeError(w, http.StatusUnprocessableEntity, fmt.Errorf("could not use this OpenCode profile"))
			return
		}
	} else {
		setting, err := s.Store.AIHelperOpenRouter(ctx, p)
		if err == nil {
			key = setting.Key
		} else if errors.Is(err, sql.ErrNoRows) {
			profiles, listErr := s.Store.ListLoginProfiles(ctx, p)
			if listErr != nil {
				writeError(w, http.StatusInternalServerError, fmt.Errorf("could not list OpenCode profiles"))
				return
			}
			name := firstAIHelperProfile(profiles)
			if name != "" {
				key, _, err = s.aiHelperProfile(ctx, p, name)
			}
		} else {
			writeError(w, http.StatusInternalServerError, fmt.Errorf("could not read AI helper setting"))
			return
		}
		if key == "" || err != nil {
			writeError(w, http.StatusUnprocessableEntity, fmt.Errorf("save an OpenRouter key or OpenCode profile to browse models"))
			return
		}
	}
	client := s.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	models, err := queryOpenCodeModelCatalogFiltered(ctx, client, "openrouter", key, "https://openrouter.ai/api/v1/models", false)
	key = ""
	if err != nil {
		writeError(w, http.StatusBadGateway, fmt.Errorf("could not load OpenRouter models; enter an exact model ID instead"))
		return
	}
	writeJSON(w, http.StatusOK, profileModelCatalog{Source: "OpenRouter live catalog", Models: models})
}
