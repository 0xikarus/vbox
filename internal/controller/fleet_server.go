package controller

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/provider"
)

func fleetTarget(r *http.Request) (string, string, error) {
	providerName := strings.TrimSpace(r.URL.Query().Get("provider"))
	credential := strings.TrimSpace(r.URL.Query().Get("providerCredential"))
	if providerName == "" {
		return "", "", fmt.Errorf("provider query parameter is required")
	}
	return providerName, credential, nil
}

func (s *Server) fleetStatus(w http.ResponseWriter, r *http.Request, p Principal) {
	providerName, credential, err := fleetTarget(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	status, err := s.Store.FleetStatus(r.Context(), p.AccountID, providerName, credential)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) fleetCosts(w http.ResponseWriter, r *http.Request, p Principal) {
	w.Header().Set("Cache-Control", "no-store")
	providerName, credential, err := fleetTarget(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	status, err := s.Store.FleetStatus(r.Context(), p.AccountID, providerName, credential)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	prov, err := s.provider(r.Context(), p.AccountID, providerName, credential)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	ids := make([]string, 0, len(status.Slots))
	for _, slot := range status.Slots {
		if slot.ServiceID != "" {
			ids = append(ids, slot.ServiceID)
		}
	}
	usageByID := make(map[string]provider.Usage, len(ids))
	if batch, ok := prov.(provider.BatchUsageProvider); ok {
		usageByID, err = batch.UsageBatch(r.Context(), ids)
	} else {
		for _, id := range ids {
			usageByID[id], err = prov.Usage(r.Context(), id)
			if err != nil {
				break
			}
		}
	}
	if err != nil {
		writeError(w, http.StatusBadGateway, fmt.Errorf("read provider costs: %w", err))
		return
	}
	overview := v1.FleetCostOverview{
		Provider: providerName, ProviderCredential: credential, Period: "current provider billing period",
		Slots: make([]v1.FleetSlotCost, 0, len(status.Slots)),
		Total: v1.FleetCost{Detail: "Sum of available fleet service costs."},
	}
	currencyConflict := false
	for _, slot := range status.Slots {
		item := v1.FleetSlotCost{Ordinal: slot.Ordinal, State: slot.State, ServiceID: slot.ServiceID, ServiceName: slot.ServiceName, LogicalBoxName: slot.LogicalBoxName}
		if slot.ServiceID == "" {
			item.Cost.Detail = "No provider service has been created for this slot."
			overview.UnavailableSlotCount++
		} else {
			usage := usageByID[slot.ServiceID]
			item.ObservedAt = usage.ObservedAt
			item.Cost = v1.FleetCost{Currency: usage.Cost.Currency, Accrued: usage.Cost.Accrued, Estimated: usage.Cost.Estimated, Available: usage.Cost.Available, Detail: usage.Cost.Detail}
			if usage.ObservedAt.After(overview.ObservedAt) {
				overview.ObservedAt = usage.ObservedAt
			}
			if item.Cost.Available {
				overview.AvailableSlotCount++
				if overview.Total.Currency == "" {
					overview.Total.Currency = item.Cost.Currency
				}
				if overview.Total.Currency == item.Cost.Currency {
					overview.Total.Accrued += item.Cost.Accrued
					overview.Total.Available = true
					overview.Total.Estimated = overview.Total.Estimated || item.Cost.Estimated
				} else {
					currencyConflict = true
				}
			} else {
				overview.UnavailableSlotCount++
			}
		}
		overview.Slots = append(overview.Slots, item)
	}
	if currencyConflict {
		overview.Total.Available = false
		overview.Total.Accrued = 0
		overview.Total.Detail = "Available service costs use multiple currencies and cannot be summed."
	}
	if len(status.Slots) == 0 {
		overview.Total.Detail = "No compute slots are configured."
	}
	writeJSON(w, http.StatusOK, overview)
}

func (s *Server) fleetSlots(w http.ResponseWriter, r *http.Request, p Principal) {
	providerName, credential, err := fleetTarget(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	config, err := s.Store.FleetConfig(r.Context(), p.AccountID, providerName, credential)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, config)
}

func (s *Server) setFleetSlots(w http.ResponseWriter, r *http.Request, p Principal) {
	var request v1.SetFleetSlotsRequest
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	config, err := s.Store.SetFleetConfig(r.Context(), p, v1.FleetConfig{
		Provider: request.Provider, ProviderCredential: request.ProviderCredential,
		ComputeBoxSlots: request.ComputeBoxSlots,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, config)
	go func() {
		if err := s.reconcileFleet(context.Background(), p.AccountID, config); err != nil {
			s.Logger.Error("compute fleet scaling failed", "account", p.AccountID, "provider", config.Provider, "error", err)
		}
	}()
}

func (s *Server) registerLogicalBox(w http.ResponseWriter, r *http.Request, p Principal) {
	var box v1.LogicalBox
	if err := decodeJSON(r, &box); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	created, err := s.Store.UpsertLogicalBox(r.Context(), p, box)
	if err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (s *Server) reserveLogicalBox(w http.ResponseWriter, r *http.Request, p Principal) {
	var request struct {
		LeaseOwner   string `json:"leaseOwner,omitempty"`
		LeaseSeconds int    `json:"leaseSeconds,omitempty"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	duration := time.Duration(request.LeaseSeconds) * time.Second
	allocation, err := s.Store.ReserveAllocation(r.Context(), p, r.PathValue("id"), r.Header.Get("Idempotency-Key"), request.LeaseOwner, duration)
	if err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	// A different idempotency key means ReserveAllocation returned the already
	// active request for this box. Its original activation (or startup recovery)
	// owns the provider mutation; this caller only follows its progress.
	if (allocation.State == "reserved" || allocation.State == "attaching") && allocation.IdempotencyKey == r.Header.Get("Idempotency-Key") {
		go func() {
			if err := s.activateAllocation(context.Background(), p.AccountID, allocation, allocation.State == "attaching"); err != nil {
				s.Logger.Error("logical box allocation failed", "allocation", allocation.RequestID, "error", err)
			}
		}()
	}
	status := http.StatusAccepted
	if allocation.State == "ready" {
		status = http.StatusOK
	}
	writeJSON(w, status, allocation)
}

func (s *Server) listLogicalBoxes(w http.ResponseWriter, r *http.Request, p Principal) {
	providerName := strings.TrimSpace(r.URL.Query().Get("provider"))
	credential := strings.TrimSpace(r.URL.Query().Get("providerCredential"))
	boxes, err := s.Store.ListLogicalBoxes(r.Context(), p, providerName, credential)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, boxes)
}

func (s *Server) getLogicalBox(w http.ResponseWriter, r *http.Request, p Principal) {
	box, err := s.Store.LogicalBox(r.Context(), p, r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, box)
}

func (s *Server) getAllocation(w http.ResponseWriter, r *http.Request, p Principal) {
	allocation, err := s.Store.Allocation(r.Context(), p.AccountID, r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	box, err := s.Store.LogicalBox(r.Context(), p, allocation.LogicalBoxID)
	if err != nil {
		writeError(w, http.StatusForbidden, err)
		return
	}
	if box.ID == "" {
		writeError(w, http.StatusNotFound, fmt.Errorf("logical box not found"))
		return
	}
	writeJSON(w, http.StatusOK, allocation)
}

func (s *Server) hibernateLogicalBoxHandler(w http.ResponseWriter, r *http.Request, p Principal) {
	assignment, err := s.Store.BeginLogicalBoxRelease(r.Context(), p, r.PathValue("id"), v1.LogicalBoxHibernating)
	if err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	if assignment.Released {
		writeJSON(w, http.StatusOK, assignment.Box)
		return
	}
	s.startLogicalBoxHibernate(p, assignment.Box.ID)
	writeJSON(w, http.StatusAccepted, assignment.Box)
}

func (s *Server) deleteLogicalBoxVolumeHandler(w http.ResponseWriter, r *http.Request, p Principal) {
	var request struct {
		Confirmation string `json:"confirmation"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	box, err := s.Store.LogicalBox(r.Context(), p, r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	if box.State == v1.LogicalBoxAttaching && box.RestorationState == "creation-reserved" && box.FailureReason != "" && pendingVolume(box.VolumeID) {
		if request.Confirmation != box.Name {
			writeError(w, http.StatusConflict, fmt.Errorf("deletion confirmation must exactly match logical box name %q", box.Name))
			return
		}
		if err := s.cancelUnmaterializedBoxCreation(r.Context(), p, box); err != nil {
			writeError(w, http.StatusConflict, err)
			return
		}
		writeJSON(w, http.StatusAccepted, box)
		return
	}
	box, err = s.queueLogicalBoxDelete(r.Context(), p, r.PathValue("id"), request.Confirmation)
	if err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	s.startLogicalBoxDelete(p, box.ID)
	writeJSON(w, http.StatusAccepted, box)
}
