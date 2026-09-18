package cli

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
)

func TestProfileAccountNames(t *testing.T) {
	if got := profileAccountName("github", "github.com:Alice"); got != "alice" {
		t.Fatal(got)
	}
	path := t.TempDir()
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"email":"coder@example.test"}`))
	if err := os.WriteFile(filepath.Join(path, "auth.json"), []byte(`{"tokens":{"id_token":"header.`+payload+`.signature","access_token":"never-display-this"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if got := profileAccountName("codex", path); got != "coder-example.test" {
		t.Fatal(got)
	}
	if got := profileNameWithModel("codex", path, "gpt-test"); got != "coder@example.test (gpt-test)" {
		t.Fatal(got)
	}
	if got := profileNameWithModel("codex", path, "vendor/model"); got != "coder@example.test (vendor-model)" {
		t.Fatal(got)
	}
	if err := os.WriteFile(filepath.Join(path, "auth.json"), []byte(`{"access_token":"never-display-this"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if got := profileAccountName("codex", path); got == "never-display-this" || got == "" {
		t.Fatal("unsafe fallback")
	}
}
