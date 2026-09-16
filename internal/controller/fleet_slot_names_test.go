package controller

import (
	"testing"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/provider"
)

func TestSharedFleetSlotNamesAreUniqueAcrossHosts(t *testing.T) {
	first := v1.ComputeSlot{ID: "11111111-1111-4111-8111-111111111111", Provider: "shared-worker", ProviderCredential: "shared-01", Ordinal: 1}
	second := v1.ComputeSlot{ID: "22222222-2222-4222-8222-222222222222", Provider: "shared-worker", ProviderCredential: "shared-02", Ordinal: 1}
	firstName, secondName := fleetProviderSlotName("account", first), fleetProviderSlotName("account", second)
	if firstName == secondName {
		t.Fatal("different hosts collided on the same ordinal")
	}
	for _, name := range []string{firstName, secondName} {
		if err := provider.ValidateName(name); err != nil {
			t.Fatal(err)
		}
	}
	if repeated := fleetProviderSlotName("account", first); repeated != firstName {
		t.Fatal("slot retry changed identity")
	}
}

func TestFleetSlotNamesPreserveExistingIdentities(t *testing.T) {
	legacy := v1.ComputeSlot{ID: "new-id", Provider: "shared-worker", ServiceID: "slot-account-01", Ordinal: 1}
	if name := fleetProviderSlotName("account", legacy); name != legacy.ServiceID {
		t.Fatal("existing shared slot renamed during repair")
	}
	dedicated := v1.ComputeSlot{Provider: "railway", Ordinal: 1}
	if name := fleetProviderSlotName("account", dedicated); name != fleetSlotName("account", 1) {
		t.Fatal("dedicated worker naming changed")
	}
}
