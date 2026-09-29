package v1

import "testing"

func TestCreateLogicalBoxMemoryDefaultsPreserveExplicitZeroSwap(t *testing.T) {
	plain := CreateLogicalBoxRequest{Provider: "shared-worker"}
	plain.Normalize()
	if plain.MemoryGiB != 0 || plain.SwapGiB != nil {
		t.Fatal("omitted limits should keep the worker's defaults")
	}

	noSwap := int64(0)
	requested := CreateLogicalBoxRequest{Provider: "shared-worker", SwapGiB: &noSwap}
	requested.Normalize()
	if requested.MemoryGiB != 2 || requested.SwapGiB == nil || *requested.SwapGiB != 0 {
		t.Fatalf("explicit zero swap changed: %+v", requested)
	}

	memoryOnly := CreateLogicalBoxRequest{Provider: "shared-worker", MemoryGiB: 4}
	memoryOnly.Normalize()
	if memoryOnly.SwapGiB == nil || *memoryOnly.SwapGiB != 1 {
		t.Fatalf("missing swap default: %+v", memoryOnly)
	}
}
