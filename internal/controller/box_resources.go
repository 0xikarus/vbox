package controller

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"time"

	"github.com/0xikarus/vmbox-service/internal/provider"
)

func (s *Server) boxResources(w http.ResponseWriter, r *http.Request, p Principal) {
	a, err := s.Store.assignment(r.Context(), p.AccountID, r.PathValue("id"))
	if err != nil || a.Box.State != "running" || a.Slot.ServiceID == "" {
		writeError(w, 409, fmt.Errorf("resources require a running, assigned box"))
		return
	}
	prov, err := s.provider(r.Context(), p.AccountID, a.Box.Provider, a.Box.ProviderCredential)
	if err != nil {
		writeError(w, 502, err)
		return
	}
	limits, ok := prov.(provider.ResourceLimitsProvider)
	if !ok {
		writeError(w, 409, fmt.Errorf("provider does not support resource limit settings"))
		return
	}
	resources, err := limits.ResourceLimits(r.Context(), a.Slot.ServiceID)
	if err != nil {
		writeError(w, 502, err)
		return
	}
	response := map[string]any{"slotId": a.Slot.ID, "assignmentGeneration": a.Box.AssignmentGeneration, "resources": resources}
	if usageReader, ok := prov.(provider.ResourceUsageProvider); ok {
		if usage, err := usageReader.ResourceUsage(r.Context(), a.Slot.ServiceID); err == nil {
			response["memoryUsedBytes"] = usage.MemoryUsedBytes
			response["swapUsedBytes"] = usage.SwapUsedBytes
			if usage.DiskUsedBytes != nil {
				response["diskUsedBytes"] = *usage.DiskUsedBytes
			}
			if usage.DiskTotalBytes != nil {
				response["diskTotalBytes"] = *usage.DiskTotalBytes
			}
			response["diskEnforced"] = usage.DiskEnforced
			if usage.DiskObservedAt != nil {
				response["diskObservedAt"] = usage.DiskObservedAt
			}
			if usage.DiskPartial {
				response["diskPartial"] = true
			}
			if usage.DiskUnavailableReason != "" {
				response["diskUnavailableReason"] = usage.DiskUnavailableReason
			}
			if usage.HostDiskUsedBytes != nil && usage.HostDiskTotalBytes != nil {
				response["hostDiskUsedBytes"] = *usage.HostDiskUsedBytes
				response["hostDiskTotalBytes"] = *usage.HostDiskTotalBytes
			}
			response["observedAt"] = usage.ObservedAt
		}
	}
	current, err := s.Store.assignment(r.Context(), p.AccountID, a.Box.ID)
	if err != nil || nativeFence(current) != nativeFence(a) || current.Box.State != "running" {
		writeError(w, 409, fmt.Errorf("assignment changed; reload resource settings"))
		return
	}
	if settings, ok := prov.(provider.WorkerSettingsProvider); ok {
		if config, err := settings.WorkerConfig(r.Context()); err == nil {
			response["limits"] = config.Limits
		}
	}
	writeJSON(w, 200, response)
}

type boxResourceRequest struct {
	SlotID               string  `json:"slotId"`
	AssignmentGeneration int64   `json:"assignmentGeneration"`
	CPU                  float64 `json:"cpu"`
	MemoryMiB            int64   `json:"memoryMiB"`
	SwapMiB              *int64  `json:"swapMiB,omitempty"`
}

func (v boxResourceRequest) validate() error {
	if v.SlotID == "" || v.AssignmentGeneration <= 0 || math.IsNaN(v.CPU) || math.IsInf(v.CPU, 0) || v.CPU <= 0 || v.MemoryMiB < 256 {
		return fmt.Errorf("current slot and assignment, positive CPU, and at least 256 MiB RAM are required")
	}
	return nil
}

func (s *Server) setBoxResources(w http.ResponseWriter, r *http.Request, p Principal) {
	var request boxResourceRequest
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, 400, err)
		return
	}
	if err := request.validate(); err != nil {
		writeError(w, 400, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	tx, err := s.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	defer tx.Rollback()
	// Keep the assignment pinned during the provider mutation. Release/allocation
	// paths lock these same rows in this order, so a reused slot cannot be resized.
	box, err := scanLogicalBox(tx.QueryRowContext(ctx, logicalBoxSelect+" WHERE account_id=$1 AND id=$2 FOR UPDATE", p.AccountID, r.PathValue("id")))
	if err != nil || box.State != "running" || box.SlotID != request.SlotID || box.AssignmentGeneration != request.AssignmentGeneration {
		writeError(w, 409, fmt.Errorf("assignment changed; reload resource settings"))
		return
	}
	slot, err := scanComputeSlot(tx.QueryRowContext(ctx, computeSlotSelect+" WHERE s.account_id=$1 AND s.id=$2 FOR UPDATE OF s", p.AccountID, box.SlotID))
	if err != nil || slot.ServiceID == "" {
		writeError(w, 409, fmt.Errorf("compute slot unavailable"))
		return
	}
	if box.Provider == "shared-worker" && request.SwapMiB == nil {
		writeError(w, 400, fmt.Errorf("shared-worker resource updates require swapMiB"))
		return
	} else if box.Provider != "shared-worker" && request.SwapMiB != nil {
		writeError(w, 400, fmt.Errorf("per-box swap settings require a container-isolated shared worker"))
		return
	}
	prov, err := s.provider(ctx, p.AccountID, box.Provider, box.ProviderCredential)
	if err != nil {
		writeError(w, 502, err)
		return
	}
	if box.Provider == "shared-worker" {
		if err := sharedBoxLimitsAllowed(ctx, prov, provider.BoxLimits{CPU: request.CPU, MemoryMiB: request.MemoryMiB, SwapMiB: *request.SwapMiB}); err != nil {
			writeError(w, 400, err)
			return
		}
	}
	limits, ok := prov.(provider.ResourceLimitsProvider)
	if !ok {
		writeError(w, 409, fmt.Errorf("provider does not support resource limit settings"))
		return
	}
	if usageReader, ok := prov.(provider.ResourceUsageProvider); ok {
		if usage, err := usageReader.ResourceUsage(ctx, slot.ServiceID); err == nil {
			if request.MemoryMiB*1024*1024 < usage.MemoryUsedBytes || request.SwapMiB != nil && *request.SwapMiB*1024*1024 < usage.SwapUsedBytes {
				writeError(w, 409, fmt.Errorf("requested RAM or swap is below current usage"))
				return
			}
		}
	}
	resources := provider.Resources{CPU: request.CPU, MemoryMiB: request.MemoryMiB}
	if request.SwapMiB != nil {
		resources.SwapMiB = *request.SwapMiB
	}
	if err = limits.SetResourceLimits(ctx, slot.ServiceID, resources); err != nil {
		writeError(w, 502, fmt.Errorf("resource update could not be confirmed; reload before retrying: %w", err))
		return
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO audit_log(account_id,user_id,action,target_type,target_id,detail) VALUES($1,$2,'slot.resources.set','compute_slot',$3,jsonb_build_object('cpu',$4::float8,'memoryMiB',$5::bigint,'swapMiB',$6::bigint))`, p.AccountID, p.UserID, slot.ID, resources.CPU, resources.MemoryMiB, resources.SwapMiB)
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		writeError(w, 500, fmt.Errorf("provider accepted limits, but audit could not be saved; reload before retrying"))
		return
	}
	message := "Limits submitted for this compute slot. No restart was requested. Reload to check configured limits; the live container may require a later restart to adopt them. These settings stay with the slot, not the workspace volume."
	if box.Provider == "shared-worker" {
		message = "CPU, RAM and swap limits updated on the running container and saved with the workspace. No restart was requested."
	}
	writeJSON(w, 200, map[string]any{"resources": resources, "message": message})
}

// sharedBoxLimitsAllowed checks one box against its worker machine. Workers
// without remote settings keep the original fixed 1 CPU, 1–8 GiB RAM and
// 0–4 GiB swap.
func sharedBoxLimitsAllowed(ctx context.Context, prov provider.Provider, box provider.BoxLimits) error {
	if settings, ok := prov.(provider.WorkerSettingsProvider); ok {
		config, err := settings.WorkerConfig(ctx)
		if err == nil {
			return config.Limits.CheckBox(box)
		}
		if !errors.Is(err, provider.ErrUnsupported) {
			return fmt.Errorf("read worker limits: %w", err)
		}
	}
	if box.CPU != 1 || box.MemoryMiB%1024 != 0 || box.MemoryMiB < 1024 || box.MemoryMiB > 8192 || box.SwapMiB%1024 != 0 || box.SwapMiB < 0 || box.SwapMiB > 4096 {
		return fmt.Errorf("this shared worker fixes CPU at 1 and accepts 1–8 GiB RAM and 0–4 GiB swap; upgrade it to use the machine's full size")
	}
	return nil
}
