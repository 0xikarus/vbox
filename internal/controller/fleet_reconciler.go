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
	if err := s.refreshFleetSlotImages(ctx, accountID, &status, prov); err != nil {
		return fmt.Errorf("refresh observed slot images: %w", err)
	}
	for i := range status.Slots {
		if !repairableSlotState(status.Slots[i].State) {
			continue
		}
		repaired, repairErr := s.ensureFleetSlot(ctx, accountID, status.Slots[i], prov)
		if repairErr != nil {
			return fmt.Errorf("repair compute slot %d: %w", status.Slots[i].Ordinal, repairErr)
		}
		status.Slots[i] = repaired
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
			if _, err := s.ensureFleetSlot(ctx, accountID, slot, prov); err != nil {
				return fmt.Errorf("create compute slot %d: %w", slot.Ordinal, err)
			}
			status.ActualSlots++
		}
		return nil
	}
	if status.ActualSlots <= config.ComputeBoxSlots {
		return nil
	}
	excess := remainingScaleDown(status.ActualSlots, config.ComputeBoxSlots, status.Slots)
	if excess == 0 {
		return nil
	}
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
		if slot.State == v1.FleetSlotDraining {
			continue
		}
		if !removableSlotState(slot.State) {
			if err := s.Store.SetComputeSlotState(ctx, accountID, slot.ID, v1.FleetSlotDraining, "waiting for logical box release after fleet scale-down"); err != nil {
				return err
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

func (s *Server) refreshFleetSlotImages(ctx context.Context, accountID string, status *v1.FleetStatus, prov provider.Provider) error {
	observed, err := prov.List(ctx)
	if err != nil {
		return err
	}
	byID := make(map[string]provider.Box, len(observed))
	for _, box := range observed {
		if box.ID != "" {
			byID[box.ID] = box
		}
	}
	for index := range status.Slots {
		slot := &status.Slots[index]
		actual, ok := byID[slot.ServiceID]
		if !ok || strings.TrimSpace(actual.Image) == "" || actual.Image == slot.Image && actual.ImageDigest == slot.ImageVersion {
			continue
		}
		if err := s.Store.SetComputeSlotObservedImage(ctx, accountID, slot.ID, actual.Image, actual.ImageDigest); err != nil {
			return fmt.Errorf("slot %d: %w", slot.Ordinal, err)
		}
		slot.Image = actual.Image
		slot.ImageVersion = actual.ImageDigest
	}
	return nil
}

func (s *Server) ensureFleetSlot(ctx context.Context, accountID string, slot v1.ComputeSlot, prov provider.Provider) (v1.ComputeSlot, error) {
	if err := s.Store.SetComputeSlotState(ctx, accountID, slot.ID, v1.FleetSlotStarting, ""); err != nil {
		return slot, err
	}
	box, err := prov.Create(ctx, provider.CreateRequest{
		Name: fleetSlotName(accountID, slot.Ordinal), Image: s.DefaultImage,
		Resources: provider.Resources{CPU: 2, MemoryMiB: 4096},
		Owner:     provider.Owner{AccountID: accountID, BoxID: "compute-slot:" + slot.ID},
		Detached:  true,
	})
	if err != nil {
		_ = s.Store.SetComputeSlotState(ctx, accountID, slot.ID, v1.FleetSlotUnhealthy, err.Error())
		return slot, err
	}
	slot.ServiceID = box.ID
	slot.ServiceName = box.Name
	slot.Region = box.Region
	slot.Image = box.Image
	slot.State = v1.FleetSlotFree
	slot.Health = "healthy"
	slot.FailureReason = ""
	if connection, connectionErr := prov.Connection(ctx, box.ID); connectionErr == nil {
		slot.DeploymentInstanceID = connection.Metadata["deploymentInstanceId"]
	}
	slot, err = s.Store.UpsertComputeSlot(ctx, accountID, slot)
	return slot, err
}
func remainingScaleDown(actual, desired int, slots []v1.ComputeSlot) int {
	excess := actual - desired
	for _, slot := range slots {
		if slot.State == v1.FleetSlotDraining && excess > 0 {
			excess--
		}
	}
	if excess < 0 {
		return 0
	}
	return excess
}

func removableSlotState(state v1.FleetSlotState) bool {
	return state == v1.FleetSlotFree || state == v1.FleetSlotStopped || state == v1.FleetSlotUnhealthy || state == v1.FleetSlotStarting
}

func repairableSlotState(state v1.FleetSlotState) bool {
	return state == v1.FleetSlotStopped || state == v1.FleetSlotUnhealthy || state == v1.FleetSlotStarting
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
