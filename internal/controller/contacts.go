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

func effectiveAccess(protected bool, override sql.NullBool, allContacts bool) (bool, string) {
	if protected {
		return false, "Blocked because the target is protected."
	}
	if override.Valid && !override.Bool {
		return false, "Blocked by an explicit contact rule."
	}
	if override.Valid && override.Bool {
		return true, "Included in this box's direct contact list."
	}
	if allContacts {
		return true, "Allowed by the All contacts capability."
	}
	return false, "Not in this box's direct contact list."
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
		COALESCE((SELECT (d.capabilities->'allContacts'->>'enabled')::boolean FROM agent_box_policies d
			WHERE d.account_id=$1 AND d.box_id=$2::uuid), EXISTS(SELECT 1 FROM box_role_assignments a
			JOIN agent_role_permissions rp ON rp.role_id=a.role_id AND rp.account_id=a.account_id
			WHERE a.account_id=$1 AND a.box_id=$2::uuid AND rp.permission='all_contacts'
				AND COALESCE((rp.config->>'enabled')::boolean,false))),
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
		var allContacts bool
		var roles []byte
		if err := rows.Scan(&value.BoxID, &value.BoxName, &value.ContactBoxID, &value.ContactName, &value.ContactAgent, &value.ContactState, &value.Protected, &override, &updated, &allContacts, &roles); err != nil {
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
		value.CanMessage, value.Reason = effectiveAccess(value.Protected, override, allContacts)
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

func putContactOverrideWithEvent(ctx context.Context, tx *sql.Tx, p Principal, boxID, contactID, contactName, state string) error {
	var previous sql.NullBool
	err := tx.QueryRowContext(ctx, `SELECT can_message FROM box_contacts WHERE account_id=$1 AND box_id=$2 AND contact_box_id=$3 FOR UPDATE`, p.AccountID, boxID, contactID).Scan(&previous)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err := putContactOverride(ctx, tx, p, boxID, contactID, state); err != nil {
		return err
	}
	changed := state == "inherit" && previous.Valid || state == "allow" && (!previous.Valid || !previous.Bool) || state == "block" && (!previous.Valid || previous.Bool)
	if !changed {
		return nil
	}
	label := "contact added"
	if state == "block" {
		label = "contact blocked"
	} else if state == "inherit" {
		label = "contact removed"
		if !previous.Bool {
			label = "contact block removed"
		}
	}
	return appendBoxEvent(ctx, tx, p.AccountID, boxID, label+" · "+contactName, "contact:"+uuid())
}

func boxNameByte(char byte) bool {
	return char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '.' || char == '_' || char == '-'
}

func containsBoxMention(text, name string) bool {
	needle := "@" + name
	for start := 0; start < len(text); {
		index := strings.Index(text[start:], needle)
		if index < 0 {
			return false
		}
		begin := start + index
		end := begin + len(needle)
		if (begin == 0 || strings.ContainsRune(" \t\r\n", rune(text[begin-1]))) && (end == len(text) || !boxNameByte(text[end])) {
			return true
		}
		start = end
	}
	return false
}

// Mention targets are resolved from exact IDs and visible @names before a
// message is routed. The browser cannot silently grant a hidden box contact.
func (s *Store) validateMentionTargets(ctx context.Context, p Principal, sourceID, text string, refs []string) ([]string, error) {
	if len(refs) == 0 {
		return nil, nil
	}
	if p.Role != "owner" || len(refs) > 8 || strings.HasPrefix(text, "/silent") {
		return nil, fmt.Errorf("box mentions require an owner message with at most eight boxes")
	}
	seen := map[string]bool{}
	ids := make([]string, 0, len(refs))
	for _, ref := range refs {
		id, name, _, state, protected, err := s.contactBox(ctx, p.AccountID, ref)
		if err != nil {
			return nil, err
		}
		if ref != id || id == sourceID || state == "deleting" || protected || !containsBoxMention(text, name) {
			return nil, fmt.Errorf("invalid box mention")
		}
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	return ids, nil
}

// Apply all reciprocal contact grants together after the owner message is
// accepted. A retry with the same message idempotency key can repair a failed
// grant without posting the message again.
func (s *Store) allowMentionContacts(ctx context.Context, p Principal, sourceID string, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var sourceName string
	if err := tx.QueryRowContext(ctx, `SELECT name FROM logical_boxes WHERE account_id=$1 AND id=$2`, p.AccountID, sourceID).Scan(&sourceName); err != nil {
		return err
	}
	for _, id := range ids {
		var targetName string
		if err := tx.QueryRowContext(ctx, `SELECT name FROM logical_boxes WHERE account_id=$1 AND id=$2`, p.AccountID, id).Scan(&targetName); err != nil {
			return err
		}
		if err := putContactOverrideWithEvent(ctx, tx, p, sourceID, id, targetName, "allow"); err != nil {
			return err
		}
		if err := putContactOverrideWithEvent(ctx, tx, p, id, sourceID, sourceName, "allow"); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO audit_log(account_id,user_id,action,target_type,target_id,detail)
			VALUES($1,$2,'box_contact.mention','logical_box',$3,jsonb_build_object('contact_box_id',$4::text,'two_way',true))`, p.AccountID, p.UserID, sourceID, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) PutBoxContact(ctx context.Context, p Principal, boxRef string, request v1.PutBoxContactRequest) (v1.BoxContact, error) {
	if p.Role != "owner" {
		return v1.BoxContact{}, fmt.Errorf("only an account owner may change contact rules")
	}
	box, err := s.LogicalBox(ctx, p, boxRef)
	if err != nil {
		return v1.BoxContact{}, err
	}
	contactID, contactName, _, _, _, err := s.contactBox(ctx, p.AccountID, request.Contact)
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
	if err := putContactOverrideWithEvent(ctx, tx, p, box.ID, contactID, contactName, request.State); err != nil {
		return v1.BoxContact{}, err
	}
	if request.TwoWay {
		if err := putContactOverrideWithEvent(ctx, tx, p, contactID, box.ID, box.Name, request.State); err != nil {
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
	shortIDs := shortContactIDs(views)
	for _, view := range views {
		if !view.CanMessage {
			continue
		}
		values = append(values, v1.ContactEntry{ID: shortIDs[view.ContactBoxID], Name: view.ContactName, Roles: view.ContactRoles, Agent: view.ContactAgent, State: view.ContactState, CanMessage: true, Reason: view.Reason})
	}
	return values, nil
}

// shortContactIDs keeps internal UUIDs out of agent context. Eight hexadecimal
// characters are normally enough; if two visible contacts share that prefix,
// extend every ID just far enough to keep all handles unambiguous.
func shortContactIDs(views []v1.BoxContact) map[string]string {
	compact := make(map[string]string, len(views))
	maxLength := 0
	for _, view := range views {
		if !view.CanMessage {
			continue
		}
		value := strings.ReplaceAll(strings.ToLower(view.ContactBoxID), "-", "")
		if value == "" {
			value = view.ContactBoxID
		}
		compact[view.ContactBoxID] = value
		if len(value) > maxLength {
			maxLength = len(value)
		}
	}
	length := 8
	for length < maxLength {
		seen, collision := map[string]bool{}, false
		for _, value := range compact {
			id := value
			if len(id) > length {
				id = id[:length]
			}
			if seen[id] {
				collision = true
				break
			}
			seen[id] = true
		}
		if !collision {
			break
		}
		length++
	}
	result := make(map[string]string, len(compact))
	for full, value := range compact {
		if len(value) > length {
			value = value[:length]
		}
		result[full] = value
	}
	return result
}

// AuthorizeBoxMessage re-evaluates direct contacts and All contacts for every delivery.
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
	var allContacts bool
	err = s.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM box_role_assignments a
		JOIN agent_role_permissions p ON p.role_id=a.role_id AND p.account_id=a.account_id
		WHERE a.account_id=$1 AND a.box_id=$2 AND p.permission='all_contacts'
			AND COALESCE((p.config->>'enabled')::boolean,false)) OR COALESCE((SELECT (d.capabilities->'allContacts'->>'enabled')::boolean
			FROM agent_box_policies d WHERE d.account_id=$1 AND d.box_id=$2),false)`, accountID, senderBoxID).Scan(&allContacts)
	if err != nil {
		return err
	}
	allowed, reason := effectiveAccess(protected, override, allContacts)
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
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var changed sql.Result
	if protected {
		changed, err = tx.ExecContext(ctx, `INSERT INTO box_protection(account_id,box_id,protected_by) VALUES($1,$2,$3) ON CONFLICT(account_id,box_id) DO NOTHING`, p.AccountID, box.ID, p.UserID)
	} else {
		changed, err = tx.ExecContext(ctx, `DELETE FROM box_protection WHERE account_id=$1 AND box_id=$2`, p.AccountID, box.ID)
	}
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO audit_log(account_id,user_id,action,target_type,target_id,detail) VALUES($1,$2,'logical_box.protection','logical_box',$3,jsonb_build_object('protected',$4::bool))`, p.AccountID, p.UserID, box.ID, protected); err != nil {
		return err
	}
	if rows, _ := changed.RowsAffected(); rows > 0 {
		label := "box protection removed"
		if protected {
			label = "box protected · incoming contacts blocked"
		}
		if err := appendBoxEvent(ctx, tx, p.AccountID, box.ID, label, "protection:"+uuid()); err != nil {
			return err
		}
	}
	return tx.Commit()
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
