package controller

import (
	"encoding/json"
	"slices"
	"testing"
)

func TestProviderTypesKeepSecretsOutOfPublicConfig(t *testing.T) {
	for _, typ := range providerTypes {
		for _, field := range typ.Fields {
			if _, public := providerSchemas[typ.Name][field.Name]; public == field.Secret {
				t.Errorf("%s.%s: secret=%v but public allowlist=%v", typ.Name, field.Name, field.Secret, public)
			}
		}
	}
	raw := json.RawMessage(`{"endpoint":"https://worker.example","token":"never public"}`)
	if err := validateProviderConfig("shared-worker", raw); err == nil {
		t.Fatal("secret field accepted in shared-worker config")
	}
	if got := string(publicProviderConfig("shared-worker", raw)); got != `{"endpoint":"https://worker.example"}` {
		t.Fatalf("public config = %s", got)
	}
}

func TestProviderTypesDeriveRequiredFields(t *testing.T) {
	if !slices.Equal(providerRequired["railway"], []string{"projectId", "environmentId"}) || !slices.Equal(providerRequired["shared-worker"], []string{"endpoint"}) {
		t.Fatalf("required = %v", providerRequired)
	}
	if err := validateProviderConfig("shared-worker", json.RawMessage(`{}`)); err == nil {
		t.Fatal("shared-worker without endpoint accepted")
	}
	if err := validateProviderConfig("railway", json.RawMessage(`{"projectId":"p","environmentId":"e","tokenEnvironment":"RAILWAY_TOKEN"}`)); err != nil {
		t.Fatal(err)
	}
}
