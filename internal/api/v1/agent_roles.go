package v1

import "time"

const (
	RolePermissionContacts = "contacts"
	ContactScopeNone       = "none"
	ContactScopeSelected   = "selected"
	ContactScopeAll        = "all"
)

type AgentRoleSummary struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// AgentRole is an owner-defined, account-scoped set of grants. Phase 1 exposes
// the contacts permission; the permission key is explicit so later phases can
// extend the catalogue without adding another authorization model.
type AgentRole struct {
	ID               string    `json:"id"`
	Name             string    `json:"name"`
	Description      string    `json:"description,omitempty"`
	ContactScope     string    `json:"contactScope"`
	ContactBoxIDs    []string  `json:"contactBoxIds"`
	AssignedBoxCount int       `json:"assignedBoxCount"`
	CreatedAt        time.Time `json:"createdAt,omitempty"`
	UpdatedAt        time.Time `json:"updatedAt,omitempty"`
}

type PutAgentRoleRequest struct {
	Name          string   `json:"name"`
	Description   string   `json:"description,omitempty"`
	ContactScope  string   `json:"contactScope"`
	ContactBoxIDs []string `json:"contactBoxIds,omitempty"`
}

type BoxRoleAssignment struct {
	BoxID   string   `json:"boxId"`
	RoleIDs []string `json:"roleIds"`
}

type PutRoleAssignmentsRequest struct {
	Assignments []BoxRoleAssignment `json:"assignments"`
}
