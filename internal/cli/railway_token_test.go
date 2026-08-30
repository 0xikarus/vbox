package cli

import "testing"

func TestRailwayTokenSelectionIsExclusive(t *testing.T) {
	token, environment, err := railwayToken(map[string]string{"RAILWAY_TOKEN": "project-token"})
	if err != nil || token != "project-token" || environment != "RAILWAY_TOKEN" {
		t.Fatalf("project token=%q environment=%q err=%v", token, environment, err)
	}
	if _, _, err := railwayToken(map[string]string{"RAILWAY_TOKEN": "project", "RAILWAY_API_TOKEN": "account"}); err == nil {
		t.Fatal("simultaneous Railway token types were accepted")
	}
}
