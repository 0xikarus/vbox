package cli

import (
	"testing"

	"github.com/0xikarus/vmbox-service/internal/config"
)

func TestRailwayTokenSelectionIsExclusive(t *testing.T) {
	token, environment, err := railwayToken(map[string]string{"RAILWAY_TOKEN": "project-token"})
	if err != nil || token != "project-token" || environment != "RAILWAY_TOKEN" {
		t.Fatalf("project token=%q environment=%q err=%v", token, environment, err)
	}
	if _, _, err := railwayToken(map[string]string{"RAILWAY_TOKEN": "project", "RAILWAY_API_TOKEN": "account"}); err == nil {
		t.Fatal("simultaneous Railway token types were accepted")
	}
}

func TestRailwayLocalCLIAuthRequiresExplicitContextOptIn(t *testing.T) {
	app := New()
	app.Environ = map[string]string{}
	base := config.Context{Provider: "railway", Project: "project", Environment: "environment"}
	if _, err := app.provider(base); err == nil {
		t.Fatal("missing Railway token was accepted without local auth opt-in")
	}
	base.RailwayCLIAuth = true
	if _, err := app.provider(base); err != nil {
		t.Fatalf("local Railway CLI auth opt-in rejected: %v", err)
	}
}
