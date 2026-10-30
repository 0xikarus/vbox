package hub

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestProfileModelsReturnsOnlyModelMetadata(t *testing.T) {
	s, cookie := authenticated(t)
	a, err := ProfileFilesArchive("opencode", map[string][]byte{"opencode.json": []byte(`{"model":"saved/one","provider":{"saved":{"options":{"apiKey":"fixture-secret"},"models":{"one":{},"two":{}}}}}`)})
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.Manager.Store.SaveProfile("models", "opencode", a)
	if err != nil {
		t.Fatal(err)
	}
	for _, route := range []string{"/api/profiles", "/api/profiles/" + p.ID + "/models"} {
		w := call(s, "GET", route, cookie, "")
		if w.Code != 200 || strings.Contains(w.Body.String(), "fixture-secret") || !strings.Contains(w.Body.String(), "saved/one") {
			t.Fatal("invalid model metadata")
		}
	}
	b, err := s.Manager.Create(Spec{Name: "chosen model", Agent: "codex", AgentModel: "gpt-6-astra", AgentReasoningEffort: "high"})
	if err != nil || b.Spec.AgentModel != "gpt-6-astra" || b.Spec.AgentReasoningEffort != "high" {
		t.Fatal("selection not retained")
	}
	if _, err = s.Manager.Create(Spec{Name: "invalid", Agent: "claude", AgentModel: "haiku", AgentReasoningEffort: "high"}); err == nil {
		t.Fatal("invalid reasoning accepted")
	}
}

func TestCustomModelCatalogIsAuthenticatedAndBounded(t *testing.T) {
	s, cookie := authenticated(t)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Error("wrong catalog path")
		}
		json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"id": "local-fixture"}}})
	}))
	defer provider.Close()
	route := "/api/models?endpoint=" + url.QueryEscape(provider.URL+"/v1")
	if w := call(s, "GET", route, "", ""); w.Code != 401 {
		t.Fatal("anonymous catalog query accepted")
	}
	if w := call(s, "GET", route, cookie, ""); w.Code != 200 || !strings.Contains(w.Body.String(), "local-fixture") {
		t.Fatal("custom model catalog unavailable")
	}
}
