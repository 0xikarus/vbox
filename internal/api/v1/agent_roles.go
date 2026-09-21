package v1

import "time"

const (
	RolePermissionContacts        = "contacts"
	RolePermissionRequestMoreTime = "request_more_time"
	RolePermissionQueueFollowup   = "queue_followup"
	RolePermissionCreateAgentBox  = "create_agent_box"
	RolePermissionCreateEmail     = "create_email_address"
	RolePermissionSharedChats     = "shared_chats"
	RolePermissionMCPTools        = "mcp_tools"
	ContactScopeNone              = "none"
	ContactScopeSelected          = "selected"
	ContactScopeAll               = "all"
)

var BasicAgentMCPTools = []string{"get_contacts", "get_run_budget", "get_thread_history", "set_busy", "chat_message", "chat_ask"}

var OptionalAgentMCPTools = []string{
	"request_more_time", "queue_followup", "discover_shared_chats", "read_shared_chat", "create_shared_chat",
	"subscribe_shared_chat", "invite_to_shared_chat", "send_shared_chat_message", "create_email_address", "create_agent_box",
	"secret_request", "generate_password", "type_secret", "take_screenshot", "capture_window", "move_mouse", "click_mouse",
	"drag_mouse", "scroll_mouse", "type_text", "press_keys",
}

// CanonicalAgentMCPToolName translates names emitted by the pre-merge role UI
// to the concise snake_case public names. It keeps saved review data usable
// without continuing to advertise the old names.
func CanonicalAgentMCPToolName(name string) string {
	switch name {
	case "secret_ensure":
		return "generate_password"
	case "typeSecret":
		return "type_secret"
	case "desktop_screenshot":
		return "take_screenshot"
	case "desktop_move":
		return "move_mouse"
	case "desktop_click":
		return "click_mouse"
	case "desktop_drag":
		return "drag_mouse"
	case "desktop_scroll":
		return "scroll_mouse"
	case "desktop_type":
		return "type_text"
	case "desktop_key":
		return "press_keys"
	}
	return name
}

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

type MCPToolsGrant struct {
	Enabled      bool     `json:"enabled"`
	AllowedTools []string `json:"allowedTools"`
}

type AgentRoleCapabilities struct {
	RequestMoreTime RequestMoreTimeGrant    `json:"requestMoreTime"`
	QueueFollowup   QueueFollowupGrant      `json:"queueFollowup"`
	CreateAgentBox  CreateAgentBoxGrant     `json:"createAgentBox"`
	CreateEmail     CreateEmailAddressGrant `json:"createEmailAddress"`
	SharedChats     SharedChatsGrant        `json:"sharedChats"`
	MCPTools        MCPToolsGrant           `json:"mcpTools"`
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
