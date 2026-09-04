package controller

import (
	"context"
	"fmt"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
)

type AccountFleetConfig struct {
	AccountID string
	Config    v1.FleetConfig
}

func (s *Store) ListFleetConfigs(ctx context.Context) ([]AccountFleetConfig, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT account_id::text,provider,provider_credential,compute_box_slots,updated_at FROM fleet_settings ORDER BY account_id,provider,provider_credential`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var values []AccountFleetConfig
	for rows.Next() {
		var value AccountFleetConfig
		if err := rows.Scan(&value.AccountID, &value.Config.Provider, &value.Config.ProviderCredential, &value.Config.ComputeBoxSlots, &value.Config.UpdatedAt); err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (s *Store) SetComputeSlotState(ctx context.Context, accountID, id string, state v1.FleetSlotState, reason string) error {
	result, err := s.DB.ExecContext(ctx, `UPDATE compute_slots SET state=$3,failure_reason=NULLIF($4,''),updated_at=now() WHERE account_id=$1 AND id=$2`, accountID, id, state, reason)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return fmt.Errorf("compute slot not found")
	}
	return nil
}

func (s *Store) DeleteComputeSlotRecord(ctx context.Context, accountID, id string) error {
	result, err := s.DB.ExecContext(ctx, `DELETE FROM compute_slots s WHERE s.account_id=$1 AND s.id=$2 AND NOT EXISTS (SELECT 1 FROM logical_boxes b WHERE b.slot_id=s.id)`, accountID, id)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return fmt.Errorf("compute slot is occupied or no longer exists")
	}
	return nil
}
