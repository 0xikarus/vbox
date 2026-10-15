package controller

import (
	"context"
	"fmt"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/provider"
)

func (s *Server) validateGoalTaskConfiguration(ctx context.Context, a goalTaskActor, req v1.CreateGoalTaskRequest) error {
	profiles, err := s.Store.resolveGoalTaskProfiles(ctx, a, req)
	if err != nil {
		return err
	}
	if err = s.validateBoxProfileRefs(ctx, a.AccountID, profiles); err != nil {
		return fmt.Errorf("selected profile or model is unavailable")
	}
	for _, role := range req.RoleIDs {
		var exists bool
		if err = s.Store.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM agent_roles WHERE account_id=$1 AND id::text=$2)`, a.AccountID, role).Scan(&exists); err != nil || !exists {
			return fmt.Errorf("selected role is unavailable")
		}
	}
	return nil
}

func (s *Server) validateGoalTaskAdmission(ctx context.Context, a goalTaskActor, req v1.CreateGoalTaskRequest) error {
	if err := s.validateGoalTaskConfiguration(ctx, a, req); err != nil {
		return err
	}
	if req.SlotID == "" && req.MemoryGiB == 0 {
		return nil
	}
	rows, err := s.Store.DB.QueryContext(ctx, `SELECT DISTINCT provider,provider_credential FROM compute_slots WHERE account_id=$1 AND ($2='' OR id::text=$2)`, a.AccountID, req.SlotID)
	if err != nil {
		return fmt.Errorf("worker configuration unavailable")
	}
	type pool struct{ provider, credential string }
	pools := []pool{}
	for rows.Next() {
		var p pool
		if err = rows.Scan(&p.provider, &p.credential); err != nil {
			rows.Close()
			return fmt.Errorf("worker configuration unavailable")
		}
		pools = append(pools, p)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return fmt.Errorf("worker configuration unavailable")
	}
	if len(pools) == 0 {
		return fmt.Errorf("worker slot is unavailable")
	}
	if req.MemoryGiB == 0 {
		return nil
	}
	for _, p := range pools {
		if s.verifyBoxMemoryPool(ctx, a.AccountID, p.provider, p.credential, &provider.BoxLimits{MemoryMiB: req.MemoryGiB * 1024, SwapMiB: *req.SwapGiB * 1024}) == nil {
			return nil
		}
	}
	return fmt.Errorf("requested memory and swap require a compatible container-isolated worker")
}

// Current disk availability is a dispatch condition, not a reason to reject a
// valid queued task. Providers without host disk telemetry enforce it on create.
func (s *Server) goalTaskWorkerDiskFits(ctx context.Context, account string, w agentBoxWorker, gib int64) bool {
	prov, err := s.provider(ctx, account, w.Provider, w.ProviderCredential)
	if err != nil {
		return false
	}
	reporter, ok := prov.(provider.HostResourcesProvider)
	if !ok {
		return true
	}
	probe, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	resources, err := reporter.HostResources(probe)
	if err != nil {
		return false
	}
	return resources.DiskFreeBytes == nil || *resources.DiskFreeBytes >= gib*(1<<30)
}
