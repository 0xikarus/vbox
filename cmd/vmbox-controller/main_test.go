package main

import (
	"encoding/json"
	"reflect"
	"testing"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/controller"
)

func TestRailwayControllerCredentialSelectsProjectToken(t *testing.T) {
	provider, err := providerForCredential("railway", controller.DecryptedProviderCredential{
		Secret:             json.RawMessage(`{"token":"project-secret"}`),
		ProviderCredential: v1.ProviderCredential{Config: json.RawMessage(`{"projectId":"project","environmentId":"environment","tokenEnvironment":"RAILWAY_TOKEN"}`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	value := reflect.ValueOf(provider).Elem().FieldByName("cfg").FieldByName("TokenEnvironment").String()
	if value != "RAILWAY_TOKEN" {
		t.Fatalf("token environment = %q", value)
	}
	if _, err := providerForCredential("railway", controller.DecryptedProviderCredential{
		Secret:             json.RawMessage(`{"token":"secret"}`),
		ProviderCredential: v1.ProviderCredential{Config: json.RawMessage(`{"tokenEnvironment":"BOTH"}`)},
	}); err == nil {
		t.Fatal("invalid Railway token environment was accepted")
	}
}
