package controller

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strings"

	"github.com/0xikarus/vmbox-service/internal/provider"
)

func (s *Store) lockFleetPlacement(ctx context.Context, account, name, alias string) (*sql.Tx, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, account+":"+name+":"+alias); err != nil {
		tx.Rollback()
		return nil, err
	}
	return tx, nil
}

func (s *Store) SetFleetLocation(ctx context.Context, p Principal, name, alias, region string) error {
	tx, err := s.lockFleetPlacement(ctx, p.AccountID, name, alias)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var desired int
	var current string
	if err = tx.QueryRowContext(ctx, `SELECT compute_box_slots,region FROM fleet_settings WHERE account_id=$1 AND provider=$2 AND provider_credential=$3 FOR UPDATE`, p.AccountID, name, alias).Scan(&desired, &current); err != nil {
		return err
	}
	if current == region {
		return tx.Commit()
	}
	var slots, boxes int
	if err = tx.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM compute_slots WHERE account_id=$1 AND provider=$2 AND provider_credential=$3), (SELECT count(*) FROM logical_boxes WHERE account_id=$1 AND provider=$2 AND provider_credential=$3)`, p.AccountID, name, alias).Scan(&slots, &boxes); err != nil {
		return err
	}
	if desired != 0 || slots != 0 || boxes != 0 {
		return fmt.Errorf("location changes require an empty fleet with zero desired and actual slots (desired=%d, actual=%d, boxes=%d); existing regional boxes cannot be migrated here; for an empty fleet run vmbox fleet slots set 0 and wait for vmbox fleet status", desired, slots, boxes)
	}
	if _, err = tx.ExecContext(ctx, `UPDATE fleet_settings SET region=$4,updated_at=now() WHERE account_id=$1 AND provider=$2 AND provider_credential=$3`, p.AccountID, name, alias, region); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO audit_log(account_id,user_id,action,target_type,target_id,detail) VALUES($1,$2,'fleet.location.set','fleet',$3,jsonb_build_object('region',$4::text))`, p.AccountID, p.UserID, name+":"+alias, region); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Server) providerRegions(ctx context.Context, account, name, alias string) ([]provider.Region, error) {
	p, err := s.provider(ctx, account, name, alias)
	if err != nil {
		return nil, err
	}
	rp, ok := p.(provider.RegionProvider)
	if !ok {
		return nil, fmt.Errorf("provider %s does not support fleet location selection", name)
	}
	return rp.Regions(ctx)
}

func (s *Server) fleetRegions(w http.ResponseWriter, r *http.Request, p Principal) {
	name, alias, err := fleetTarget(r)
	if err != nil {
		writeError(w, 400, err)
		return
	}
	regions, err := s.providerRegions(r.Context(), p.AccountID, name, alias)
	if err != nil {
		writeError(w, 400, err)
		return
	}
	writeJSON(w, 200, regions)
}

func (s *Server) setFleetLocation(w http.ResponseWriter, r *http.Request, p Principal) {
	var request struct {
		Provider   string `json:"provider"`
		Credential string `json:"providerCredential"`
		Region     string `json:"region"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, 400, err)
		return
	}
	regions, err := s.providerRegions(r.Context(), p.AccountID, request.Provider, request.Credential)
	if err != nil {
		writeError(w, 400, err)
		return
	}
	request.Region = strings.TrimSpace(request.Region)
	found := false
	for _, region := range regions {
		if region.ID == request.Region {
			found = true
		}
	}
	if !found {
		writeError(w, 400, fmt.Errorf("unsupported region %q; use vmbox fleet location", request.Region))
		return
	}
	if _, err = s.Store.FleetConfig(r.Context(), p.AccountID, request.Provider, request.Credential); err != nil {
		writeError(w, 500, err)
		return
	}
	if err = s.Store.SetFleetLocation(r.Context(), p, request.Provider, request.Credential, request.Region); err != nil {
		writeError(w, 409, err)
		return
	}
	writeJSON(w, 200, map[string]string{"region": request.Region})
}
