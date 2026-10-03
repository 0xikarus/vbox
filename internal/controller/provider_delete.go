package controller

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/0xikarus/vmbox-service/internal/provider"
)

type providerDeleteBox struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	State string `json:"state"`
}
type providerDeleteSlot struct {
	ID          string `json:"id"`
	State       string `json:"state"`
	ServiceID   string `json:"serviceId,omitempty"`
	ServiceName string `json:"serviceName,omitempty"`
}
type providerDeletePlan struct {
	IsDefault    bool                 `json:"isDefault"`
	Boxes        []providerDeleteBox  `json:"boxes"`
	Slots        []providerDeleteSlot `json:"slots"`
	Workers      int                  `json:"workers"`
	CloudServers int                  `json:"cloudServers"`
	CanDelete    bool                 `json:"canDelete"`
	Blockers     []string             `json:"blockers"`
}
type providerDefaultChoice struct {
	Provider string `json:"provider"`
	Name     string `json:"name"`
}

func (s *Store) providerDeletePlan(ctx context.Context, accountID, name, alias string) (providerDeletePlan, error) {
	plan := providerDeletePlan{Boxes: []providerDeleteBox{}, Slots: []providerDeleteSlot{}, Blockers: []string{}}
	var deleting bool
	err := s.DB.QueryRowContext(ctx, `SELECT deleting FROM provider_credentials WHERE account_id=$1 AND provider=$2 AND name=$3`, accountID, name, alias).Scan(&deleting)
	if err != nil {
		return plan, err
	}
	err = s.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM controller_defaults WHERE account_id=$1 AND provider=$2 AND provider_credential=$3)`, accountID, name, alias).Scan(&plan.IsDefault)
	if err != nil {
		return plan, err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT id::text,name,state FROM logical_boxes WHERE account_id=$1 AND provider=$2 AND provider_credential=$3 ORDER BY name`, accountID, name, alias)
	if err != nil {
		return plan, err
	}
	for rows.Next() {
		var box providerDeleteBox
		if err = rows.Scan(&box.ID, &box.Name, &box.State); err != nil {
			break
		}
		plan.Boxes = append(plan.Boxes, box)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return plan, err
	}
	rows, err = s.DB.QueryContext(ctx, `SELECT id::text,state,COALESCE(service_id,''),COALESCE(service_name,'') FROM compute_slots WHERE account_id=$1 AND provider=$2 AND provider_credential=$3 ORDER BY ordinal`, accountID, name, alias)
	if err != nil {
		return plan, err
	}
	for rows.Next() {
		var slot providerDeleteSlot
		if err = rows.Scan(&slot.ID, &slot.State, &slot.ServiceID, &slot.ServiceName); err != nil {
			break
		}
		plan.Slots = append(plan.Slots, slot)
		if slot.ServiceID != "" {
			plan.Workers++
		}
		if name == "railway" && slot.ServiceID != "" {
			plan.CloudServers++
		}
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return plan, err
	}
	if deleting {
		plan.Blockers = append(plan.Blockers, "Deletion is already in progress")
	}
	if len(plan.Boxes) > 0 {
		plan.Blockers = append(plan.Blockers, "Delete or move every box on this provider first")
	}
	for _, slot := range plan.Slots {
		switch slot.State {
		case "free", "stopped", "unhealthy":
		default:
			plan.Blockers = append(plan.Blockers, fmt.Sprintf("Slot %s is %s; stop or drain it first", slot.ID, slot.State))
		}
	}
	plan.CanDelete = len(plan.Blockers) == 0
	return plan, nil
}

func (s *Server) providerCredentialDeletePlan(w http.ResponseWriter, r *http.Request, p Principal) {
	plan, err := s.Store.providerDeletePlan(r.Context(), p.AccountID, r.PathValue("provider"), r.PathValue("name"))
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, 404, fmt.Errorf("provider credential not found"))
		return
	}
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, plan)
}

func (s *Server) deleteProviderCredential(w http.ResponseWriter, r *http.Request, p Principal) {
	ctx := r.Context()
	name, alias := r.PathValue("provider"), r.PathValue("name")
	var req struct {
		NewDefault json.RawMessage `json:"newDefault"`
	}
	if r.ContentLength > 0 {
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, 400, err)
			return
		}
	}
	// The committed flag makes this pool unavailable to placement and fleet reconciliation.
	result, err := s.Store.DB.ExecContext(ctx, `UPDATE provider_credentials SET deleting=true WHERE account_id=$1 AND provider=$2 AND name=$3 AND deleting=false`, p.AccountID, name, alias)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	changed, _ := result.RowsAffected()
	if changed != 1 {
		writeError(w, 409, fmt.Errorf("provider credential missing or deletion already in progress"))
		return
	}
	completed := false
	type claimedSlot struct {
		providerDeleteSlot
		gone bool
	}
	claimed := []*claimedSlot{}
	defer func() {
		if !completed {
			cleanupCtx := context.WithoutCancel(ctx)
			for _, slot := range claimed {
				_, _ = s.Store.DB.ExecContext(cleanupCtx, `UPDATE compute_slots SET state=$3,health=CASE WHEN $4 THEN 'unhealthy' ELSE health END,updated_at=now() WHERE account_id=$1 AND id=$2 AND state='deprovisioning'`, p.AccountID, slot.ID, slot.State, slot.gone)
			}
			_, _ = s.Store.DB.ExecContext(cleanupCtx, `UPDATE provider_credentials SET deleting=false WHERE account_id=$1 AND provider=$2 AND name=$3`, p.AccountID, name, alias)
		}
	}()
	plan, err := s.Store.providerDeletePlan(ctx, p.AccountID, name, alias)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	// Our own flag is not a blocker; all other blockers remain fatal.
	blockers := []string{}
	for _, blocker := range plan.Blockers {
		if blocker != "Deletion is already in progress" {
			blockers = append(blockers, blocker)
		}
	}
	if len(blockers) > 0 {
		writeError(w, 409, fmt.Errorf("%s", blockers[0]))
		return
	}
	var choice *providerDefaultChoice
	if plan.IsDefault {
		if len(req.NewDefault) == 0 {
			writeError(w, 409, fmt.Errorf("newDefault is required for the default provider; use null to clear it"))
			return
		}
		if string(req.NewDefault) != "null" {
			if err := json.Unmarshal(req.NewDefault, &choice); err != nil || choice == nil || choice.Provider == "" || choice.Name == "" {
				writeError(w, 400, fmt.Errorf("newDefault must be a provider/name pair or null"))
				return
			}
		}
		if choice != nil && choice.Provider == name && choice.Name == alias {
			writeError(w, 409, fmt.Errorf("choose a different default provider"))
			return
		}
	}
	var prov provider.Provider
	for _, slot := range plan.Slots {
		if slot.ServiceID != "" {
			prov, err = s.provider(ctx, p.AccountID, name, alias)
			if err != nil {
				writeError(w, 409, fmt.Errorf("cannot deprovision services: %w", err))
				return
			}
			break
		}
	}
	for _, slot := range plan.Slots {
		if slot.ServiceID == "" {
			continue
		}
		claimedRow := &claimedSlot{providerDeleteSlot: slot}
		claimErr := s.Store.DB.QueryRowContext(ctx, `UPDATE compute_slots SET state='deprovisioning',updated_at=now() WHERE account_id=$1 AND id=$2 AND state=$3 AND state IN ('free','stopped','unhealthy') AND NOT EXISTS (SELECT 1 FROM logical_boxes WHERE slot_id=compute_slots.id) RETURNING id::text`, p.AccountID, slot.ID, slot.State).Scan(new(string))
		if errors.Is(claimErr, sql.ErrNoRows) {
			writeError(w, 409, fmt.Errorf("slot %s changed before deprovisioning; retry after it is idle", slot.ID))
			return
		}
		if claimErr != nil {
			writeError(w, 500, claimErr)
			return
		}
		claimed = append(claimed, claimedRow)
		box, inspectErr := prov.Inspect(ctx, slot.ServiceID)
		if errors.Is(inspectErr, provider.ErrNotFound) {
			claimedRow.gone = true
			continue
		}
		if inspectErr != nil {
			writeError(w, 409, fmt.Errorf("inspect service %s: %w", slot.ServiceID, inspectErr))
			return
		}
		attached := box.Storage
		if inspector, ok := prov.(provider.AttachedStorageProvider); ok {
			attached, err = inspector.AttachedStorage(ctx, box.ID)
			if err != nil {
				writeError(w, 409, fmt.Errorf("inspect service %s storage: %w", slot.ServiceID, err))
				return
			}
		}
		if attached != nil && (attached.ID != "" || attached.Name != "") {
			writeError(w, 409, fmt.Errorf("service %s still has attached storage", slot.ServiceID))
			return
		}
		if err = prov.Delete(ctx, slot.ServiceID, box.Owner); err != nil && !errors.Is(err, provider.ErrNotFound) {
			if _, checkErr := prov.Inspect(ctx, slot.ServiceID); errors.Is(checkErr, provider.ErrNotFound) {
				claimedRow.gone = true
				continue
			}
			writeError(w, 409, fmt.Errorf("deprovision service %s: %w", slot.ServiceID, err))
			return
		}
		claimedRow.gone = true
	}
	tx, err := s.Store.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		writeError(w, 500, err)
		return
	}
	defer tx.Rollback()
	var stillDeleting bool
	if err = tx.QueryRowContext(ctx, `SELECT deleting FROM provider_credentials WHERE account_id=$1 AND provider=$2 AND name=$3 FOR UPDATE`, p.AccountID, name, alias).Scan(&stillDeleting); err != nil || !stillDeleting {
		writeError(w, 409, fmt.Errorf("provider deletion lost its lock"))
		return
	}
	var boxCount, activeSlots int
	err = tx.QueryRowContext(ctx, `SELECT count(*) FROM logical_boxes WHERE account_id=$1 AND provider=$2 AND provider_credential=$3`, p.AccountID, name, alias).Scan(&boxCount)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	err = tx.QueryRowContext(ctx, `SELECT count(*) FROM compute_slots WHERE account_id=$1 AND provider=$2 AND provider_credential=$3 AND state NOT IN ('free','stopped','unhealthy','deprovisioning')`, p.AccountID, name, alias).Scan(&activeSlots)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	if boxCount > 0 || activeSlots > 0 {
		writeError(w, 409, fmt.Errorf("provider acquired boxes or active slots during deletion; retry after removing them"))
		return
	}
	if plan.IsDefault {
		if choice == nil {
			// Clearing a default must not strand the account when exactly one
			// usable provider remains. Resolve inside the deletion transaction.
			rows, listErr := tx.QueryContext(ctx, `SELECT provider,name FROM provider_credentials WHERE account_id=$1 AND NOT (provider=$2 AND name=$3) AND deleting=false ORDER BY provider,name LIMIT 2`, p.AccountID, name, alias)
			if listErr != nil {
				writeError(w, 500, listErr)
				return
			}
			var remaining providerDefaultChoice
			if rows.Next() {
				listErr = rows.Scan(&remaining.Provider, &remaining.Name)
				if listErr == nil && !rows.Next() && rows.Err() == nil {
					choice = &remaining
				}
			}
			if listErr == nil {
				listErr = rows.Err()
			}
			rows.Close()
			if listErr != nil {
				writeError(w, 500, listErr)
				return
			}
		}
		if choice == nil {
			_, err = tx.ExecContext(ctx, `DELETE FROM controller_defaults WHERE account_id=$1 AND provider=$2 AND provider_credential=$3`, p.AccountID, name, alias)
		} else {
			result, updateErr := tx.ExecContext(ctx, `UPDATE controller_defaults SET provider=$4,provider_credential=$5 WHERE account_id=$1 AND provider=$2 AND provider_credential=$3 AND EXISTS(SELECT 1 FROM provider_credentials WHERE account_id=$1 AND provider=$4 AND name=$5 AND deleting=false)`, p.AccountID, name, alias, choice.Provider, choice.Name)
			err = updateErr
			if err == nil {
				count, _ := result.RowsAffected()
				if count != 1 {
					err = fmt.Errorf("replacement default does not exist")
				}
			}
		}
		if err != nil {
			writeError(w, 409, fmt.Errorf("default migration failed: %w", err))
			return
		}
	}
	for _, query := range []string{`DELETE FROM compute_slots WHERE account_id=$1 AND provider=$2 AND provider_credential=$3`, `DELETE FROM fleet_settings WHERE account_id=$1 AND provider=$2 AND provider_credential=$3`, `DELETE FROM provider_credentials WHERE account_id=$1 AND provider=$2 AND name=$3`} {
		if _, err = tx.ExecContext(ctx, query, p.AccountID, name, alias); err != nil {
			writeError(w, 409, err)
			return
		}
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO audit_log(account_id,user_id,action,target_type,target_id,detail) VALUES($1,$2,'provider_credential.delete','provider_credential',$3,jsonb_build_object('provider',$4::text,'name',$5::text,'deprovisionedServices',$6::integer))`, p.AccountID, p.UserID, name+":"+alias, name, alias, plan.CloudServers); err != nil {
		writeError(w, 500, err)
		return
	}
	if err = tx.Commit(); err != nil {
		writeError(w, 409, err)
		return
	}
	completed = true
	w.WriteHeader(http.StatusNoContent)
}
