package hub

import (
	"archive/tar"
	"bytes"
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

func (s *Server) localModels(w http.ResponseWriter, r *http.Request) {
	endpoint := strings.TrimRight(r.URL.Query().Get("endpoint"), "/")
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "http" && u.Scheme != "https") {
		http.Error(w, "choose an HTTP(S) model endpoint without credentials", 400)
		return
	}
	request, err := http.NewRequestWithContext(r.Context(), "GET", endpoint+"/models", nil)
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
			ID string `json:"id"`
		} `json:"data"`
	}
	if response.StatusCode != 200 || json.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(&catalog) != nil {
		http.Error(w, "invalid model catalog", 502)
		return
	}
	models := []string{}
	for _, entry := range catalog.Data {
		if entry.ID != "" && len(entry.ID) <= 200 && strings.IndexFunc(entry.ID, unicode.IsControl) < 0 {
			models = append(models, entry.ID)
		}
	}
	output(w, 200, map[string]any{"source": fmt.Sprintf("Custom endpoint · %d models", len(models)), "models": models})
}
