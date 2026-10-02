package controller

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/provider"
)

// workerPool is one provider worker's remotely editable settings together with
// the machine they are bounded by. Slots is the single number an owner edits:
// it is both the worker's capacity and the controller's desired slot count.
type workerPool struct {
	Provider           string                 `json:"provider"`
	ProviderCredential string                 `json:"providerCredential,omitempty"`
	Supported          bool                   `json:"supported"`
	Reason             string                 `json:"reason,omitempty"`
	Worker             *provider.WorkerConfig `json:"worker,omitempty"`
	DesiredSlots       int                    `json:"desiredSlots"`
}

type setWorkerPoolRequest struct {
	Provider           string              `json:"provider"`
	ProviderCredential string              `json:"providerCredential,omitempty"`
	Revision           uint64              `json:"revision"`
	Slots              int                 `json:"slots"`
	BoxDefaults        *provider.BoxLimits `json:"boxDefaults,omitempty"`
}

var errWorkerSettingsConflict = errors.New("worker settings changed since they were loaded; reload and retry")

func (s *Server) workerSettingsProvider(ctx context.Context, accountID, providerName, credential string) (provider.WorkerSettingsProvider, error) {
	prov, err := s.provider(ctx, accountID, providerName, credential)
	if err != nil {
		return nil, err
	}
	settings, ok := prov.(provider.WorkerSettingsProvider)
	if !ok {
		return nil, fmt.Errorf("%w: %s pools have no remotely configurable worker", provider.ErrUnsupported, providerName)
	}
	return settings, nil
}

func (s *Server) workerPool(ctx context.Context, accountID, providerName, credential string) (workerPool, error) {
	pool := workerPool{Provider: providerName, ProviderCredential: credential}
	fleet, err := s.Store.FleetConfig(ctx, accountID, providerName, credential)
	if err != nil {
		return pool, err
	}
	pool.DesiredSlots = fleet.ComputeBoxSlots
	prov, err := s.workerSettingsProvider(ctx, accountID, providerName, credential)
	if err == nil {
		var config provider.WorkerConfig
		if config, err = prov.WorkerConfig(ctx); err == nil {
			pool.Supported, pool.Worker = true, &config
		}
	}
	if errors.Is(err, provider.ErrUnsupported) {
		pool.Reason = err.Error()
		return pool, nil
	}
	return pool, err
}

func (s *Server) fleetWorker(w http.ResponseWriter, r *http.Request, p Principal) {
	w.Header().Set("Cache-Control", "no-store")
	providerName, credential, err := fleetTarget(r)
	if err != nil {
		writeError(w, 400, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	pool, err := s.workerPool(ctx, p.AccountID, providerName, credential)
	if err != nil {
		writeError(w, 502, fmt.Errorf("read worker settings: %w", err))
		return
	}
	writeJSON(w, 200, pool)
}

func (s *Server) setFleetWorker(w http.ResponseWriter, r *http.Request, p Principal) {
	var request setWorkerPoolRequest
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, 400, err)
		return
	}
	if request.Provider == "" {
		writeError(w, 400, fmt.Errorf("provider is required"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	pool, err := s.setWorkerPool(ctx, p, request)
	switch {
	case errors.Is(err, errWorkerSettingsConflict):
		writeError(w, 409, err)
	case errors.Is(err, provider.ErrUnsupported):
		writeError(w, 409, err)
	case err != nil:
		writeError(w, 400, err)
	default:
		writeJSON(w, 200, pool)
	}
}

// setWorkerPool applies one slot count to both the worker and the fleet.
// Raising changes the worker first so the reconciler can create the new slots.
// Lowering retires free slots first, because the worker refuses to drop below
// the slots that exist; occupied slots are never evicted.
func (s *Server) setWorkerPool(ctx context.Context, p Principal, request setWorkerPoolRequest) (workerPool, error) {
	prov, err := s.workerSettingsProvider(ctx, p.AccountID, request.Provider, request.ProviderCredential)
	if err != nil {
		return workerPool{}, err
	}
	config, err := prov.WorkerConfig(ctx)
	if err != nil {
		return workerPool{}, fmt.Errorf("read worker settings: %w", err)
	}
	if request.Revision != config.Settings.Revision {
		return workerPool{}, errWorkerSettingsConflict
	}
	next := config.Settings
	next.Slots = request.Slots
	if request.BoxDefaults != nil {
		next.BoxDefaults = *request.BoxDefaults
	}
	if next.Slots < 1 || next.Slots > config.Limits.MaxSlots {
		return workerPool{}, fmt.Errorf("this machine supports 1–%d slots", config.Limits.MaxSlots)
	}
	if next.Slots < config.OccupiedSlots {
		return workerPool{}, fmt.Errorf("%d boxes are on this worker; hibernate or delete boxes before lowering slots below %d", config.OccupiedSlots, config.OccupiedSlots)
	}
	if config.Limits.PerBoxLimits {
		if err := config.Limits.CheckBox(next.BoxDefaults); err != nil {
			return workerPool{}, fmt.Errorf("default %w", err)
		}
	}
	fleet := v1.FleetConfig{Provider: request.Provider, ProviderCredential: request.ProviderCredential, ComputeBoxSlots: next.Slots}
	if next.Slots < config.SlotsInUse {
		if fleet, err = s.Store.SetFleetConfig(ctx, p, fleet); err != nil {
			return workerPool{}, err
		}
		if err := s.reconcileFleet(ctx, p.AccountID, fleet); err != nil {
			return workerPool{}, fmt.Errorf("free slots could not be retired; desired slots are now %d and the worker keeps its current capacity: %w", next.Slots, err)
		}
	}
	updated, err := prov.SetWorkerSettings(ctx, next)
	if err != nil {
		return workerPool{}, err
	}
	if fleet, err = s.Store.SetFleetConfig(ctx, p, fleet); err != nil {
		return workerPool{}, fmt.Errorf("worker updated, but desired slots could not be saved; retry: %w", err)
	}
	_, err = s.Store.DB.ExecContext(ctx, `INSERT INTO audit_log(account_id,user_id,action,target_type,target_id,detail) VALUES($1,$2,'provider.worker.settings.set','provider',$3,jsonb_build_object('slots',$4::integer,'cpu',$5::float8,'memoryMiB',$6::bigint,'swapMiB',$7::bigint))`,
		p.AccountID, p.UserID, request.Provider+":"+request.ProviderCredential, updated.Settings.Slots, updated.Settings.BoxDefaults.CPU, updated.Settings.BoxDefaults.MemoryMiB, updated.Settings.BoxDefaults.SwapMiB)
	if err != nil {
		s.Logger.Error("worker settings audit failed", "account", p.AccountID, "provider", request.Provider, "error", err)
	}
	go func() {
		if err := s.reconcileFleet(context.Background(), p.AccountID, fleet); err != nil {
			s.Logger.Error("compute fleet scaling failed", "account", p.AccountID, "provider", fleet.Provider, "error", err)
		}
	}()
	return workerPool{Provider: request.Provider, ProviderCredential: request.ProviderCredential, Supported: true, Worker: &updated, DesiredSlots: fleet.ComputeBoxSlots}, nil
}

func (s *Server) setWorkerPoolSlots(ctx context.Context, p Principal, request v1.SetFleetSlotsRequest) (workerPool, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	prov, err := s.workerSettingsProvider(ctx, p.AccountID, request.Provider, request.ProviderCredential)
	if err != nil {
		return workerPool{}, err
	}
	config, err := prov.WorkerConfig(ctx)
	if err != nil {
		return workerPool{}, err
	}
	return s.setWorkerPool(ctx, p, setWorkerPoolRequest{Provider: request.Provider, ProviderCredential: request.ProviderCredential, Revision: config.Settings.Revision, Slots: request.ComputeBoxSlots})
}
