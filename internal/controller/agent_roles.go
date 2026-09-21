package controller

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
)

func validateAgentRoleRequest(request v1.PutAgentRoleRequest) (v1.PutAgentRoleRequest, error) {
	request.Name = strings.TrimSpace(request.Name)
	request.Description = strings.TrimSpace(request.Description)
	request.ContactScope = strings.ToLower(strings.TrimSpace(request.ContactScope))
	if request.ContactScope == "" {
		request.ContactScope = v1.ContactScopeNone
	}
	if request.Name == "" || utf8.RuneCountInString(request.Name) > 64 {
		return request, fmt.Errorf("role name must contain between 1 and 64 characters")
	}
	if utf8.RuneCountInString(request.Description) > 500 {
		return request, fmt.Errorf("role description must not exceed 500 characters")
	}
	switch request.ContactScope {
	case v1.ContactScopeNone, v1.ContactScopeAll:
		request.ContactBoxIDs = nil
	case v1.ContactScopeSelected:
		seen := map[string]bool{}
		ids := make([]string, 0, len(request.ContactBoxIDs))
		for _, id := range request.ContactBoxIDs {
			id = strings.TrimSpace(id)
			if id == "" || seen[id] {
				continue
			}
			seen[id] = true
			ids = append(ids, id)
		}
		request.ContactBoxIDs = ids
	default:
		return request, fmt.Errorf("contactScope must be none, selected, or all")
	}
	return request, nil
}

func scanAgentRole(scanner interface{ Scan(...any) error }) (v1.AgentRole, error) {
	var role v1.AgentRole
	err := scanner.Scan(&role.ID, &role.Name, &role.Description, &role.ContactScope, &role.AssignedBoxCount, &role.CreatedAt, &role.UpdatedAt)
	role.ContactBoxIDs = []string{}
	return role, err
}

const agentRoleSelect = `SELECT r.id::text,r.name,r.description,COALESCE(p.scope,'none'),
	(SELECT count(*) FROM box_role_assignments a WHERE a.account_id=r.account_id AND a.role_id=r.id),r.created_at,r.updated_at
	FROM agent_roles r LEFT JOIN agent_role_permissions p ON p.account_id=r.account_id AND p.role_id=r.id AND p.permission='contacts'`

func loadRoleContactIDs(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, accountID string, roles []v1.AgentRole) error {
	if len(roles) == 0 {
		return nil
	}
	rows, err := q.QueryContext(ctx, `SELECT role_id::text,contact_box_id::text FROM agent_role_contact_grants WHERE account_id=$1 ORDER BY role_id,contact_box_id`, accountID)
	if err != nil {
		return err
	}
	defer rows.Close()
	byID := map[string]*v1.AgentRole{}
	for i := range roles {
		byID[roles[i].ID] = &roles[i]
	}
	for rows.Next() {
		var roleID, boxID string
		if err := rows.Scan(&roleID, &boxID); err != nil {
			return err
		}
		if role := byID[roleID]; role != nil {
			role.ContactBoxIDs = append(role.ContactBoxIDs, boxID)
		}
	}
	return rows.Err()
}

func (s *Store) AgentRoles(ctx context.Context, p Principal) ([]v1.AgentRole, error) {
	rows, err := s.DB.QueryContext(ctx, agentRoleSelect+` WHERE r.account_id=$1 ORDER BY lower(r.name),r.id`, p.AccountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	roles := []v1.AgentRole{}
	for rows.Next() {
		role, err := scanAgentRole(rows)
		if err != nil {
			return nil, err
		}
		roles = append(roles, role)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return roles, loadRoleContactIDs(ctx, s.DB, p.AccountID, roles)
}

func (s *Store) AgentRole(ctx context.Context, p Principal, id string) (v1.AgentRole, error) {
	role, err := scanAgentRole(s.DB.QueryRowContext(ctx, agentRoleSelect+` WHERE r.account_id=$1 AND r.id::text=$2`, p.AccountID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return role, fmt.Errorf("role not found")
	}
	if err != nil {
		return role, err
	}
	roles := []v1.AgentRole{role}
	if err := loadRoleContactIDs(ctx, s.DB, p.AccountID, roles); err != nil {
		return role, err
	}
	return roles[0], nil
}

func validateRoleContactBoxes(ctx context.Context, tx *sql.Tx, accountID string, ids []string) error {
	for _, id := range ids {
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM logical_boxes WHERE account_id=$1 AND id::text=$2 AND state<>'deleting')`, accountID, id).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("contact box %q not found in this account", id)
		}
	}
	return nil
}

func putAgentRolePermission(ctx context.Context, tx *sql.Tx, accountID, roleID string, request v1.PutAgentRoleRequest) error {
	if err := validateRoleContactBoxes(ctx, tx, accountID, request.ContactBoxIDs); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO agent_role_permissions(account_id,role_id,permission,scope) VALUES($1,$2,'contacts',$3)
		ON CONFLICT(role_id,permission) DO UPDATE SET scope=excluded.scope,config='{}'::jsonb,updated_at=now()`, accountID, roleID, request.ContactScope); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM agent_role_contact_grants WHERE account_id=$1 AND role_id=$2`, accountID, roleID); err != nil {
		return err
	}
	for _, boxID := range request.ContactBoxIDs {
		if _, err := tx.ExecContext(ctx, `INSERT INTO agent_role_contact_grants(account_id,role_id,contact_box_id) VALUES($1,$2,$3)`, accountID, roleID, boxID); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) CreateAgentRole(ctx context.Context, p Principal, request v1.PutAgentRoleRequest) (v1.AgentRole, error) {
	if p.Role != "owner" {
		return v1.AgentRole{}, fmt.Errorf("only an account owner may manage roles")
	}
	request, err := validateAgentRoleRequest(request)
	if err != nil {
		return v1.AgentRole{}, err
	}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return v1.AgentRole{}, err
	}
	defer tx.Rollback()
	id := uuid()
	if _, err := tx.ExecContext(ctx, `INSERT INTO agent_roles(id,account_id,name,description,created_by) VALUES($1,$2,$3,$4,$5)`, id, p.AccountID, request.Name, request.Description, p.UserID); err != nil {
		return v1.AgentRole{}, err
	}
	if err := putAgentRolePermission(ctx, tx, p.AccountID, id, request); err != nil {
		return v1.AgentRole{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO audit_log(account_id,user_id,action,target_type,target_id,detail) VALUES($1,$2,'agent_role.create','agent_role',$3,jsonb_build_object('name',$4::text,'contact_scope',$5::text))`, p.AccountID, p.UserID, id, request.Name, request.ContactScope); err != nil {
		return v1.AgentRole{}, err
	}
	if err := tx.Commit(); err != nil {
		return v1.AgentRole{}, err
	}
	return s.AgentRole(ctx, p, id)
}

func (s *Store) UpdateAgentRole(ctx context.Context, p Principal, id string, request v1.PutAgentRoleRequest) (v1.AgentRole, error) {
	if p.Role != "owner" {
		return v1.AgentRole{}, fmt.Errorf("only an account owner may manage roles")
	}
	request, err := validateAgentRoleRequest(request)
	if err != nil {
		return v1.AgentRole{}, err
	}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return v1.AgentRole{}, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE agent_roles SET name=$3,description=$4,updated_at=now() WHERE account_id=$1 AND id::text=$2`, p.AccountID, id, request.Name, request.Description)
	if err != nil {
		return v1.AgentRole{}, err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return v1.AgentRole{}, fmt.Errorf("role not found")
	}
	if err := putAgentRolePermission(ctx, tx, p.AccountID, id, request); err != nil {
		return v1.AgentRole{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO audit_log(account_id,user_id,action,target_type,target_id,detail) VALUES($1,$2,'agent_role.update','agent_role',$3,jsonb_build_object('name',$4::text,'contact_scope',$5::text))`, p.AccountID, p.UserID, id, request.Name, request.ContactScope); err != nil {
		return v1.AgentRole{}, err
	}
	if err := tx.Commit(); err != nil {
		return v1.AgentRole{}, err
	}
	return s.AgentRole(ctx, p, id)
}

func (s *Store) DeleteAgentRole(ctx context.Context, p Principal, id string) error {
	if p.Role != "owner" {
		return fmt.Errorf("only an account owner may manage roles")
	}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var name string
	if err := tx.QueryRowContext(ctx, `DELETE FROM agent_roles WHERE account_id=$1 AND id::text=$2 RETURNING name`, p.AccountID, id).Scan(&name); errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("role not found")
	} else if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO audit_log(account_id,user_id,action,target_type,target_id,detail) VALUES($1,$2,'agent_role.delete','agent_role',$3,jsonb_build_object('name',$4::text))`, p.AccountID, p.UserID, id, name); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) PutRoleAssignments(ctx context.Context, p Principal, request v1.PutRoleAssignmentsRequest) error {
	if p.Role != "owner" {
		return fmt.Errorf("only an account owner may assign roles")
	}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	seenBoxes := map[string]bool{}
	for _, assignment := range request.Assignments {
		if assignment.BoxID == "" || seenBoxes[assignment.BoxID] {
			return fmt.Errorf("each box must appear exactly once in an assignment batch")
		}
		seenBoxes[assignment.BoxID] = true
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM logical_boxes WHERE account_id=$1 AND id::text=$2 AND state<>'deleting')`, p.AccountID, assignment.BoxID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("box %q not found in this account", assignment.BoxID)
		}
		seenRoles := map[string]bool{}
		for _, roleID := range assignment.RoleIDs {
			if roleID == "" || seenRoles[roleID] {
				return fmt.Errorf("role IDs must be non-empty and unique for each box")
			}
			seenRoles[roleID] = true
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM agent_roles WHERE account_id=$1 AND id::text=$2)`, p.AccountID, roleID).Scan(&exists); err != nil {
				return err
			}
			if !exists {
				return fmt.Errorf("role %q not found in this account", roleID)
			}
		}
	}
	for _, assignment := range request.Assignments {
		if _, err := tx.ExecContext(ctx, `DELETE FROM box_role_assignments WHERE account_id=$1 AND box_id=$2`, p.AccountID, assignment.BoxID); err != nil {
			return err
		}
		for _, roleID := range assignment.RoleIDs {
			if _, err := tx.ExecContext(ctx, `INSERT INTO box_role_assignments(account_id,box_id,role_id,assigned_by) VALUES($1,$2,$3,$4)`, p.AccountID, assignment.BoxID, roleID, p.UserID); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO audit_log(account_id,user_id,action,target_type,target_id,detail) VALUES($1,$2,'agent_role.assign','logical_box',$3,jsonb_build_object('role_ids',$4::jsonb))`, p.AccountID, p.UserID, assignment.BoxID, roleIDsJSON(assignment.RoleIDs)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func roleIDsJSON(ids []string) string {
	encoded, _ := json.Marshal(ids)
	return string(encoded)
}

func assignInitialRoles(ctx context.Context, tx *sql.Tx, p Principal, boxID string, roleIDs []string) error {
	seen := map[string]bool{}
	for _, roleID := range roleIDs {
		roleID = strings.TrimSpace(roleID)
		if roleID == "" || seen[roleID] {
			return fmt.Errorf("role IDs must be non-empty and unique")
		}
		seen[roleID] = true
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM agent_roles WHERE account_id=$1 AND id::text=$2)`, p.AccountID, roleID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("role %q not found in this account", roleID)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO box_role_assignments(account_id,box_id,role_id,assigned_by) VALUES($1,$2,$3,$4)`, p.AccountID, boxID, roleID, p.UserID); err != nil {
			return err
		}
	}
	if len(roleIDs) > 0 {
		if _, err := tx.ExecContext(ctx, `INSERT INTO audit_log(account_id,user_id,action,target_type,target_id,detail) VALUES($1,$2,'agent_role.assign','logical_box',$3,jsonb_build_object('role_ids',$4::jsonb,'source','creation'::text))`, p.AccountID, p.UserID, boxID, roleIDsJSON(roleIDs)); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) agentRolesHandler(w http.ResponseWriter, r *http.Request, p Principal) {
	if r.Method == http.MethodGet {
		roles, err := s.Store.AgentRoles(r.Context(), p)
		if err != nil {
			writeError(w, http.StatusConflict, err)
			return
		}
		writeJSON(w, http.StatusOK, roles)
		return
	}
	var request v1.PutAgentRoleRequest
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	role, err := s.Store.CreateAgentRole(r.Context(), p, request)
	if err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	writeJSON(w, http.StatusCreated, role)
}

func (s *Server) agentRoleHandler(w http.ResponseWriter, r *http.Request, p Principal) {
	id := r.PathValue("id")
	switch r.Method {
	case http.MethodGet:
		role, err := s.Store.AgentRole(r.Context(), p, id)
		if err != nil {
			writeError(w, http.StatusNotFound, err)
			return
		}
		writeJSON(w, http.StatusOK, role)
	case http.MethodPut:
		var request v1.PutAgentRoleRequest
		if err := decodeJSON(r, &request); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		role, err := s.Store.UpdateAgentRole(r.Context(), p, id, request)
		if err != nil {
			writeError(w, http.StatusConflict, err)
			return
		}
		writeJSON(w, http.StatusOK, role)
	case http.MethodDelete:
		if err := s.Store.DeleteAgentRole(r.Context(), p, id); err != nil {
			writeError(w, http.StatusNotFound, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *Server) roleAssignmentsHandler(w http.ResponseWriter, r *http.Request, p Principal) {
	var request v1.PutRoleAssignmentsRequest
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := s.Store.PutRoleAssignments(r.Context(), p, request); err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
