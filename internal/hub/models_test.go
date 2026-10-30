package hub

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/0xikarus/vmbox-service/internal/modelcatalog"
	"github.com/0xikarus/vmbox-service/internal/procexec"
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

func TestNativeProfileCatalogUsesSelectedCredentialAndInvalidatesCache(t *testing.T) {
	for _, agent := range []string{"claude", "codex"} {
		t.Run(agent, func(t *testing.T) {
			s, cookie := authenticated(t)
			filename := "auth.json"
			if agent == "claude" {
				filename = ".credentials.json"
			}
			archive, err := ProfileFilesArchive(agent, map[string][]byte{filename: []byte(`{"token":"fixture-private"}`)})
			if err != nil {
				t.Fatal(err)
			}
			p, err := s.Manager.Store.SaveProfile("selected", agent, archive)
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			s.CatalogQuery = func(_ context.Context, application string, files map[string][]byte) ([]modelcatalog.Choice, error) {
				calls++
				if application != agent || !strings.Contains(string(files[filename]), "fixture-private") {
					t.Fatal("wrong profile used for model query")
				}
				return []modelcatalog.Choice{{ID: agent + "-live", Label: "Account model", Reasoning: true, ReasoningEfforts: []string{"low", "high"}}}, nil
			}
			route := "/api/profiles/" + p.ID + "/models"
			if w := call(s, "GET", route, "", ""); w.Code != 401 {
				t.Fatal("catalog exposed without login")
			}
			for range 2 {
				w := call(s, "GET", route, cookie, "")
				if w.Code != 200 || !strings.Contains(w.Body.String(), agent+"-live") || strings.Contains(w.Body.String(), "fixture-private") {
					t.Fatal("wrong catalog or credential disclosure")
				}
			}
			if calls != 1 {
				t.Fatal("catalog was not cached")
			}
			updated, err := ProfileFilesArchive(agent, map[string][]byte{filename: []byte(`{"token":"fixture-private-refreshed"}`)})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.Manager.Store.db.Exec(`UPDATE profiles SET archive=? WHERE id=?`, updated, p.ID); err != nil {
				t.Fatal(err)
			}
			if w := call(s, "GET", route, cookie, ""); w.Code != 200 || calls != 2 {
				t.Fatal("profile refresh kept stale catalog")
			}
		})
	}
}

func TestNativeCatalogErrorsDoNotExposeCredentials(t *testing.T) {
	s, cookie := authenticated(t)
	archive, _ := ProfileFilesArchive("codex", map[string][]byte{"auth.json": []byte(`{}`)})
	p, err := s.Manager.Store.SaveProfile("error", "codex", archive)
	if err != nil {
		t.Fatal(err)
	}
	s.CatalogQuery = func(context.Context, string, map[string][]byte) ([]modelcatalog.Choice, error) {
		return nil, fmt.Errorf("fixture-secret-error")
	}
	w := call(s, "GET", "/api/profiles/"+p.ID+"/models", cookie, "")
	if w.Code != 502 || strings.Contains(w.Body.String(), "fixture-secret-error") {
		t.Fatal("failed query exposed internal credentials")
	}
}

func TestCodexCatalogRunsIsolatedWithPrivateStdinAndOwnershipCleanup(t *testing.T) {
	runner := &procexec.FakeRunner{Results: []procexec.Result{{Stdout: []byte(`[{"id":"native-model","label":"Native","reasoningEfforts":["high"]}]`)}, {Stdout: []byte("hub-fixture\n")}, {}}}
	engine := &DockerEngine{HubID: "hub-fixture", Image: "worker:fixture", Runner: runner}
	models, err := engine.CodexModels(context.Background(), map[string][]byte{"auth.json": []byte("fixture-private-token")})
	if err != nil || len(models) != 1 || models[0].ID != "native-model" {
		t.Fatal("native result not returned")
	}
	if len(runner.Calls) != 3 {
		t.Fatal("owned query container not cleaned")
	}
	argv := strings.Join(runner.Calls[0].Argv, " ")
	for _, flag := range []string{"--rm", "--read-only", "--user 10001:10001", "--memory 256m", "--cpus 0.5", "--profile-models=codex"} {
		if !strings.Contains(argv, flag) {
			t.Fatal("unbounded or privileged model query")
		}
	}
	if strings.Contains(argv, "fixture-private-token") || !strings.Contains(runner.Calls[0].Stdin, base64.StdEncoding.EncodeToString([]byte("fixture-private-token"))) {
		t.Fatal("credential was not sent privately over stdin")
	}
	if strings.Join(runner.Calls[2].Argv[:3], " ") != "docker rm -f" {
		t.Fatal("wrong cleanup")
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
