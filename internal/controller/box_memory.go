package controller

import (
	"context"
	"fmt"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
)

// Check the actual worker tier before accepting per-box cgroup settings.
// All slots of one shared-worker credential are served by the same supervisor.
func (s *Server) verifyBoxMemoryPool(ctx context.Context, accountID, providerName, credential string) error {
	if providerName != "shared-worker" {
		return fmt.Errorf("per-box RAM and swap limits require a container-isolated shared-worker pool")
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	prov, err := s.provider(ctx, accountID, providerName, credential)
	if err != nil {
		return err
	}
	boxes, err := prov.List(ctx)
	if err != nil {
		return err
	}
	for _, box := range boxes {
		if box.Connection.Metadata["isolationTier"] == "container" {
			return nil
		}
	}
	return fmt.Errorf("selected shared worker does not provide per-box container memory limits")
}

func customBoxMemory(request v1.CreateLogicalBoxRequest) bool {
	return request.MemoryGiB != 0 || request.SwapGiB != nil
}
