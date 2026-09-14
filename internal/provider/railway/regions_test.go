package railway

import (
	"context"
	"github.com/0xikarus/vmbox-service/internal/procexec"
	"strings"
	"testing"
)

func TestRegionsFromAPI(t *testing.T) {
	runner := &procexec.FakeRunner{Results: []procexec.Result{{Stdout: []byte(`{"data":{"regions":[{"name":"new-region","location":"New location","country":"NL"}]}}`)}}}
	p := newTestProvider(Config{ProjectID: "project"}, runner)
	regions, err := p.Regions(context.Background())
	if err != nil || len(regions) != 1 || regions[0].ID != "new-region" || regions[0].Name != "New location, NL" {
		t.Fatalf("%+v %v", regions, err)
	}
	if !strings.Contains(strings.Join(runner.Calls[0].Argv, " "), `"projectId":"project"`) {
		t.Fatal("project scope missing")
	}
}

func TestRegionsRejectsUnavailableCatalogue(t *testing.T) {
	for _, value := range []string{`{"errors":[{"message":"Not Authorized"}]}`, `{"data":{"regions":[]}}`, `invalid`, `{"data":{"regions":[{"name":""}]}}`} {
		runner := &procexec.FakeRunner{Results: []procexec.Result{{Stdout: []byte(value)}}}
		if _, err := newTestProvider(Config{}, runner).Regions(context.Background()); err == nil {
			t.Fatalf("accepted %s", value)
		}
	}
}
