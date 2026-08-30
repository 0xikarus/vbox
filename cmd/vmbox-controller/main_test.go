package main

import (
	"encoding/json"
	"reflect"
	"testing"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/controller"
)

func TestSevallaControllerCredentialPlumbsRegistryCredential(t *testing.T) {
	provider, err := providerForCredential("sevalla", controller.DecryptedProviderCredential{
		Secret:             json.RawMessage(`{"token":"secret"}`),
		ProviderCredential: v1.ProviderCredential{Config: json.RawMessage(`{"projectId":"project","dockerRegistryCredentialId":"registry-credential"}`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	value := reflect.ValueOf(provider).Elem().FieldByName("cfg").FieldByName("DockerRegistryCredentialID").String()
	if value != "registry-credential" {
		t.Fatalf("docker registry credential = %q", value)
	}
}
