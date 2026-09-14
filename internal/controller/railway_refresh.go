package controller

import (
	"context"

	"github.com/0xikarus/vmbox-service/internal/provider"
)

// RefreshRailwayHint refreshes advisory infrastructure inventory only. Recheck
// scope because a queued event may outlive a credential or service reassignment.
// This never mutates a logical box, assignment, deployment, or volume.
func (s *Server) RefreshRailwayHint(ctx context.Context, hint RailwayRefreshHint) error {
	var current bool
	err := s.Store.DB.QueryRowContext(ctx, `SELECT EXISTS (
 SELECT 1 FROM compute_slots c JOIN provider_credentials p
 ON p.account_id=c.account_id AND p.provider=c.provider AND p.name=c.provider_credential
 WHERE c.account_id=$1 AND c.id=$2 AND c.provider='railway' AND c.provider_credential=$3 AND c.service_id=$4
 AND p.config->>'projectId'=$5 AND p.config->>'environmentId'=$6
)`, hint.AccountID, hint.SlotID, hint.ProviderCredential, hint.ServiceID, hint.ProjectID, hint.EnvironmentID).Scan(&current)
	if err != nil || !current {
		return err
	}
	prov, err := s.provider(ctx, hint.AccountID, "railway", hint.ProviderCredential)
	if err != nil {
		return err
	}
	if direct, ok := prov.(*directWorkerProvider); ok {
		prov = direct.Provider
	}
	refresher, ok := prov.(interface{ RefreshInventory(context.Context) error })
	if !ok {
		return provider.ErrUnsupported
	}
	return refresher.RefreshInventory(ctx)
}
