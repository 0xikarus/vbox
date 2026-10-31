package standalone

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestThinkingDiscoveryUsesTemplateAllowedValues(t *testing.T) {
	template := `{%- set resolved_reasoning_effort = reasoning_effort|default('xhigh') %}{%- if resolved_reasoning_effort not in ('xhigh', 'medium', 'low') %}{{ raise_exception('invalid') }}{% endif %}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			json.NewEncoder(w).Encode(map[string]any{"data": []any{}})
			return
		}
		if r.URL.Path != "/props" || r.URL.Query().Get("model") != "arbitrary-model" {
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"model_alias": "arbitrary-model", "chat_template": template, "default_generation_settings": map[string]int{"n_ctx": 65536}})
	}))
	defer server.Close()
	c := discoverThinking(context.Background(), server.URL+"/v1", "arbitrary-model")
	if len(c.Efforts) != 3 || c.Efforts[0] != "xhigh" || c.Default != "xhigh" || c.Context != 65536 {
		t.Fatalf("wrong advertised values: %+v", c)
	}
	if supportedEffort("high", c) || supportedEffort("off", c) {
		t.Fatal("invented an unsupported effort")
	}
	if c := templateThinking(`{% if reasoning_effort %}thought{% endif %}`); len(c.Efforts) > 0 {
		t.Fatal("guessed values without a declaration")
	}
	if c := templateThinking(`{% if reasoning_effort not in (custom(), 'high') %}`); len(c.Efforts) > 0 {
		t.Fatal("interpreted dynamic template expressions")
	}
}

func TestThinkingDiscoveryAcceptsBooleanReasoningFlag(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(`{"data":[{"id":"fixture","capabilities":{"reasoning":true},"supported_reasoning_efforts":["low","xhigh"]}]}`))
	}))
	defer server.Close()
	caps := discoverThinking(context.Background(), server.URL+"/v1", "fixture")
	if len(caps.Efforts) != 2 || caps.Efforts[1] != "xhigh" {
		t.Fatal("boolean reasoning metadata hid declared effort choices")
	}
}
