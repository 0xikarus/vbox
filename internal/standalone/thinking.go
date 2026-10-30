package standalone

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

type thinkingSetting struct {
	Mode      string   `json:"mode"`
	Supported bool     `json:"supported"`
	Efforts   []string `json:"efforts,omitempty"`
	Default   string   `json:"default,omitempty"`
	Source    string   `json:"source,omitempty"`
}

func thinkingPath(home string) string {
	return filepath.Join(home, ".config/vmbox/standalone-thinking.json")
}
func (s *Server) thinking(w http.ResponseWriter, r *http.Request) {
	runtime, ok := s.Runtime.(*BoxRuntime)
	if !ok || runtime.agent() != "opencode" {
		jsonResponse(w, 200, thinkingSetting{Mode: "default"})
		return
	}
	caps := readThinkingCapabilities(runtime.Home)
	if len(caps.Efforts) == 0 {
		jsonResponse(w, 200, thinkingSetting{Mode: "default"})
		return
	}
	path := thinkingPath(runtime.Home)
	mode := "default"
	if b, err := os.ReadFile(path); err == nil {
		var setting thinkingSetting
		if json.Unmarshal(b, &setting) == nil {
			mode = setting.Mode
			if !supportedEffort(mode, caps) {
				mode = "default"
			}
		}
	}
	if r.Method == http.MethodPost {
		var input thinkingSetting
		if !decode(w, r, &input) {
			return
		}
		if !supportedEffort(input.Mode, caps) {
			http.Error(w, "invalid thinking mode", 400)
			return
		}
		mode = input.Mode
		b, _ := json.Marshal(thinkingSetting{Mode: mode})
		if err := os.WriteFile(path+".tmp", b, 0600); err != nil {
			http.Error(w, "cannot save thinking mode", 503)
			return
		}
		if err := os.Rename(path+".tmp", path); err != nil {
			http.Error(w, "cannot save thinking mode", 503)
			return
		}
	}
	jsonResponse(w, 200, thinkingSetting{Mode: mode, Supported: true, Efforts: caps.Efforts, Default: caps.Default, Source: caps.Source})
}
func supportedEffort(mode string, caps thinkingCapabilities) bool {
	if mode == "default" {
		return true
	}
	for _, v := range caps.Efforts {
		if mode == v {
			return true
		}
	}
	return false
}
func configureThinking(entry map[string]any, caps thinkingCapabilities) {
	variants, _ := entry["variants"].(map[string]any)
	if variants == nil {
		variants = map[string]any{}
	}
	delete(variants, "thinking-on")
	delete(variants, "thinking-off")
	// Replace only the standalone-owned variants when backend metadata changes.
	for name := range variants {
		if strings.HasPrefix(name, "thinking-") {
			delete(variants, name)
		}
	}
	for _, effort := range caps.Efforts {
		variants["thinking-"+effort] = map[string]any{"reasoningEffort": effort}
	}
	if len(caps.Efforts) > 0 {
		entry["reasoning"] = true
	}
	entry["variants"] = variants
}
