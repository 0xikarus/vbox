package controller

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
)

const contactBoxSelect = `SELECT b.id::text,b.name,b.default_agent,b.state,
	EXISTS(SELECT 1 FROM box_protection p WHERE p.account_id=b.account_id AND p.box_id=b.id)
	FROM logical_boxes b`

func scanContactBox(scanner interface{ Scan(...any) error }) (id, name, agent, state string, protected bool, err error) {
	err = scanner.Scan(&id, &name, &agent, &state, &protected)
	return
}

func (s *Store) contactBox(ctx context.Context, accountID, ref string) (id, name, agent, state string, protected bool, err error) {
	if strings.TrimSpace(ref) == "" {
		return "", "", "", "", false, fmt.Errorf("a contact box is required")
	}
	id, name, agent, state, protected, err = scanContactBox(s.DB.QueryRowContext(ctx, contactBoxSelect+` WHERE b.account_id=$1 AND (b.id::text=$2 OR b.name=$2)`, accountID, ref))
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", "", "", false, fmt.Errorf("contact box %q not found in this account", ref)
	}
	return
}

func effectiveAccess(protected bool, override sql.NullBool, roleName string) (bool, string) {
	if protected {
		return false, "Blocked because the target is protected."
	}
	if override.Valid && !override.Bool {
		return false, "Blocked by an explicit connection override."
	}
	if override.Valid && override.Bool {
		return true, "Allowed by an explicit connection override."
	}
	if roleName != "" {
		return true, "Allowed by " + roleName + "."
	}
	return false, "No role or manual connection grants access."
}

// contactViews is the shared read model for owner views and agent discovery.
func (s *Store) contactViews(ctx context.Context, accountID, senderBoxID string) ([]v1.BoxContact, error) {
	var senderName string
	if err := s.DB.QueryRowContext(ctx, `SELECT name FROM logical_boxes WHERE account_id=$1 AND id=$2 AND state<>'deleting'`, accountID, senderBoxID).Scan(&senderName); err != nil {
		return nil, fmt.Errorf("sender box not found")
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT ($2::uuid)::text,$3::text,t.id::text,t.name,t.default_agent,t.state,
		EXISTS(SELECT 1 FROM box_protection bp WHERE bp.account_id=t.account_id AND bp.box_id=t.id),
		c.can_message,c.updated_at,
		COALESCE((SELECT r.name FROM box_role_assignments a
			JOIN agent_roles r ON r.id=a.role_id AND r.account_id=a.account_id
			JOIN agent_role_permissions rp ON rp.role_id=r.id AND rp.account_id=r.account_id AND rp.permission='contacts'
			WHERE a.account_id=$1 AND a.box_id=$2::uuid AND
				(rp.scope='all' OR (rp.scope='selected' AND EXISTS(
					SELECT 1 FROM agent_role_contact_grants g WHERE g.account_id=a.account_id AND g.role_id=r.id AND g.contact_box_id=t.id)))
			ORDER BY lower(r.name),r.id LIMIT 1),''),
		COALESCE((SELECT jsonb_agg(jsonb_build_object('id',r.id::text,'name',r.name) ORDER BY lower(r.name),r.id)
			FROM box_role_assignments a JOIN agent_roles r ON r.id=a.role_id AND r.account_id=a.account_id
			WHERE a.account_id=t.account_id AND a.box_id=t.id),'[]'::jsonb)
		FROM logical_boxes t
		LEFT JOIN box_contacts c ON c.account_id=t.account_id AND c.box_id=$2::uuid AND c.contact_box_id=t.id
		WHERE t.account_id=$1 AND t.id<>$2::uuid AND t.state<>'deleting'
		ORDER BY lower(t.name),t.id`, accountID, senderBoxID, senderName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []v1.BoxContact{}
	for rows.Next() {
		var value v1.BoxContact
		var override sql.NullBool
		var updated sql.NullTime
		var roleName string
		var roles []byte
		if err := rows.Scan(&value.BoxID, &value.BoxName, &value.ContactBoxID, &value.ContactName, &value.ContactAgent, &value.ContactState, &value.Protected, &override, &updated, &roleName, &roles); err != nil {
			return nil, err
		}
		value.Override = "inherit"
		if override.Valid {
			if override.Bool {
				value.Override = "allow"
			} else {
				value.Override = "block"
			}
		}
		value.CanMessage, value.Reason = effectiveAccess(value.Protected, override, roleName)
		if updated.Valid {
			value.UpdatedAt = updated.Time
		}
		value.ContactRoles = []v1.AgentRoleSummary{}
		_ = json.Unmarshal(roles, &value.ContactRoles)
		values = append(values, value)
	}
	return values, rows.Err()
}

func (s *Store) BoxContacts(ctx context.Context, p Principal, boxRef string) ([]v1.BoxContact, error) {
	box, err := s.LogicalBox(ctx, p, boxRef)
	if err != nil {
		return nil, err
	}
	return s.contactViews(ctx, p.AccountID, box.ID)
}

func putContactOverride(ctx context.Context, tx *sql.Tx, p Principal, boxID, contactID, state string) error {
	switch state {
	case "inherit":
		_, err := tx.ExecContext(ctx, `DELETE FROM box_contacts WHERE account_id=$1 AND box_id=$2 AND contact_box_id=$3`, p.AccountID, boxID, contactID)
		return err
	case "allow", "block":
		_, err := tx.ExecContext(ctx, `INSERT INTO box_contacts(account_id,box_id,contact_box_id,can_message,can_receive,created_by)
			VALUES($1,$2,$3,$4,true,$5)
			ON CONFLICT(box_id,contact_box_id) DO UPDATE SET can_message=excluded.can_message,updated_at=now()`, p.AccountID, boxID, contactID, state == "allow", p.UserID)
		return err
	default:
		return fmt.Errorf("state must be inherit, allow, or block")
	}
}

func (s *Store) PutBoxContact(ctx context.Context, p Principal, boxRef string, request v1.PutBoxContactRequest) (v1.BoxContact, error) {
	if p.Role != "owner" {
		return v1.BoxContact{}, fmt.Errorf("only an account owner may change contact rules")
	}
	box, err := s.LogicalBox(ctx, p, boxRef)
	if err != nil {
		return v1.BoxContact{}, err
	}
	contactID, _, _, _, _, err := s.contactBox(ctx, p.AccountID, request.Contact)
	if err != nil {
		return v1.BoxContact{}, err
	}
	if contactID == box.ID {
		return v1.BoxContact{}, fmt.Errorf("a box cannot be its own contact")
	}
	request.State = strings.ToLower(strings.TrimSpace(request.State))
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return v1.BoxContact{}, err
	}
	defer tx.Rollback()
	if err := putContactOverride(ctx, tx, p, box.ID, contactID, request.State); err != nil {
		return v1.BoxContact{}, err
	}
	if request.TwoWay {
		if err := putContactOverride(ctx, tx, p, contactID, box.ID, request.State); err != nil {
			return v1.BoxContact{}, err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO audit_log(account_id,user_id,action,target_type,target_id,detail) VALUES($1,$2,'box_contact.override','logical_box',$3,jsonb_build_object('contact_box_id',$4::text,'state',$5::text,'two_way',$6::bool))`, p.AccountID, p.UserID, box.ID, contactID, request.State, request.TwoWay); err != nil {
		return v1.BoxContact{}, err
	}
	if err := tx.Commit(); err != nil {
		return v1.BoxContact{}, err
	}
	values, err := s.contactViews(ctx, p.AccountID, box.ID)
	if err != nil {
		return v1.BoxContact{}, err
	}
	for _, value := range values {
		if value.ContactBoxID == contactID {
			return value, nil
		}
	}
	return v1.BoxContact{}, fmt.Errorf("contact box not found in this account")
}

func (s *Store) DeleteBoxContact(ctx context.Context, p Principal, boxRef, contactRef string) error {
	_, err := s.PutBoxContact(ctx, p, boxRef, v1.PutBoxContactRequest{Contact: contactRef, State: "inherit"})
	return err
}

func (s *Store) ContactEntries(ctx context.Context, accountID, boxID string) ([]v1.ContactEntry, error) {
	views, err := s.contactViews(ctx, accountID, boxID)
	if err != nil {
		return nil, err
	}
	values := []v1.ContactEntry{}
	for _, view := range views {
		if !view.CanMessage {
			continue
		}
		values = append(values, v1.ContactEntry{ID: view.ContactBoxID, Name: view.ContactName, Roles: view.ContactRoles, Agent: view.ContactAgent, State: view.ContactState, CanMessage: true, Reason: view.Reason})
	}
	return values, nil
}

// AuthorizeBoxMessage re-evaluates roles and overrides for every delivery.
func (s *Store) AuthorizeBoxMessage(ctx context.Context, accountID, senderBoxID, targetBoxID string) error {
	if senderBoxID == "" || targetBoxID == "" || senderBoxID == targetBoxID {
		return fmt.Errorf("a contact must be a different box")
	}
	var senderExists bool
	if err := s.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM logical_boxes WHERE account_id=$1 AND id=$2 AND state<>'deleting')`, accountID, senderBoxID).Scan(&senderExists); err != nil || !senderExists {
		return fmt.Errorf("sender box not found")
	}
	var targetState string
	var protected bool
	if err := s.DB.QueryRowContext(ctx, `SELECT b.state,EXISTS(SELECT 1 FROM box_protection p WHERE p.account_id=b.account_id AND p.box_id=b.id) FROM logical_boxes b WHERE b.account_id=$1 AND b.id=$2 AND b.state<>'deleting'`, accountID, targetBoxID).Scan(&targetState, &protected); err != nil {
		return fmt.Errorf("contact box not found in this account")
	}
	var override sql.NullBool
	err := s.DB.QueryRowContext(ctx, `SELECT can_message FROM box_contacts WHERE account_id=$1 AND box_id=$2 AND contact_box_id=$3`, accountID, senderBoxID, targetBoxID).Scan(&override)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	var roleName string
	err = s.DB.QueryRowContext(ctx, `SELECT r.name FROM box_role_assignments a
		JOIN agent_roles r ON r.id=a.role_id AND r.account_id=a.account_id
		JOIN agent_role_permissions p ON p.role_id=r.id AND p.account_id=r.account_id AND p.permission='contacts'
		WHERE a.account_id=$1 AND a.box_id=$2 AND (p.scope='all' OR (p.scope='selected' AND EXISTS(
			SELECT 1 FROM agent_role_contact_grants g WHERE g.account_id=a.account_id AND g.role_id=r.id AND g.contact_box_id=$3)))
		ORDER BY lower(r.name),r.id LIMIT 1`, accountID, senderBoxID, targetBoxID).Scan(&roleName)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	allowed, reason := effectiveAccess(protected, override, roleName)
	if !allowed {
		return errors.New(reason)
	}
	if targetState != string(v1.LogicalBoxRunning) {
		return fmt.Errorf("contact box is %s; only a running box can receive a message", targetState)
	}
	return nil
}

func (s *Store) contactBoxName(ctx context.Context, accountID, boxID string) (string, error) {
	var name string
	err := s.DB.QueryRowContext(ctx, `SELECT name FROM logical_boxes WHERE account_id=$1 AND id=$2`, accountID, boxID).Scan(&name)
	return name, err
}

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
		_, err = s.DB.ExecContext(ctx, `INSERT INTO box_protection(account_id,box_id,protected_by) VALUES($1,$2,$3) ON CONFLICT(account_id,box_id) DO NOTHING`, p.AccountID, box.ID, p.UserID)
	} else {
		_, err = s.DB.ExecContext(ctx, `DELETE FROM box_protection WHERE account_id=$1 AND box_id=$2`, p.AccountID, box.ID)
	}
	if err != nil {
		return err
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

func (s *Server) agentContactsHandler(w http.ResponseWriter, r *http.Request, p Principal) {
	values, err := s.Store.ContactEntries(r.Context(), p.AccountID, r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"contacts": values})
}
