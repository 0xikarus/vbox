package controller

import (
	"context"
	"errors"
	"fmt"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/provider"
)

// Check the actual worker tier before accepting per-box cgroup settings.
// All slots of one shared-worker credential are served by the same supervisor.
// A non-nil box is also checked against the worker machine's limits.
func (s *Server) verifyBoxMemoryPool(ctx context.Context, accountID, providerName, credential string, box *provider.BoxLimits) error {
	if providerName != "shared-worker" {
		return fmt.Errorf("per-box RAM and swap limits require a container-isolated shared-worker pool")
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	prov, err := s.provider(ctx, accountID, providerName, credential)
	if err != nil {
		return err
	}
	if settings, ok := prov.(provider.WorkerSettingsProvider); ok {
		config, err := settings.WorkerConfig(ctx)
		if err == nil {
			if !config.Limits.PerBoxLimits {
				return fmt.Errorf("selected shared worker does not provide per-box container memory limits")
			}
			if box == nil {
				return nil
			}
			checked := *box
			checked.CPU = config.Settings.BoxDefaults.CPU
			return config.Limits.CheckBox(checked)
		}
		if !errors.Is(err, provider.ErrUnsupported) {
			return err
		}
	}
	// Workers without remote settings keep their original fixed bounds.
	if box != nil && (box.MemoryMiB < 1024 || box.MemoryMiB > 8192 || box.SwapMiB < 0 || box.SwapMiB > 4096) {
		return fmt.Errorf("this shared worker accepts 1–8 GiB RAM and 0–4 GiB swap; upgrade it to use the machine's full size")
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

func requestedBoxMemory(request v1.CreateLogicalBoxRequest) *provider.BoxLimits {
	box := provider.BoxLimits{MemoryMiB: request.MemoryGiB * 1024}
	if request.SwapGiB != nil {
		box.SwapMiB = *request.SwapGiB * 1024
	}
	return &box
}

func customBoxMemory(request v1.CreateLogicalBoxRequest) bool {
	return request.MemoryGiB != 0 || request.SwapGiB != nil
}
