package standalone

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// ModelCapabilities describes only values explicitly advertised by the backend.
type ModelCapabilities struct {
	Efforts []string `json:"efforts"`
	Default string   `json:"default,omitempty"`
	Source  string   `json:"source,omitempty"`
	Context int      `json:"context,omitempty"`
}

type thinkingCapabilities = ModelCapabilities

// DiscoverModelCapabilities reads metadata and the selected model's template; it never generates a completion.
func DiscoverModelCapabilities(ctx context.Context, endpoint, model string) ModelCapabilities {
	return discoverThinking(ctx, endpoint, model)
}

func capabilitiesPath(home string) string {
	return filepath.Join(home, ".config/vmbox/model-capabilities.json")
}
func readThinkingCapabilities(home string) thinkingCapabilities {
	var c thinkingCapabilities
	b, _ := os.ReadFile(capabilitiesPath(home))
	_ = json.Unmarshal(b, &c)
	return c
}

var effortToken = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)

// Read explicit declarations only. This does not execute the model's template,
// infer choices from its name, or generate inference requests to probe values.
var effortGuard = regexp.MustCompile(`(?:resolved_)?reasoning_effort\s+not\s+in\s*[\(\[]([^\)\]]+)[\)\]]`)
var quotedEffort = regexp.MustCompile(`['"]([a-z][a-z0-9_-]{0,31})['"]`)
var effortDefault = regexp.MustCompile(`reasoning_effort\s*\|\s*default\(\s*['"]([a-z][a-z0-9_-]{0,31})['"]`)

func templateThinking(template string) thinkingCapabilities {
	c := thinkingCapabilities{}
	if m := effortGuard.FindStringSubmatch(template); len(m) > 1 {
		rest := quotedEffort.ReplaceAllString(m[1], "")
		rest = strings.Trim(rest, " ,\n\r\t")
		if rest != "" {
			return c
		}
		for _, v := range quotedEffort.FindAllStringSubmatch(m[1], -1) {
			c.Efforts = append(c.Efforts, v[1])
		}
	}
	if len(c.Efforts) > 16 {
		return thinkingCapabilities{}
	}
	if m := effortDefault.FindStringSubmatch(template); len(m) > 1 {
		for _, v := range c.Efforts {
			if m[1] == v {
				c.Default = v
			}
		}
	}
	return c
}
func discoverThinking(ctx context.Context, endpoint, model string) thinkingCapabilities {
	ctx, cancel := context.WithTimeout(ctx, 5e9)
	defer cancel()
	get := func(target string, out any) bool {
		r, err := http.NewRequestWithContext(ctx, "GET", target, nil)
		if err != nil {
			return false
		}
		response, err := http.DefaultClient.Do(r)
		if err != nil {
			return false
		}
		defer response.Body.Close()
		return response.StatusCode == 200 && json.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(out) == nil
	}
	var models struct {
		Data []struct {
			ID           string   `json:"id"`
			Efforts      []string `json:"supported_reasoning_efforts"`
			Capabilities struct {
				Reasoning json.RawMessage `json:"reasoning"`
			}
		}
	}
	if get(strings.TrimRight(endpoint, "/")+"/models", &models) {
		for _, m := range models.Data {
			if m.ID != model {
				continue
			}
			values := m.Efforts
			if len(values) == 0 {
				var reasoning struct {
					Efforts []string `json:"effort"`
				}
				_ = json.Unmarshal(m.Capabilities.Reasoning, &reasoning)
				values = reasoning.Efforts
			}
			valid := len(values) > 0 && len(values) <= 16
			for _, v := range values {
				valid = valid && effortToken.MatchString(v)
			}
			if valid {
				return thinkingCapabilities{Efforts: values, Source: "model metadata"}
			}
		}
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return thinkingCapabilities{}
	}
	u.Path = strings.TrimSuffix(strings.TrimRight(u.Path, "/"), "/v1") + "/props"
	q := u.Query()
	q.Set("model", model)
	u.RawQuery = q.Encode()
	var props struct {
		Model      string `json:"model_alias"`
		Template   string `json:"chat_template"`
		Generation struct {
			Context int `json:"n_ctx"`
		} `json:"default_generation_settings"`
	}
	if get(u.String(), &props) && (props.Model == model || props.Model == "") {
		c := templateThinking(props.Template)
		if props.Generation.Context >= 1024 && props.Generation.Context <= 16<<20 {
			c.Context = props.Generation.Context
		}
		if len(c.Efforts) > 0 {
			c.Source = "model chat template"
		}
		return c
	}
	return thinkingCapabilities{}
}
func saveThinkingCapabilities(home string, c thinkingCapabilities) error {
	path := capabilitiesPath(home)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	b, _ := json.Marshal(c)
	return os.WriteFile(path, b, 0600)
}
