package controller

import (
	"strings"
	"testing"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/provider"
)

func TestLogicalBoxWelcomeAlwaysContainsManagedIdentityAndDetails(t *testing.T) {
	assignment := fleetAssignment{
		Box:  v1.LogicalBox{Name: "research", Provider: "railway", VolumeName: "research-data"},
		Slot: v1.ComputeSlot{ServiceName: "fleet-slot-2", Region: "us-east4-eqdc4a"},
	}
	actual := provider.Box{
		Region:    "europe-west4-drams3a",
		Resources: provider.Resources{CPU: 2, MemoryMiB: 4096, DiskGiB: 20},
		Storage:   &provider.Storage{SizeGiB: 30},
	}
	welcome := string(logicalBoxWelcome(assignment, actual))
	for _, expected := range []string{
		"vmbox research is ready", "Provider: railway (controller)", "Region: europe-west4-drams3a",
		"2 CPU", "4096 MiB RAM", "30 GiB disk", "Volume: research-data", "Compute slot: fleet-slot-2",
		"direct OpenSSH", "vmbox hibernate research",
	} {
		if !strings.Contains(welcome, expected) {
			t.Fatalf("welcome omitted %q: %s", expected, welcome)
		}
	}
}
