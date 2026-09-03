package controller

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/provider"
)

func (s *Server) ReconcileFleetNow(ctx context.Context) error {
	configs, err := s.Store.ListFleetConfigs(ctx)
	if err != nil {
		return err
	}
	var failures []error
	for _, value := range configs {
		if err := s.reconcileFleet(ctx, value.AccountID, value.Config); err != nil {
			failures = append(failures, fmt.Errorf("fleet %s/%s: %w", value.AccountID, value.Config.Provider, err))
		}
	}
	return errors.Join(failures...)
}

func (s *Server) reconcileFleet(ctx context.Context, accountID string, config v1.FleetConfig) error {
	if err := config.Validate(); err != nil {
		return err
	}
	if config.Provider == "railway" && s.DefaultImage != "" && !immutableImage(config.Provider, s.DefaultImage) {
		return fmt.Errorf("Railway compute fleet image must be pinned by sha256 digest")
	}
	prov, err := s.provider(ctx, accountID, config.Provider, config.ProviderCredential)
	if err != nil {
		return err
	}
	status, err := s.Store.FleetStatus(ctx, accountID, config.Provider, config.ProviderCredential)
	if err != nil {
		return err
	}
	if status.ActualSlots < config.ComputeBoxSlots {
		maxOrdinal := 0
		for _, slot := range status.Slots {
			if slot.Ordinal > maxOrdinal {
				maxOrdinal = slot.Ordinal
			}
		}
		for status.ActualSlots < config.ComputeBoxSlots {
			maxOrdinal++
			slot := v1.ComputeSlot{
				Provider: config.Provider, ProviderCredential: config.ProviderCredential,
				Ordinal: maxOrdinal, State: v1.FleetSlotStarting, Health: "starting",
				Image: s.DefaultImage,
			}
			slot, err = s.Store.UpsertComputeSlot(ctx, accountID, slot)
			if err != nil {
				return err
			}
			name := fleetSlotName(accountID, maxOrdinal)
			box, createErr := prov.Create(ctx, provider.CreateRequest{
				Name: name, Image: s.DefaultImage,
				Resources: provider.Resources{CPU: 2, MemoryMiB: 4096},
				Owner:     provider.Owner{AccountID: accountID, BoxID: "compute-slot:" + slot.ID},
				Detached:  true,
			})
			if createErr != nil {
				_ = s.Store.SetComputeSlotState(ctx, accountID, slot.ID, v1.FleetSlotUnhealthy, createErr.Error())
				return createErr
			}
			slot.ServiceID = box.ID
			slot.ServiceName = box.Name
			slot.Region = box.Region
			slot.Image = box.Image
			slot.State = v1.FleetSlotFree
			slot.Health = "healthy"
			if connection, connectionErr := prov.Connection(ctx, box.ID); connectionErr == nil {
				slot.DeploymentInstanceID = connection.Metadata["deploymentInstanceId"]
			}
			if _, err := s.Store.UpsertComputeSlot(ctx, accountID, slot); err != nil {
				return err
			}
			status.ActualSlots++
		}
		return nil
	}
	if status.ActualSlots <= config.ComputeBoxSlots {
		return nil
	}
	excess := status.ActualSlots - config.ComputeBoxSlots
	slots := append([]v1.ComputeSlot(nil), status.Slots...)
	sort.SliceStable(slots, func(i, j int) bool {
		leftFree := removableSlotState(slots[i].State)
		rightFree := removableSlotState(slots[j].State)
		if leftFree != rightFree {
			return leftFree
		}
		return slots[i].Ordinal > slots[j].Ordinal
	})
	for _, slot := range slots {
		if excess == 0 {
			break
		}
		if !removableSlotState(slot.State) {
			if slot.State != v1.FleetSlotDraining {
				if err := s.Store.SetComputeSlotState(ctx, accountID, slot.ID, v1.FleetSlotDraining, "waiting for logical box release after fleet scale-down"); err != nil {
					return err
				}
			}
			excess--
			continue
		}
		if slot.ServiceID != "" {
			box, inspectErr := prov.Inspect(ctx, slot.ServiceID)
			if inspectErr != nil && !errors.Is(inspectErr, provider.ErrNotFound) {
				return inspectErr
			}
			if inspectErr == nil {
				attached := box.Storage
				if inspector, ok := prov.(provider.AttachedStorageProvider); ok {
					attached, err = inspector.AttachedStorage(ctx, box.ID)
					if err != nil {
						return fmt.Errorf("verify slot %d storage before scale-down: %w", slot.Ordinal, err)
					}
				}
				if attached != nil && (attached.ID != "" || attached.Name != "") {
					return fmt.Errorf("refusing to scale down slot %d while storage %s is attached", slot.Ordinal, attached.ID)
				}
				if err := prov.Delete(ctx, box.ID, box.Owner); err != nil {
					return err
				}
			}
		}
		if err := s.Store.DeleteComputeSlotRecord(ctx, accountID, slot.ID); err != nil {
			return err
		}
		excess--
	}
	return nil
}

func removableSlotState(state v1.FleetSlotState) bool {
	return state == v1.FleetSlotFree || state == v1.FleetSlotStopped || state == v1.FleetSlotUnhealthy || state == v1.FleetSlotStarting
}

func fleetSlotName(accountID string, ordinal int) string {
	prefix := strings.ReplaceAll(accountID, "-", "")
	if len(prefix) > 10 {
		prefix = prefix[:10]
	}
	if prefix == "" {
		prefix = "default"
	}
	return fmt.Sprintf("slot-%s-%02d", prefix, ordinal)
}
