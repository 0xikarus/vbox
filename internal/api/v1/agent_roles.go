package v1

import "time"

const (
	RolePermissionAllContacts      = "all_contacts"
	RolePermissionRequestMoreTime  = "request_more_time"
	RolePermissionQueueFollowup    = "queue_followup"
	RolePermissionCreateAgentBox   = "create_agent_box"
	RolePermissionManageAgentBoxes = "manage_agent_boxes"
	RolePermissionCreateEmail      = "create_email_address"
	RolePermissionSharedChats      = "shared_chats"
	RolePermissionMCPTools         = "mcp_tools"
)

var BasicAgentMCPTools = []string{"get_contacts", "get_run_budget", "get_thread_history", "set_busy", "chat_message", "chat_ask", "multicall"}

var ComputerAgentMCPTools = []string{"take_screenshot", "capture_window", "move_mouse", "click_mouse", "drag_mouse", "scroll_mouse", "type_text", "press_keys"}

var OptionalAgentMCPTools = []string{
	"heartbeat",
	"list_agent_boxes", "get_agent_box", "get_agent_box_screenshot", "create_agent_box", "get_agent_box_configs", "get_available_workers", "set_agent_box_tags", "set_agent_box_run_budget", "restart_agent_box", "wake_agent_box", "clear_agent_box_context", "compact_agent_box_context", "delete_agent_box",
	"secret_request", "generate_password", "type_secret", "take_screenshot", "capture_window", "move_mouse", "click_mouse",
	"drag_mouse", "scroll_mouse", "type_text", "press_keys",
}

// These previously advertised MCP tools are retired. Keep recognizing their
// names when an older saved role or policy is edited, but never grant them.
var RetiredCoordinationMCPTools = map[string]bool{
	"request_more_time": true, "queue_followup": true, "discover_shared_chats": true,
	"read_shared_chat": true, "create_shared_chat": true, "subscribe_shared_chat": true,
	"invite_to_shared_chat": true, "send_shared_chat_message": true, "create_email_address": true,
}

// CanonicalAgentMCPToolName translates names emitted by the pre-merge role UI
// to the concise snake_case public names. It keeps saved review data usable
// without continuing to advertise the old names.
func CanonicalAgentMCPToolName(name string) string {
	switch name {
	case "start_heartbeat", "stop_heartbeat":
		return "heartbeat"
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

// AllContactsGrant lets get_contacts discover every eligible box. Without it,
// a box sees only destinations in its own direct contact list.
type AllContactsGrant struct {
	Enabled bool `json:"enabled"`
}

type CreateAgentBoxGrant struct {
	Enabled           bool     `json:"enabled"`
	MaxBoxes          int      `json:"maxBoxes"`
	MaxDiskGiB        int      `json:"maxDiskGiB"`
	AllowedAgents     []string `json:"allowedAgents"`
	AssignableRoleIDs []string `json:"assignableRoleIds"`
}

// ManageAgentBoxesGrant covers safe inspection, metadata labels, wake/restart,
// and deletion. Creation has its own bounded grant because it carries limits.
type ManageAgentBoxesGrant struct {
	List    bool `json:"list"`
	Inspect bool `json:"inspect"`
	Tag     bool `json:"tag"`
	Restart bool `json:"restart"`
	Delete  bool `json:"delete"`
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
	AllContacts      AllContactsGrant        `json:"allContacts"`
	RequestMoreTime  RequestMoreTimeGrant    `json:"requestMoreTime"`
	QueueFollowup    QueueFollowupGrant      `json:"queueFollowup"`
	CreateAgentBox   CreateAgentBoxGrant     `json:"createAgentBox"`
	ManageAgentBoxes ManageAgentBoxesGrant   `json:"manageAgentBoxes"`
	CreateEmail      CreateEmailAddressGrant `json:"createEmailAddress"`
	SharedChats      SharedChatsGrant        `json:"sharedChats"`
	MCPTools         MCPToolsGrant           `json:"mcpTools"`
}

// AgentBoxPolicy is the owner-managed permission set attached directly to one
// box. Legacy roles are only an upgrade source and are not part of this API.
type AgentBoxPolicy struct {
	BoxID        string                `json:"boxId"`
	BoxName      string                `json:"boxName"`
	Capabilities AgentRoleCapabilities `json:"capabilities"`
	Migrated     bool                  `json:"migratedFromRoles,omitempty"`
	UpdatedAt    time.Time             `json:"updatedAt,omitempty"`
}

type PutAgentBoxPolicyRequest struct {
	Capabilities AgentRoleCapabilities `json:"capabilities"`
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
	Capabilities     AgentRoleCapabilities `json:"capabilities"`
	AssignedBoxCount int                   `json:"assignedBoxCount"`
	CreatedAt        time.Time             `json:"createdAt,omitempty"`
	UpdatedAt        time.Time             `json:"updatedAt,omitempty"`
}

type PutAgentRoleRequest struct {
	Name         string                `json:"name"`
	Description  string                `json:"description,omitempty"`
	Capabilities AgentRoleCapabilities `json:"capabilities"`
}

type BoxRoleAssignment struct {
	BoxID   string   `json:"boxId"`
	RoleIDs []string `json:"roleIds"`
}

type PutRoleAssignmentsRequest struct {
	Assignments []BoxRoleAssignment `json:"assignments"`
}
