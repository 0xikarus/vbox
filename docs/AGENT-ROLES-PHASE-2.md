# Agent roles: Phase 2

Agent-role names are owner-defined labels. Authorization never derives from a
name: every grant below is stored as a typed `agent_role_permissions` row and
is rechecked by the controller at execution time.

## Capability catalogue

- `request_more_time`: maximum minutes per request and maximum cumulative
  extension for one assignment.
- `queue_followup`: maximum delay and maximum pending follow-ups. Due work is
  delivered only to an already-running assignment; it never allocates or wakes
  a box and does not change the run budget.
- `create_agent_box`: allowed exact agent types, maximum box count, maximum
  disk per box, and the exact role IDs that may be assigned at creation.
- `create_email_address`: maximum address count plus explicit provider domains
  and address types. Provisioning uses `VMBOX_EMAIL_PROVISION_URL` and optional
  `VMBOX_EMAIL_PROVISION_TOKEN`; when no URL is configured the tool reports
  that fact and does not invent an address.
- `shared_chats`: independent discover, read, subscribe, create, and invite
  grants. Membership is still required to read or post.
- `mcp_tools`: an exact allow-list for optional coordination, secret, and
  desktop tools. Core chat, contact, budget visibility, busy-state, and thread
  history tools remain available. The allow-list is an additional restriction:
  a selected tool still needs its typed capability grant and limits. Optional
  tools default to denied when no assigned role explicitly grants them.

Multiple assigned roles combine additively. Boolean grants are ORed, numeric
limits use the largest explicit ceiling, and allow-lists are unioned.

The local stdio MCP server and authenticated HTTP tool facade both retrieve the
effective policy from the controller for tool discovery and again before tool
execution. Role edits therefore apply without restarting the agent, and an
unavailable policy fails closed rather than exposing the unfiltered inventory.

## Runtime and retries

The default run budget is eight hours and may be configured with
`VMBOX_DEFAULT_RUN_BUDGET` (a Go duration such as `8h`). Its deadline exists
only while the logical box is running; hibernated time does not count. This is
separate from desktop inactivity. Mutation tools require an idempotency key so
a transport retry returns the existing operation instead of repeating it.

## Threading

Every direct and shared-chat message has a persistent message ID, optional
parent ID, and root thread ID. Replies may branch from any message. Incoming
agent context includes the parent and thread IDs, and `get_thread_history`
returns paginated history only after proving the authenticated box can access
the conversation. Upgrades make old messages independent roots; the migration
does not guess links from timestamps or adjacency.

Shared-chat delivery modes are:

- `following`: retain membership and readable history without automatic
  delivery;
- `mentions`: deliver messages containing `@<exact box name>` or `@<box id>`;
- `every_message`: deliver every new message except the sender's own.
