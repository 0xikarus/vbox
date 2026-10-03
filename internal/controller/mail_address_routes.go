package controller

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
)

var mailLocalPartPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._+-]{0,62}[a-z0-9]$|^[a-z0-9]$`)

type ownerMailAddress struct {
	ID          string   `json:"id"`
	LocalPart   string   `json:"localPart"`
	Address     string   `json:"address"`
	Label       string   `json:"label"`
	OwningBoxID string   `json:"owningBoxId,omitempty"`
	BoxIDs      []string `json:"boxIds"`
	Enabled     bool     `json:"enabled"`
	Unread      int      `json:"unread"`
}

func normalizeMailLocalPart(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if !mailLocalPartPattern.MatchString(value) || strings.Contains(value, "..") || !validMailAddress(value+"@example.com") {
		return "", fmt.Errorf("invalid mail local part")
	}
	return value, nil
}

func validateMailAddressBoxIDs(ctx context.Context, tx *sql.Tx, accountID string, ids []string) ([]string, error) {
	if len(ids) > 50 {
		return nil, fmt.Errorf("too many boxes")
	}
	clean := []string{}
	for _, id := range ids {
		if !mailUUIDPattern.MatchString(id) {
			return nil, fmt.Errorf("invalid box ID")
		}
		if slices.Contains(clean, id) {
			continue
		}
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM logical_boxes WHERE account_id=$1 AND id=$2 AND state NOT IN ('deleting','deleted'))`, accountID, id).Scan(&exists); err != nil {
			return nil, err
		}
		if !exists {
			return nil, fmt.Errorf("box unavailable")
		}
		clean = append(clean, id)
	}
	return clean, nil
}

func storeMailAddressGrants(ctx context.Context, tx *sql.Tx, accountID, addressID string, boxIDs []string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM mail_address_grants WHERE account_id=$1 AND address_id=$2`, accountID, addressID); err != nil {
		return err
	}
	for _, boxID := range boxIDs {
		if _, err := tx.ExecContext(ctx, `INSERT INTO mail_address_grants(account_id,address_id,box_id) VALUES($1,$2,$3)`, accountID, addressID, boxID); err != nil {
			return err
		}
	}
	return nil
}

func isMailAddressCollision(err error) bool {
	var pgerr *pgconn.PgError
	return errors.As(err, &pgerr) && pgerr.Code == "23505"
}

func (s *Server) ownerMailAddresses(w http.ResponseWriter, r *http.Request, p Principal) {
	if r.Method == http.MethodPost {
		s.ownerMailAddressCreate(w, r, p)
		return
	}
	rows, err := s.Store.DB.QueryContext(r.Context(), `SELECT a.id::text,a.local_part,a.address,a.label,COALESCE(a.owning_box_id::text,''),a.enabled,(SELECT count(*) FROM mail_messages m WHERE m.account_id=a.account_id AND m.address_id=a.id AND m.read_at IS NULL AND NOT m.quarantined AND m.expires_at>now()) FROM mail_addresses a WHERE a.account_id=$1 ORDER BY a.address`, p.AccountID)
	if err != nil {
		writeError(w, 500, fmt.Errorf("mail addresses unavailable"))
		return
	}
	addresses := []ownerMailAddress{}
	for rows.Next() {
		var item ownerMailAddress
		if err := rows.Scan(&item.ID, &item.LocalPart, &item.Address, &item.Label, &item.OwningBoxID, &item.Enabled, &item.Unread); err != nil {
			rows.Close()
			writeError(w, 500, fmt.Errorf("mail addresses unavailable"))
			return
		}
		addresses = append(addresses, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		writeError(w, 500, fmt.Errorf("mail addresses unavailable"))
		return
	}
	rows.Close()
	for i := range addresses {
		addresses[i].BoxIDs = []string{}
		if addresses[i].OwningBoxID != "" {
			addresses[i].BoxIDs = append(addresses[i].BoxIDs, addresses[i].OwningBoxID)
		}
		grants, err := s.Store.DB.QueryContext(r.Context(), `SELECT box_id::text FROM mail_address_grants WHERE account_id=$1 AND address_id=$2 ORDER BY box_id`, p.AccountID, addresses[i].ID)
		if err != nil {
			writeError(w, 500, fmt.Errorf("mail addresses unavailable"))
			return
		}
		for grants.Next() {
			var id string
			if err := grants.Scan(&id); err != nil {
				grants.Close()
				writeError(w, 500, fmt.Errorf("mail addresses unavailable"))
				return
			}
			if !slices.Contains(addresses[i].BoxIDs, id) {
				addresses[i].BoxIDs = append(addresses[i].BoxIDs, id)
			}
		}
		if err := grants.Err(); err != nil {
			grants.Close()
			writeError(w, 500, fmt.Errorf("mail addresses unavailable"))
			return
		}
		grants.Close()
	}
	writeJSON(w, 200, map[string]any{"addresses": addresses})
}

func (s *Server) ownerMailAddressCreate(w http.ResponseWriter, r *http.Request, p Principal) {
	var request struct {
		LocalPart string   `json:"localPart"`
		Label     string   `json:"label"`
		BoxIDs    []string `json:"boxIds"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, 400, err)
		return
	}
	local, err := normalizeMailLocalPart(request.LocalPart)
	if err != nil {
		writeError(w, 400, err)
		return
	}
	label := strings.TrimSpace(request.Label)
	if len(label) > 100 || strings.ContainsAny(label, "\r\n") {
		writeError(w, 400, fmt.Errorf("invalid mail label"))
		return
	}
	address := local + "@" + mailDomain()
	tx, err := s.Store.DB.BeginTx(r.Context(), nil)
	if err != nil {
		writeError(w, 500, fmt.Errorf("mail address unavailable"))
		return
	}
	defer tx.Rollback()
	boxIDs, err := validateMailAddressBoxIDs(r.Context(), tx, p.AccountID, request.BoxIDs)
	if err != nil {
		writeError(w, 400, err)
		return
	}
	id := uuid()
	_, err = tx.ExecContext(r.Context(), `INSERT INTO mail_addresses(id,account_id,local_part,address,label,enabled) VALUES($1,$2,$3,$4,$5,true)`, id, p.AccountID, local, address, label)
	if isMailAddressCollision(err) {
		writeError(w, 409, fmt.Errorf("mail address already exists"))
		return
	}
	if err != nil {
		writeError(w, 500, fmt.Errorf("mail address unavailable"))
		return
	}
	if err := storeMailAddressGrants(r.Context(), tx, p.AccountID, id, boxIDs); err != nil {
		writeError(w, 500, fmt.Errorf("mail grants unavailable"))
		return
	}
	if err := tx.Commit(); err != nil {
		writeError(w, 500, fmt.Errorf("mail address unavailable"))
		return
	}
	writeJSON(w, 201, ownerMailAddress{ID: id, LocalPart: local, Address: address, Label: label, BoxIDs: boxIDs, Enabled: true})
}

func (s *Server) ownerMailAddressItem(w http.ResponseWriter, r *http.Request, p Principal) {
	id := r.PathValue("aid")
	if !mailUUIDPattern.MatchString(id) {
		writeError(w, 404, fmt.Errorf("mail address unavailable"))
		return
	}
	if r.Method == http.MethodDelete {
		result, err := s.Store.DB.ExecContext(r.Context(), `DELETE FROM mail_addresses WHERE account_id=$1 AND id=$2 AND owning_box_id IS NULL`, p.AccountID, id)
		if err != nil {
			writeError(w, 500, fmt.Errorf("mail address deletion unavailable"))
			return
		}
		if n, _ := result.RowsAffected(); n == 0 {
			writeError(w, 404, fmt.Errorf("mail address unavailable"))
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	var request struct {
		LocalPart *string   `json:"localPart"`
		Label     *string   `json:"label"`
		BoxIDs    *[]string `json:"boxIds"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, 400, err)
		return
	}
	tx, err := s.Store.DB.BeginTx(r.Context(), nil)
	if err != nil {
		writeError(w, 500, fmt.Errorf("mail address unavailable"))
		return
	}
	defer tx.Rollback()
	var item ownerMailAddress
	err = tx.QueryRowContext(r.Context(), `SELECT local_part,address,label,enabled,COALESCE(owning_box_id::text,'') FROM mail_addresses WHERE account_id=$1 AND id=$2 FOR UPDATE`, p.AccountID, id).Scan(&item.LocalPart, &item.Address, &item.Label, &item.Enabled, &item.OwningBoxID)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, 404, fmt.Errorf("mail address unavailable"))
		return
	}
	if err != nil {
		writeError(w, 500, fmt.Errorf("mail address unavailable"))
		return
	}
	if item.OwningBoxID != "" && (request.LocalPart != nil || request.Label != nil) {
		writeError(w, 400, fmt.Errorf("a box's own address cannot be renamed here"))
		return
	}
	if request.LocalPart != nil {
		item.LocalPart, err = normalizeMailLocalPart(*request.LocalPart)
		if err != nil {
			writeError(w, 400, err)
			return
		}
		item.Address = item.LocalPart + "@" + mailDomain()
	}
	if request.Label != nil {
		item.Label = strings.TrimSpace(*request.Label)
		if len(item.Label) > 100 || strings.ContainsAny(item.Label, "\r\n") {
			writeError(w, 400, fmt.Errorf("invalid mail label"))
			return
		}
	}
	if item.OwningBoxID == "" {
		if _, err := tx.ExecContext(r.Context(), `UPDATE mail_addresses SET local_part=$3,address=$4,label=$5 WHERE account_id=$1 AND id=$2`, p.AccountID, id, item.LocalPart, item.Address, item.Label); err != nil {
			if isMailAddressCollision(err) {
				writeError(w, 409, fmt.Errorf("mail address already exists"))
				return
			}
			writeError(w, 500, fmt.Errorf("mail address unavailable"))
			return
		}
	}
	if request.BoxIDs != nil {
		item.BoxIDs, err = validateMailAddressBoxIDs(r.Context(), tx, p.AccountID, *request.BoxIDs)
		if err != nil {
			writeError(w, 400, err)
			return
		}
		storedGrants := append([]string(nil), item.BoxIDs...)
		if item.OwningBoxID != "" {
			storedGrants = slices.DeleteFunc(storedGrants, func(boxID string) bool { return boxID == item.OwningBoxID })
		}
		if err := storeMailAddressGrants(r.Context(), tx, p.AccountID, id, storedGrants); err != nil {
			writeError(w, 500, fmt.Errorf("mail grants unavailable"))
			return
		}
	} else {
		item.BoxIDs = []string{}
		rows, err := tx.QueryContext(r.Context(), `SELECT box_id::text FROM mail_address_grants WHERE account_id=$1 AND address_id=$2 ORDER BY box_id`, p.AccountID, id)
		if err != nil {
			writeError(w, 500, fmt.Errorf("mail grants unavailable"))
			return
		}
		for rows.Next() {
			var boxID string
			if err := rows.Scan(&boxID); err != nil {
				rows.Close()
				writeError(w, 500, fmt.Errorf("mail grants unavailable"))
				return
			}
			item.BoxIDs = append(item.BoxIDs, boxID)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			writeError(w, 500, fmt.Errorf("mail grants unavailable"))
			return
		}
		rows.Close()
	}
	if item.OwningBoxID != "" && !slices.Contains(item.BoxIDs, item.OwningBoxID) {
		item.BoxIDs = append([]string{item.OwningBoxID}, item.BoxIDs...)
	}
	if err := tx.Commit(); err != nil {
		writeError(w, 500, fmt.Errorf("mail address unavailable"))
		return
	}
	item.ID = id
	writeJSON(w, 200, item)
}

func (s *Server) ownerMailAccountSettings(w http.ResponseWriter, r *http.Request, p Principal) {
	if r.Method == http.MethodPut {
		var request struct {
			KeepUnknown *bool `json:"keepUnknown"`
		}
		if err := decodeJSON(r, &request); err != nil || request.KeepUnknown == nil {
			writeError(w, 400, fmt.Errorf("keepUnknown is required"))
			return
		}
		_, err := s.Store.DB.ExecContext(r.Context(), `INSERT INTO mail_account_settings(account_id,keep_unknown) VALUES($1,$2) ON CONFLICT(account_id) DO UPDATE SET keep_unknown=excluded.keep_unknown`, p.AccountID, *request.KeepUnknown)
		if isMailAddressCollision(err) {
			writeError(w, 409, fmt.Errorf("another account already owns catch-all mail"))
			return
		}
		if err != nil {
			writeError(w, 500, fmt.Errorf("mail settings unavailable"))
			return
		}
	}
	var enabled bool
	err := s.Store.DB.QueryRowContext(r.Context(), `SELECT keep_unknown FROM mail_account_settings WHERE account_id=$1`, p.AccountID).Scan(&enabled)
	if errors.Is(err, sql.ErrNoRows) {
		enabled = false
	} else if err != nil {
		writeError(w, 500, fmt.Errorf("mail settings unavailable"))
		return
	}
	writeJSON(w, 200, map[string]any{"keepUnknown": enabled})
}

func (s *Server) agentMailAddresses(w http.ResponseWriter, r *http.Request, p Principal) {
	if !s.agentMailAllowed(w, r, p, "list_mail_addresses") {
		return
	}
	rows, err := s.Store.DB.QueryContext(r.Context(), `SELECT a.id::text,a.address,a.label,COALESCE(a.owning_box_id=$2,false),(SELECT count(*) FROM mail_messages m WHERE m.account_id=a.account_id AND m.address_id=a.id AND m.read_at IS NULL AND NOT m.quarantined AND m.expires_at>now()) FROM mail_addresses a WHERE a.account_id=$1 AND a.enabled AND (a.owning_box_id=$2 OR EXISTS(SELECT 1 FROM mail_address_grants g WHERE g.account_id=a.account_id AND g.address_id=a.id AND g.box_id=$2)) ORDER BY a.address`, p.AccountID, agentBoxID(p))
	if err != nil {
		writeError(w, 500, fmt.Errorf("mail addresses unavailable"))
		return
	}
	defer rows.Close()
	type address struct {
		ID      string `json:"id"`
		Address string `json:"address"`
		Label   string `json:"label"`
		Own     bool   `json:"own"`
		Unread  int    `json:"unread"`
	}
	items := []address{}
	for rows.Next() {
		var item address
		if err := rows.Scan(&item.ID, &item.Address, &item.Label, &item.Own, &item.Unread); err != nil {
			writeError(w, 500, fmt.Errorf("mail addresses unavailable"))
			return
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		writeError(w, 500, fmt.Errorf("mail addresses unavailable"))
		return
	}
	writeJSON(w, 200, map[string]any{"addresses": items})
}
