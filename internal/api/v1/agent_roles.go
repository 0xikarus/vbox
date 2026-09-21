package v1

import "time"

const (
	RolePermissionContacts        = "contacts"
	RolePermissionRequestMoreTime = "request_more_time"
	RolePermissionQueueFollowup   = "queue_followup"
	RolePermissionCreateAgentBox  = "create_agent_box"
	RolePermissionCreateEmail     = "create_email_address"
	RolePermissionSharedChats     = "shared_chats"
	ContactScopeNone              = "none"
	ContactScopeSelected          = "selected"
	ContactScopeAll               = "all"
)

type RequestMoreTimeGrant struct {
	Enabled             bool `json:"enabled"`
	MaxExtensionMinutes int  `json:"maxExtensionMinutes"`
	MaxTotalMinutes     int  `json:"maxTotalMinutes"`
}

type QueueFollowupGrant struct {
	Enabled         bool `json:"enabled"`
	MaxDelayMinutes int  `json:"maxDelayMinutes"`
	MaxPending      int  `json:"maxPending"`
}

type CreateAgentBoxGrant struct {
	Enabled           bool     `json:"enabled"`
	MaxBoxes          int      `json:"maxBoxes"`
	MaxDiskGiB        int      `json:"maxDiskGiB"`
	AllowedAgents     []string `json:"allowedAgents"`
	AssignableRoleIDs []string `json:"assignableRoleIds"`
}

type CreateEmailAddressGrant struct {
	Enabled      bool     `json:"enabled"`
	MaxAddresses int      `json:"maxAddresses"`
	Domains      []string `json:"domains"`
	AddressTypes []string `json:"addressTypes"`
}

type SharedChatsGrant struct {
	Discover  bool `json:"discover"`
	Read      bool `json:"read"`
	Subscribe bool `json:"subscribe"`
	Create    bool `json:"create"`
	Invite    bool `json:"invite"`
}

type AgentRoleCapabilities struct {
	RequestMoreTime RequestMoreTimeGrant    `json:"requestMoreTime"`
	QueueFollowup   QueueFollowupGrant      `json:"queueFollowup"`
	CreateAgentBox  CreateAgentBoxGrant     `json:"createAgentBox"`
	CreateEmail     CreateEmailAddressGrant `json:"createEmailAddress"`
	SharedChats     SharedChatsGrant        `json:"sharedChats"`
}

type AgentRoleSummary struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// AgentRole is an owner-defined, account-scoped set of typed grants. Its name
// is descriptive only and never participates in authorization.
type AgentRole struct {
	ID               string                `json:"id"`
	Name             string                `json:"name"`
	Description      string                `json:"description,omitempty"`
	ContactScope     string                `json:"contactScope"`
	ContactBoxIDs    []string              `json:"contactBoxIds"`
	Capabilities     AgentRoleCapabilities `json:"capabilities"`
	AssignedBoxCount int                   `json:"assignedBoxCount"`
	CreatedAt        time.Time             `json:"createdAt,omitempty"`
	UpdatedAt        time.Time             `json:"updatedAt,omitempty"`
}

type PutAgentRoleRequest struct {
	Name          string                `json:"name"`
	Description   string                `json:"description,omitempty"`
	ContactScope  string                `json:"contactScope"`
	ContactBoxIDs []string              `json:"contactBoxIds,omitempty"`
	Capabilities  AgentRoleCapabilities `json:"capabilities"`
}

type BoxRoleAssignment struct {
	BoxID   string   `json:"boxId"`
	RoleIDs []string `json:"roleIds"`
}

type PutRoleAssignmentsRequest struct {
	Assignments []BoxRoleAssignment `json:"assignments"`
}
