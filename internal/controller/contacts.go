package controller

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"sort"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
)

// contactTargetSelect resolves a contact box by identifier or exact name inside
// one account. Protected boxes stay addressable only for the owner, never for
// another box's agent.
const contactBoxSelect = `SELECT b.id::text,b.name,COALESCE(b.role,'worker'),b.default_agent,b.state,
	EXISTS(SELECT 1 FROM box_protection p WHERE p.account_id=b.account_id AND p.box_id=b.id)
	FROM logical_boxes b`

func scanContactBox(scanner interface{ Scan(...any) error }) (id, name, role, agent, state string, protected bool, err error) {
	err = scanner.Scan(&id, &name, &role, &agent, &state, &protected)
	return
}

func (s *Store) contactBox(ctx context.Context, accountID, ref string) (id, name, role, agent, state string, protected bool, err error) {
	if ref == "" {
		return "", "", "", "", "", false, fmt.Errorf("a contact box is required")
	}
	id, name, role, agent, state, protected, err = scanContactBox(s.DB.QueryRowContext(ctx, contactBoxSelect+` WHERE b.account_id=$1 AND (b.id::text=$2 OR b.name=$2)`, accountID, ref))
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", "", "", "", false, fmt.Errorf("contact box %q not found in this account", ref)
	}
	return
}

// BoxContacts lists one box's contacts. Explicit relationships are stored in
// both directions; a manager's fleet-wide permission is implicit and is not
// materialized as rows.
func (s *Store) BoxContacts(ctx context.Context, p Principal, boxRef string) ([]v1.BoxContact, error) {
	box, err := s.LogicalBox(ctx, p, boxRef)
	if err != nil {
		return nil, err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT c.box_id::text,b.name,c.contact_box_id::text,t.name,COALESCE(t.role,'worker'),t.default_agent,t.state,c.can_message,c.can_receive,c.updated_at
		FROM box_contacts c
		JOIN logical_boxes b ON b.id=c.box_id AND b.account_id=c.account_id
		JOIN logical_boxes t ON t.id=c.contact_box_id AND t.account_id=c.account_id
		WHERE c.account_id=$1 AND c.box_id=$2 ORDER BY t.name`, p.AccountID, box.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []v1.BoxContact{}
	for rows.Next() {
		var value v1.BoxContact
		if err := rows.Scan(&value.BoxID, &value.BoxName, &value.ContactBoxID, &value.ContactName, &value.ContactRole, &value.ContactAgent, &value.ContactState, &value.CanMessage, &value.CanReceive, &value.UpdatedAt); err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

// PutBoxContact creates or updates both halves of an explicit relationship.
// Absent booleans default to true on creation and keep their current value on
// update. The reverse row swaps message/receive permissions.
func (s *Store) PutBoxContact(ctx context.Context, p Principal, boxRef string, request v1.PutBoxContactRequest) (v1.BoxContact, error) {
	box, err := s.LogicalBox(ctx, p, boxRef)
	if err != nil {
		return v1.BoxContact{}, err
	}
	contactID, contactName, contactRole, contactAgent, contactState, protected, err := s.contactBox(ctx, p.AccountID, request.Contact)
	if err != nil {
		return v1.BoxContact{}, err
	}
	if contactID == box.ID {
		return v1.BoxContact{}, fmt.Errorf("a box cannot be its own contact")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return v1.BoxContact{}, err
	}
	defer tx.Rollback()
	var value v1.BoxContact
	err = tx.QueryRowContext(ctx, `INSERT INTO box_contacts(account_id,box_id,contact_box_id,can_message,can_receive,created_by)
		VALUES($1,$2,$3,COALESCE($4,true),COALESCE($5,true),$6)
		ON CONFLICT(box_id,contact_box_id) DO UPDATE
		SET can_message=COALESCE($4,box_contacts.can_message),can_receive=COALESCE($5,box_contacts.can_receive),updated_at=now()
		RETURNING box_id::text,contact_box_id::text,can_message,can_receive,updated_at`, p.AccountID, box.ID, contactID, request.CanMessage, request.CanReceive, p.UserID).
		Scan(&value.BoxID, &value.ContactBoxID, &value.CanMessage, &value.CanReceive, &value.UpdatedAt)
	if err != nil {
		return v1.BoxContact{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO box_contacts(account_id,box_id,contact_box_id,can_message,can_receive,created_by)
		VALUES($1,$3,$2,COALESCE($5,true),COALESCE($4,true),$6)
		ON CONFLICT(box_id,contact_box_id) DO UPDATE
		SET can_message=COALESCE($5,box_contacts.can_message),can_receive=COALESCE($4,box_contacts.can_receive),updated_at=now()`, p.AccountID, box.ID, contactID, request.CanMessage, request.CanReceive, p.UserID); err != nil {
		return v1.BoxContact{}, err
	}
	value.BoxName, value.ContactName, value.ContactRole = box.Name, contactName, contactRole
	value.ContactAgent, value.ContactState = contactAgent, contactState
	if _, err := tx.ExecContext(ctx, `INSERT INTO audit_log(account_id,user_id,action,target_type,target_id,detail) VALUES($1,$2,'box_contact.put','logical_box',$3,jsonb_build_object('contact_box_id',$4::text,'contact_name',$5::text,'protected',$6::bool,'two_way',true))`, p.AccountID, p.UserID, box.ID, contactID, contactName, protected); err != nil {
		return v1.BoxContact{}, err
	}
	return value, tx.Commit()
}

func (s *Store) DeleteBoxContact(ctx context.Context, p Principal, boxRef, contactRef string) error {
	box, err := s.LogicalBox(ctx, p, boxRef)
	if err != nil {
		return err
	}
	contactID, _, _, _, _, _, err := s.contactBox(ctx, p.AccountID, contactRef)
	if err != nil {
		return err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `DELETE FROM box_contacts WHERE account_id=$1 AND ((box_id=$2 AND contact_box_id=$3) OR (box_id=$3 AND contact_box_id=$2))`, p.AccountID, box.ID, contactID)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed == 0 {
		return fmt.Errorf("contact edge not found")
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO audit_log(account_id,user_id,action,target_type,target_id,detail) VALUES($1,$2,'box_contact.delete','logical_box',$3,jsonb_build_object('contact_box_id',$4::text,'two_way',true))`, p.AccountID, p.UserID, box.ID, contactID); err != nil {
		return err
	}
	return tx.Commit()
}

// ContactEntries returns the addressable contacts of a box. A manager sees every
// non-protected box in the account; a worker sees only explicit edges. An
// explicit can_message=false edge removes a box even for a manager.
func (s *Store) ContactEntries(ctx context.Context, accountID, boxID string) ([]v1.ContactEntry, error) {
	var role string
	if err := s.DB.QueryRowContext(ctx, `SELECT COALESCE(role,'worker') FROM logical_boxes WHERE account_id=$1 AND id=$2`, accountID, boxID).Scan(&role); err != nil {
		return nil, fmt.Errorf("sender box not found")
	}
	edges := map[string]v1.ContactEntry{}
	rows, err := s.DB.QueryContext(ctx, `SELECT contact_box_id::text,can_message,can_receive FROM box_contacts WHERE account_id=$1 AND box_id=$2`, accountID, boxID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id string
		var canMessage, canReceive bool
		if err := rows.Scan(&id, &canMessage, &canReceive); err != nil {
			rows.Close()
			return nil, err
		}
		edges[id] = v1.ContactEntry{ID: id, CanMessage: canMessage, CanReceive: canReceive}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	candidates, err := s.DB.QueryContext(ctx, `SELECT b.id::text,b.name,COALESCE(b.role,'worker'),b.default_agent,b.state
		FROM logical_boxes b
		WHERE b.account_id=$1 AND b.id<>$2 AND b.state<>'deleting'
		AND NOT EXISTS (SELECT 1 FROM box_protection p WHERE p.account_id=b.account_id AND p.box_id=b.id)
		ORDER BY b.name`, accountID, boxID)
	if err != nil {
		return nil, err
	}
	defer candidates.Close()
	values := []v1.ContactEntry{}
	for candidates.Next() {
		var entry v1.ContactEntry
		if err := candidates.Scan(&entry.ID, &entry.Name, &entry.Role, &entry.Agent, &entry.State); err != nil {
			return nil, err
		}
		if edge, ok := edges[entry.ID]; ok {
			// An explicit can_message=false edge is an owner veto: a manager must
			// not fall back to the fleet-wide permission for that box.
			if role == string(v1.BoxRoleManager) && !edge.CanMessage {
				continue
			}
			entry.CanMessage, entry.CanReceive = edge.CanMessage, edge.CanReceive
		} else if role == string(v1.BoxRoleManager) {
			entry.CanMessage, entry.CanReceive = true, true
		} else {
			continue
		}
		if !entry.CanMessage && !entry.CanReceive {
			continue
		}
		values = append(values, entry)
	}
	if err := candidates.Err(); err != nil {
		return nil, err
	}
	sort.Slice(values, func(i, j int) bool { return values[i].Name < values[j].Name })
	return values, nil
}

// AuthorizeBoxMessage is the controller-side gate for every inter-box send. It
// runs regardless of what the model requested: account scope, protection, edge
// direction and a live target all have to hold.
func (s *Store) AuthorizeBoxMessage(ctx context.Context, accountID, senderBoxID, targetBoxID string) error {
	if senderBoxID == "" || targetBoxID == "" || senderBoxID == targetBoxID {
		return fmt.Errorf("a contact must be a different box")
	}
	var senderRole, targetState string
	if err := s.DB.QueryRowContext(ctx, `SELECT COALESCE(role,'worker') FROM logical_boxes WHERE account_id=$1 AND id=$2`, accountID, senderBoxID).Scan(&senderRole); err != nil {
		return fmt.Errorf("sender box not found")
	}
	if err := s.DB.QueryRowContext(ctx, `SELECT state FROM logical_boxes WHERE account_id=$1 AND id=$2`, accountID, targetBoxID).Scan(&targetState); err != nil {
		return fmt.Errorf("contact box not found in this account")
	}
	var protected bool
	if err := s.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM box_protection WHERE account_id=$1 AND box_id=$2)`, accountID, targetBoxID).Scan(&protected); err != nil {
		return err
	}
	if protected {
		return fmt.Errorf("contact box is protected")
	}
	if targetState != string(v1.LogicalBoxRunning) {
		return fmt.Errorf("contact box is %s; only a running box can receive a message", targetState)
	}
	var canMessage sql.NullBool
	err := s.DB.QueryRowContext(ctx, `SELECT can_message FROM box_contacts WHERE account_id=$1 AND box_id=$2 AND contact_box_id=$3`, accountID, senderBoxID, targetBoxID).Scan(&canMessage)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if senderRole == string(v1.BoxRoleManager) {
		if canMessage.Valid && !canMessage.Bool {
			return fmt.Errorf("contact messaging is disabled for this box")
		}
		return nil
	}
	if !canMessage.Valid || !canMessage.Bool {
		return fmt.Errorf("no contact permission to message this box")
	}
	return nil
}

func (s *Store) contactBoxName(ctx context.Context, accountID, boxID string) (string, error) {
	var name string
	err := s.DB.QueryRowContext(ctx, `SELECT name FROM logical_boxes WHERE account_id=$1 AND id=$2`, accountID, boxID).Scan(&name)
	return name, err
}

// BoxProtection reports whether the owner marked a box as off-limits to managers.
func (s *Store) BoxProtection(ctx context.Context, p Principal, boxRef string) (bool, error) {
	box, err := s.LogicalBox(ctx, p, boxRef)
	if err != nil {
		return false, err
	}
	var protected bool
	err = s.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM box_protection WHERE account_id=$1 AND box_id=$2)`, p.AccountID, box.ID).Scan(&protected)
	return protected, err
}

func (s *Store) SetBoxProtection(ctx context.Context, p Principal, boxRef string, protected bool) error {
	if p.Role != "owner" {
		return fmt.Errorf("only an account owner may change box protection")
	}
	box, err := s.LogicalBox(ctx, p, boxRef)
	if err != nil {
		return err
	}
	if protected {
		if _, err = s.DB.ExecContext(ctx, `INSERT INTO box_protection(account_id,box_id,protected_by) VALUES($1,$2,$3) ON CONFLICT(account_id,box_id) DO NOTHING`, p.AccountID, box.ID, p.UserID); err != nil {
			return err
		}
	} else {
		if _, err = s.DB.ExecContext(ctx, `DELETE FROM box_protection WHERE account_id=$1 AND box_id=$2`, p.AccountID, box.ID); err != nil {
			return err
		}
	}
	_, err = s.DB.ExecContext(ctx, `INSERT INTO audit_log(account_id,user_id,action,target_type,target_id,detail) VALUES($1,$2,'logical_box.protection','logical_box',$3,jsonb_build_object('protected',$4::bool))`, p.AccountID, p.UserID, box.ID, protected)
	return err
}

func (s *Server) boxContactsHandler(w http.ResponseWriter, r *http.Request, p Principal) {
	if r.Method == http.MethodGet {
		values, err := s.Store.BoxContacts(r.Context(), p, r.PathValue("id"))
		if err != nil {
			writeError(w, http.StatusNotFound, err)
			return
		}
		writeJSON(w, http.StatusOK, values)
		return
	}
	var request v1.PutBoxContactRequest
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	value, err := s.Store.PutBoxContact(r.Context(), p, r.PathValue("id"), request)
	if err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	writeJSON(w, http.StatusOK, value)
}

func (s *Server) deleteBoxContactHandler(w http.ResponseWriter, r *http.Request, p Principal) {
	if err := s.Store.DeleteBoxContact(r.Context(), p, r.PathValue("id"), r.PathValue("contact")); err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) boxProtectionHandler(w http.ResponseWriter, r *http.Request, p Principal) {
	if r.Method == http.MethodGet {
		protected, err := s.Store.BoxProtection(r.Context(), p, r.PathValue("id"))
		if err != nil {
			writeError(w, http.StatusNotFound, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"protected": protected})
		return
	}
	var request struct {
		Protected *bool `json:"protected"`
	}
	if err := decodeJSON(r, &request); err != nil || request.Protected == nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("protected must be true or false"))
		return
	}
	if err := s.Store.SetBoxProtection(r.Context(), p, r.PathValue("id"), *request.Protected); err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"protected": *request.Protected})
}

// agentContactsHandler is the per-box agent view. The box identity comes from the
// DesktopAgent credential, never from the request.
func (s *Server) agentContactsHandler(w http.ResponseWriter, r *http.Request, p Principal) {
	box := r.PathValue("id")
	values, err := s.Store.ContactEntries(r.Context(), p.AccountID, box)
	if err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"contacts": values})
}
