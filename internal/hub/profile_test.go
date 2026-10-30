package hub

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"
)

func TestUploadedOpenCodeProfilePreservesProviderAndNormalizesJSONC(t *testing.T) {
	m, _ := fixture(t)
	archive, err := ProfileFilesArchive("opencode", map[string][]byte{
		"opencode.jsonc": []byte(`{// portable provider
 "model":"saved/qwen", "provider":{"saved":{"npm":"@ai-sdk/openai-compatible","options":{"baseURL":"http://model/v1","apiKey":"fixture-config-key"},},},}`),
		"auth.json": []byte(`{"saved":{"type":"api","key":"fixture-login-key"}}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	p, err := m.Store.SaveProfile("Saved provider", "opencode", archive)
	if err != nil {
		t.Fatal(err)
	}
	archives, err := m.Store.Archive([]string{p.ID})
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(bytes.NewReader(archives[0]))
	files := map[string][]byte{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if h.Typeflag == tar.TypeReg {
			files[h.Name], err = io.ReadAll(tr)
			if err != nil {
				t.Fatal(err)
			}
			if h.Mode != 0600 || h.Uid != 10001 {
				t.Fatal("unsafe ownership")
			}
		}
	}
	config := files[".config/opencode/opencode.json"]
	if !json.Valid(config) || !bytes.Contains(config, []byte(`"saved/qwen"`)) || !bytes.Contains(config, []byte("fixture-config-key")) || !bytes.Contains(config, []byte("http://model/v1")) {
		t.Fatal("provider configuration lost")
	}
	if !bytes.Contains(files[".local/share/opencode/auth.json"], []byte("fixture-login-key")) {
		t.Fatal("provider login lost")
	}
	b, err := m.Create(Spec{Name: "profile box", Profiles: []string{p.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if b.Spec.ModelSource != "profile" || b.Spec.ModelURL != "" || b.Spec.Model != "" || b.Spec.Vision != nil {
		t.Fatal("custom hub defaults replaced saved provider")
	}
	if _, err = m.Create(Spec{Name: "missing profile", ModelSource: "profile"}); err == nil {
		t.Fatal("accepted profile mode without profile")
	}
	custom, err := m.Create(Spec{Name: "custom", ModelSource: "custom", Profiles: []string{p.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if custom.Spec.ModelURL != "http://model/v1" || custom.Spec.Model != "fixture" {
		t.Fatal("explicit custom defaults lost")
	}
	for _, agent := range []string{"claude", "codex"} {
		b, err = m.Create(Spec{Name: agent, Agent: agent, ModelSource: "custom", ModelURL: "http://ignored/v1", Model: "ignored"})
		if err != nil {
			t.Fatal(err)
		}
		if b.Spec.ModelSource != "" || b.Spec.ModelURL != "" || b.Spec.Model != "" || b.Spec.Vision != nil {
			t.Fatal("non-OpenCode agent inherited local model")
		}
	}
}

func TestProfileFilesRejectUnsupportedAndAmbiguousConfiguration(t *testing.T) {
	for _, files := range []map[string][]byte{
		{"../auth.json": []byte(`{}`)},
		{"unknown": []byte(`{}`)},
		{"auth.json": bytes.Repeat([]byte("x"), (512<<10)+1)},
	} {
		if _, err := ProfileFilesArchive("opencode", files); err == nil {
			t.Fatal("unsafe upload accepted")
		}
	}
	a, err := ProfileFilesArchive("opencode", map[string][]byte{"opencode.json": []byte(`{}`), "opencode.jsonc": []byte(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = sanitizeArchive(a); err == nil {
		t.Fatal("ambiguous configuration accepted")
	}
}

func TestBrowserProfileUploadNeverReturnsCredentials(t *testing.T) {
	s, cookie := authenticated(t)
	payload, _ := json.Marshal(map[string]any{"name": "Browser upload", "agent": "codex", "files": map[string][]byte{"auth.json": []byte(`{"key":"fixture-private-token"}`), "config.toml": []byte("model = \"fixture\"")}})
	if w := call(s, "POST", "/api/profiles", "", string(payload)); w.Code != 401 {
		t.Fatal("anonymous upload accepted")
	}
	w := call(s, "POST", "/api/profiles", cookie, string(payload))
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	var p Profile
	if json.Unmarshal(w.Body.Bytes(), &p) != nil || p.Agent != "codex" {
		t.Fatal("invalid profile metadata")
	}
	for _, route := range []string{"/api/profiles", "/"} {
		w = call(s, "GET", route, cookie, "")
		if strings.Contains(w.Body.String(), "fixture-private-token") {
			t.Fatal("credentials exposed")
		}
	}
	archive, err := s.Manager.Store.Archive([]string{p.ID})
	if err != nil || !bytes.Contains(archive[0], []byte("fixture-private-token")) {
		t.Fatal("private login not stored")
	}
}

func TestGitHubProfileUploadsAlongsideAgentWithoutExposingCredentials(t *testing.T) {
	s, cookie := authenticated(t)
	payload, _ := json.Marshal(map[string]any{"name": "GitHub", "agent": "github", "files": map[string][]byte{"hosts.yml": []byte("github.com:\n  user: fixture\n  oauth_token: fixture-gh-private\n")}})
	w := call(s, "POST", "/api/profiles", cookie, string(payload))
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	var p Profile
	if json.Unmarshal(w.Body.Bytes(), &p) != nil || p.Agent != "github" {
		t.Fatal("wrong profile metadata")
	}
	if bytes.Contains(w.Body.Bytes(), []byte("fixture-gh-private")) || strings.Contains(call(s, "GET", "/api/profiles", cookie, "").Body.String(), "fixture-gh-private") {
		t.Fatal("credential exposed")
	}
	b, err := s.Manager.Create(Spec{Name: "With GitHub", Agent: "claude", Profiles: []string{p.ID}})
	if err != nil {
		t.Fatal(err)
	}
	archives, err := s.Manager.Store.Archive(b.Spec.Profiles)
	if err != nil || !bytes.Contains(archives[0], []byte(".config/gh/hosts.yml")) {
		t.Fatal("GitHub profile not attached")
	}
	if _, err = ProfileFilesArchive("github", map[string][]byte{"config.yml": []byte("aliases: unsafe")}); err == nil {
		t.Fatal("unsupported GitHub settings accepted")
	}
}
