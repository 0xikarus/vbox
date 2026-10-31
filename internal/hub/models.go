package hub

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/0xikarus/vmbox-service/internal/loginprofile"
	"github.com/0xikarus/vmbox-service/internal/modelcatalog"
	"github.com/0xikarus/vmbox-service/internal/standalone"
)

func profileFiles(archive []byte) map[string][]byte {
	files := map[string][]byte{}
	reader := tar.NewReader(bytes.NewReader(archive))
	for {
		h, err := reader.Next()
		if err != nil {
			break
		}
		if h.Typeflag == tar.TypeReg {
			files[path.Base(h.Name)], _ = io.ReadAll(io.LimitReader(reader, 512<<10))
		}
	}
	return files
}
func clearProfileFiles(files map[string][]byte) {
	for _, data := range files {
		clear(data)
	}
}

func profileModel(agent string, archive []byte) string {
	files := profileFiles(archive)
	defer clearProfileFiles(files)
	return loginprofile.Model(agent, files)
}

func (s *Server) profileModels(w http.ResponseWriter, r *http.Request) {
	profiles, err := s.Manager.Store.Profiles()
	if err != nil {
		http.Error(w, "profiles unavailable", 503)
		return
	}
	var selected *Profile
	for _, p := range profiles {
		if p.ID == r.PathValue("id") {
			selected = &p
			break
		}
	}
	if selected == nil || selected.Agent == "github" {
		http.NotFound(w, r)
		return
	}
	archives, err := s.Manager.Store.Archive([]string{selected.ID})
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer clear(archives[0])
	if selected.Agent == "codex" || selected.Agent == "claude" {
		digest := sha256.Sum256(archives[0])
		s.catalogMu.Lock()
		cached, ok := s.catalogCache[selected.ID]
		s.catalogMu.Unlock()
		if ok && cached.Digest == digest && time.Now().Before(cached.Expires) {
			output(w, 200, cached.Catalog)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
		defer cancel()
		select {
		case s.catalogSlot <- struct{}{}:
			defer func() { <-s.catalogSlot }()
		case <-ctx.Done():
			http.Error(w, "model catalog timed out; retry shortly", 503)
			return
		}
		s.catalogMu.Lock()
		cached, ok = s.catalogCache[selected.ID]
		s.catalogMu.Unlock()
		if ok && cached.Digest == digest && time.Now().Before(cached.Expires) {
			output(w, 200, cached.Catalog)
			return
		}
		files := profileFiles(archives[0])
		defer clearProfileFiles(files)
		models, err := s.CatalogQuery(ctx, selected.Agent, files)
		if err != nil || len(models) == 0 {
			http.Error(w, "could not load live "+selected.Agent+" models; check or refresh this profile and retry", 502)
			return
		}
		result := liveCatalog{Source: map[string]string{"codex": "Codex account catalog", "claude": "Anthropic live catalog · account access checked at launch"}[selected.Agent], Models: models}
		s.catalogMu.Lock()
		s.catalogCache[selected.ID] = cachedCatalog{Digest: digest, Expires: time.Now().Add(time.Minute), Catalog: result}
		s.catalogMu.Unlock()
		output(w, 200, result)
		return
	}
	files := profileFiles(archives[0])
	defer clearProfileFiles(files)
	models := []string{}
	if selected.Model != "" {
		models = append(models, selected.Model)
	}
	if selected.Agent == "opencode" {
		var config struct {
			Provider map[string]struct {
				Models map[string]json.RawMessage `json:"models"`
			} `json:"provider"`
		}
		if json.Unmarshal(files["opencode.json"], &config) == nil {
			for provider, entry := range config.Provider {
				for id := range entry.Models {
					models = append(models, provider+"/"+id)
				}
			}
		}
	}
	sort.Strings(models)
	output(w, 200, map[string]any{"source": "Saved profile models · exact IDs accepted; account access checked at launch", "models": models})
}

type liveCatalog struct {
	Source string                `json:"source"`
	Models []modelcatalog.Choice `json:"models"`
}
type cachedCatalog struct {
	Digest  [32]byte
	Expires time.Time
	Catalog liveCatalog
}

func (s *Server) queryLiveModels(ctx context.Context, agent string, files map[string][]byte) ([]modelcatalog.Choice, error) {
	if agent == "claude" {
		return modelcatalog.QueryClaude(ctx, &http.Client{Timeout: 20 * time.Second}, files, "https://api.anthropic.com/v1/models?limit=1000")
	}
	if engine, ok := s.Manager.Engine.(interface {
		CodexModels(context.Context, map[string][]byte) ([]modelcatalog.Choice, error)
	}); ok {
		return engine.CodexModels(ctx, files)
	}
	return nil, fmt.Errorf("native Codex catalog is unavailable")
}

func (s *Server) localModels(w http.ResponseWriter, r *http.Request) {
	endpoint := strings.TrimRight(r.URL.Query().Get("endpoint"), "/")
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "http" && u.Scheme != "https") {
		http.Error(w, "choose an HTTP(S) model endpoint without credentials", 400)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, "GET", endpoint+"/models", nil)
	if err != nil {
		http.Error(w, "invalid model endpoint", 400)
		return
	}
	client := &http.Client{Timeout: 5 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		http.Error(w, "model endpoint unavailable; exact model IDs remain selectable", 502)
		return
	}
	defer response.Body.Close()
	var catalog struct {
		Data []struct {
			ID      string   `json:"id"`
			Name    string   `json:"name"`
			OwnedBy string   `json:"owned_by"`
			Efforts []string `json:"supported_reasoning_efforts"`
			Context int      `json:"context_length"`
			Status  struct {
				Value string `json:"value"`
			} `json:"status"`
			Capabilities struct {
				Reasoning json.RawMessage `json:"reasoning"`
			} `json:"capabilities"`
		} `json:"data"`
	}
	if response.StatusCode != 200 || json.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(&catalog) != nil {
		http.Error(w, "invalid model catalog", 502)
		return
	}
	models := []modelcatalog.Choice{}
	seen := map[string]bool{}
	for _, entry := range catalog.Data {
		if entry.ID == "" || len(entry.ID) > 200 || strings.IndexFunc(entry.ID, unicode.IsControl) >= 0 || seen[entry.ID] {
			continue
		}
		seen[entry.ID] = true
		label := entry.Name
		if label == "" {
			label = entry.ID
		}
		efforts := entry.Efforts
		if len(efforts) == 0 {
			var reasoning struct {
				Efforts []string `json:"effort"`
			}
			_ = json.Unmarshal(entry.Capabilities.Reasoning, &reasoning)
			efforts = reasoning.Efforts
		}
		if !validModelEfforts(efforts) {
			efforts = nil
		}
		choice := modelcatalog.Choice{ID: entry.ID, Label: label, Context: entry.Context, Reasoning: len(efforts) > 0, ReasoningEfforts: efforts}
		// llama-swap props can load/evict models: only inspect one already loaded.
		// A direct single-model server already has its model loaded.
		if len(efforts) == 0 && (entry.Status.Value == "loaded" || (len(catalog.Data) == 1 && entry.OwnedBy != "llama-swap")) {
			caps := standalone.DiscoverModelCapabilities(ctx, endpoint, entry.ID)
			choice.ReasoningEfforts = caps.Efforts
			choice.Reasoning = len(caps.Efforts) > 0
			if caps.Context > 0 {
				choice.Context = caps.Context
			}
		}
		models = append(models, choice)
	}
	output(w, 200, map[string]any{"source": fmt.Sprintf("Custom endpoint · %d models", len(models)), "models": models})
}

func validModelEfforts(values []string) bool {
	if len(values) == 0 || len(values) > 16 {
		return false
	}
	for _, v := range values {
		if v == "" || len(v) > 32 || v[0] < 'a' || v[0] > 'z' {
			return false
		}
		for _, r := range v {
			if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
				return false
			}
		}
	}
	return true
}
