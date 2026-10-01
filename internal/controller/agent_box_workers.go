package controller

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
)

var errSelectedAgentBoxWorkerUnavailable = errors.New("selected worker slot is no longer available")

// An available worker is a healthy, unassigned compute slot in an account pool.
// A shared physical worker may offer several distinct slots.
type agentBoxWorker struct {
	SlotID             string `json:"slotId"`
	Provider           string `json:"provider"`
	ProviderCredential string `json:"providerCredential"`
	ServiceName        string `json:"serviceName,omitempty"`
	Ordinal            int    `json:"ordinal"`
	Region             string `json:"region,omitempty"`
	MemoryConfigurable bool   `json:"memoryConfigurable,omitempty"`
}

func (s *Store) availableAgentBoxWorkers(ctx context.Context, accountID, preferredProvider, preferredCredential string) ([]agentBoxWorker, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT s.id::text,s.provider,s.provider_credential,COALESCE(s.service_name,s.service_id,''),s.ordinal,COALESCE(s.region,'')
		FROM compute_slots s
		JOIN provider_credentials pc ON pc.account_id=s.account_id AND pc.provider=s.provider AND pc.name=s.provider_credential
		JOIN fleet_settings f ON f.account_id=s.account_id AND f.provider=s.provider AND f.provider_credential=s.provider_credential
		WHERE s.account_id=$1 AND s.ordinal<=f.compute_box_slots AND s.state='free' AND s.health='healthy' AND NOT pc.deleting
		AND NOT EXISTS (SELECT 1 FROM logical_boxes assigned WHERE assigned.slot_id=s.id)
		ORDER BY CASE WHEN s.provider=$2 AND s.provider_credential=$3 THEN 0 ELSE 1 END,s.provider,s.provider_credential,s.ordinal,s.id`, accountID, preferredProvider, preferredCredential)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	workers := []agentBoxWorker{}
	for rows.Next() {
		var worker agentBoxWorker
		if err := rows.Scan(&worker.SlotID, &worker.Provider, &worker.ProviderCredential, &worker.ServiceName, &worker.Ordinal, &worker.Region); err != nil {
			return nil, err
		}
		workers = append(workers, worker)
	}
	return workers, rows.Err()
}

func (s *Store) agentBoxPlacementCandidates(ctx context.Context, accountID, preferredProvider, preferredCredential, slotID string) ([]agentBoxWorker, error) {
	workers, err := s.availableAgentBoxWorkers(ctx, accountID, preferredProvider, preferredCredential)
	if err != nil {
		return nil, err
	}
	if slotID != "" {
		for _, worker := range workers {
			if worker.SlotID == slotID {
				return []agentBoxWorker{worker}, nil
			}
		}
		return nil, errSelectedAgentBoxWorkerUnavailable
	}
	// Automatic placement needs one candidate per pool. The creation
	// transaction reserves any free slot there, so concurrent team members
	// do not all race for the same first slot.
	candidates := []agentBoxWorker{}
	seen := make(map[string]bool)
	for _, worker := range workers {
		key := worker.Provider + "\x00" + worker.ProviderCredential
		if !seen[key] {
			candidates = append(candidates, worker)
			seen[key] = true
		}
	}
	if len(candidates) == 0 {
		return nil, errNoCreationSlot
	}
	return candidates, nil
}

func (s *Server) agentBoxWorkersHandler(w http.ResponseWriter, r *http.Request, p Principal) {
	w.Header().Set("Cache-Control", "no-store")
	capabilities, err := s.Store.EffectiveAgentCapabilities(r.Context(), p.AccountID, agentBoxID(p))
	if err != nil {
		writeError(w, 500, fmt.Errorf("worker options unavailable"))
		return
	}
	if err := requireCapability(capabilities.CreateAgentBox.Enabled, "create_agent_box"); err != nil {
		writeError(w, 403, err)
		return
	}
	var providerName, credential string
	err = s.Store.DB.QueryRowContext(r.Context(), `SELECT provider,provider_credential FROM logical_boxes WHERE account_id=$1 AND id=$2 AND state='running'`, p.AccountID, agentBoxID(p)).Scan(&providerName, &credential)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, 409, fmt.Errorf("creator box is unavailable"))
		return
	}
	if err != nil {
		writeError(w, 500, fmt.Errorf("worker options unavailable"))
		return
	}
	workers, err := s.Store.availableAgentBoxWorkers(r.Context(), p.AccountID, providerName, credential)
	if err != nil {
		writeError(w, 500, fmt.Errorf("worker options unavailable"))
		return
	}
	memoryPools := map[string]bool{}
	for i := range workers {
		worker := &workers[i]
		if worker.Provider != "shared-worker" {
			continue
		}
		if configurable, ok := memoryPools[worker.ProviderCredential]; ok {
			worker.MemoryConfigurable = configurable
			continue
		}
		worker.MemoryConfigurable = s.verifyBoxMemoryPool(r.Context(), p.AccountID, worker.Provider, worker.ProviderCredential, nil) == nil
		memoryPools[worker.ProviderCredential] = worker.MemoryConfigurable
	}
	writeJSON(w, 200, workers)
}
