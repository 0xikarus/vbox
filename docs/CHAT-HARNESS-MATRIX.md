# Managed chat harness matrix

Updated 2026-09-28 against the working tree. This is an implementation map for
managed Codex, Claude Code, and OpenCode conversations. The controller persists
chat messages and sends a JSON envelope to `vmbox-runtime` over the **runtime
command's stdin**. That transport is not a prompt sent to an agent's stdin.
The runtime stages an inbox event; native delivery and acknowledgement then
depend on the harness. The controller's `queued`, `delivering`, `ambiguous`, and
`delivered` message states are separate from each client's pending-turn state.

Scope: managed Agent chat, its tools, and the box lifecycle. A user who opens
a shell and runs `codex`, `claude`, or `opencode` manually is using that native
CLI outside the managed conversation adapter. `shell` is also a supported
interactive and one-shot choice, but has no managed agent chat/MCP reply loop.

## Inbound messages and scheduling

| Concern | Codex | Claude Code | OpenCode |
| --- | --- | --- | --- |
| Managed visible client | `codex --remote` TUI connected through a WebSocket proxy to its companion app-server | Claude TUI with the experimental `vmbox-desktop` channel | `opencode --auto` TUI with a pane-local `vmbox-visible-chat` plugin and loopback server |
| First message | Starts a bare visible TUI, waits for its selected zero-turn thread through the proxy, then submits text and images through the same app-server queue used for follow-ups | Starts the TUI, waits for the channel owner, then sends a channel notification | Starts a bare TUI; the plugin creates a visible session from Home and calls `session.promptAsync` with structured parts |
| Follow-up transport | WebSocket app-server `turn/steer` on the visible thread while a turn is running; `thread/queue/add` when idle or when steering is definitively unavailable | `notifications/claude/channel` on the live MCP connection | HTTP to the current TUI plugin socket, then `session.promptAsync` on its visible session |
| While the agent is busy | `turn/steer` sends the input to the active turn with its exact turn ID and chat message ID; Codex consumes it at a step boundary. A non-steerable turn falls back to the native queue, whose oldest item is started when idle if necessary | Channel notification is sent to the live client; Claude can record a busy input as a native `queued_command` attachment for later processing | `promptAsync` submits to the current native session; the bridge waits for a user item and does not claim completion or ordering of the agent turn |
| Visible conversation identity | Proxy binds successful persistent `thread/start` and `thread/resume` replies and ignores auxiliary ephemeral threads | Channel readiness is tied to the current managed pane process and MCP connection owner | Plugin reads `api.route.current`; runtime checks pane, process tree, plugin instance, and session before accepting its receipt |
| Native acceptance receipt | Matching `userMessage.clientId` in `thread/items/list`, with history lookup for reconciliation | Matching Claude JSONL `user` record or channel `queued_command` attachment with chat and message IDs | Matching native user text and file-part count in the visible session, plus unchanged bridge instance/session |
| Pending or interrupted handoff | Neither a queue ID nor a steer response alone counts as delivery; wait for the matching user item. An uncertain handoff is checked without resubmitting it | Keep the inbox until transcript receipt; inspect each pending event independently. The checked-in code re-emits after 30 seconds without receipt; the current uncommitted change emits once per live MCP connection and retries after reconnection | Keep inbox and pending marker; retry probes the native transcript without a second `promptAsync` |
| Incoming images | Initial and follow-up messages use structured `localImage`, with compatibility fallback | Channel metadata includes first `image_path` and JSON `image_paths`; model consumption of multiple images still needs verification | Native `file` parts with image data URLs |
| TUI exit and reopen | Clears proxy binding when the TUI connection ends; new TUI binds its own thread | Removes the connection owner marker; new delivery waits for a new channel | Disposes pane socket; a replacement plugin must pass process and instance checks |

`delivered` means the native client recorded the user input. It does not mean the
agent finished the turn or sent a reply. `chat_message` and `chat_ask` are
separate outbound MCP calls. A missing native receipt leaves delivery ambiguous;
the controller must not infer success from a worker command exit, Codex queue ID,
Claude notification write, or OpenCode `promptAsync` return alone.

## Conversation lifecycle

| Feature | Codex | Claude Code | OpenCode |
| --- | --- | --- | --- |
| Clear context | Attaches a fresh app-server thread to the visible pane | Respawns Claude without `--resume` and waits for a new channel owner | Sends native `/new` in the TUI |
| Restore saved conversation after hibernation | Offers the snapshotted visible thread; `thread/resume` and the visible TUI attach to it | Offers the captured channel session ID or eligible workspace transcript; relaunches with `--resume` | Offers the captured visible session or eligible saved session; relaunches with `--session` |
| Automatic wake behavior | Recreates the managed app-server/proxy and TUI; old process IDs do not survive | Recreates the managed TUI and channel | Recreates the managed TUI, loopback server, and plugin socket |
| New message after wake | Commits to a fresh conversation and suppresses the saved-thread offer | Same | Same |
| Ongoing terminal view | Agent exchange remains in the managed tmux pane and workspace viewer | Same | Same |
| Local app prompt | Box-local authenticated `POST /prompt` feeds the running conversation through the same harness delivery path | Same | Same |
| One-shot task | `codex exec` | `claude -p` | `opencode run --auto` |

The controller offers a saved conversation only after checking the hibernation
snapshot, the selected managed session, and whether a newer owner/box message
was sent. Restoring a tmux pane runs `vmbox-runtime agent-restore`, which
rebuilds the current backend and registration before launching the agent. A
profile replacement invalidates the old managed agent sessions and saved
snapshot entries; shell and desktop sessions survive. One-shot tasks run in
separate processes with an exit journal and do not reuse the persistent Agent
chat conversation.

## Profiles, models, installation, and permissions

| Feature | Codex | Claude Code | OpenCode |
| --- | --- | --- | --- |
| Portable login files | `.codex/auth.json` and `.codex/config.toml` | `.claude/.credentials.json`, `.claude/settings.json`, and `.claude.json` | OpenCode auth plus `opencode.json` or `opencode.jsonc` |
| Provisioning check | Login status and a bounded `codex exec` provider request | `claude auth status --json`; a `claude -p` completion is not a creation gate | Auth listing and a bounded `opencode run --auto` provider request |
| Live model picker | Codex app-server with the saved login | Anthropic Models API with the saved login; availability is checked when Claude uses the model | OpenRouter or Venice catalog for supported saved API-key profiles; tool-capable text models |
| Box-specific model | Writes `model` in a copy of the saved `config.toml` | Writes `model` in a copy of `settings.json` | Writes `model` in a copy of OpenCode configuration |
| Box-specific reasoning | Writes `model_reasoning_effort` | Writes `effortLevel`; Haiku has no effort choice, and session-only `max` is not a persistent setting | Writes the selected Build-agent model `variant`; support depends on that provider/model |
| Managed instructions | Snapshot linked to `~/.codex/AGENTS.md` | Snapshot linked to `~/.claude/CLAUDE.md` | Snapshot linked to `~/.config/opencode/AGENTS.md` |
| Managed permissions/trust | Config uses `approval_policy = "never"`, `sandbox_mode = "danger-full-access"`, and workspace trust | Settings use `bypassPermissions`, trusted workspace, and disable the alternate auto-mode prompt | Managed TUI and one-shot use `--auto`; configuration defaults to allow only if no permission field already exists, preserving explicit deny rules |
| Exact CLI version at creation | Installs selected `@openai/codex` release in persistent home | Installs selected `@anthropic-ai/claude-code` release | Installs selected `opencode-ai` release |
| Saved-profile usage | Codex app-server reports ChatGPT quota windows | Claude `/usage` probe reports session and weekly windows | OpenRouter spending/free-model requests or Venice balances and rates, according to profile |

The controller encrypts saved profiles, imports at most one agent profile per
box, and applies per-box model/effort settings only to the box's copied files.
That profile chooses the managed harness. Owners can explicitly replace it;
this closes old managed sessions and selects the new harness for the next chat.
Usage polling prefers a running box's imported credential copy and otherwise
checks a saved profile in a temporary home without waking a box. Managed
instruction snapshots are applied again on attach and restore; an already
running agent process does not reload them automatically.

## MCP, replies, and tool policy

| Concern | Codex | Claude Code | OpenCode |
| --- | --- | --- | --- |
| `vmbox-desktop` registration | `codex mcp add`; explicitly supplies workspace/tmux environment because Codex sanitizes MCP child environment | `claude mcp add --scope user` | Local MCP entry merged into OpenCode JSON, with the visible TUI plugin registered separately |
| Incoming chat through MCP | No; app-server queue is the follow-up path | Yes; `notifications/claude/channel` uses this connection | No; visible TUI plugin is the inbound path |
| MCP tool calls | Local stdio JSON-RPC server | Same stdio server, also carrying its inbound channel | Same local MCP server through OpenCode configuration |
| Outbound replies and choices | `chat_message` / `chat_ask` | Same | Same |
| Outbound event acknowledgement | Runtime writes a durable outbox event, tries a scoped controller callback, and removes the event after controller storage; pull/ack remains a fallback | Same | Same |
| Browser/terminal output | Managed tmux is the live presentation. Chat bubbles come from structured MCP events, not terminal scraping | Same | Same |
| Box-local scripts | Authenticated HTTP façade exposes the allowed tools and `POST /prompt` for a running conversation | Same | Same |

The server defines **26 possible tools**, and the current controller policy
always includes the six basic tools:

| Group | Tool names |
| --- | --- |
| Basic chat/history/budget (6) | `get_contacts`, `get_run_budget`, `get_thread_history`, `set_busy`, `chat_message`, `chat_ask` |
| Box management (11) | `list_agent_boxes`, `get_agent_box`, `create_agent_box`, `get_agent_box_configs`, `get_available_workers`, `set_agent_box_tags`, `restart_agent_box`, `wake_agent_box`, `clear_agent_box_context`, `compact_agent_box_context`, `delete_agent_box` |
| Secrets (3) | `secret_request`, `generate_password`, `type_secret` |
| Computer (8) | `take_screenshot`, `capture_window`, `move_mouse`, `click_mouse`, `drag_mouse`, `scroll_mouse`, `type_text`, `press_keys` |

The other 20 tools require an owner-managed allow-list and applicable role
capability. The list and each call are checked against current policy; MCP
clients can receive a tool-list change notification. The box-local HTTP façade
uses the same policy and a private bearer token. `chat_message` may link to an
owner message with `replyTo`, send a standalone message, attach image files,
or route to an authorized box contact. `chat_ask` sends a structured choice
request. Submitted messages set controller busy state; linked replies/questions
clear the matching activity, and `set_busy` can override activity outside that
flow. A late reply cannot clear a newer message's busy state.

## Other features that can reach a harness

| Feature | Shared behavior and boundary |
| --- | --- |
| Direct contacts | Owner-managed directional grants and protection checks gate `get_contacts` and `chat_message`/`chat_ask`. Target delivery enters its existing native conversation; no second target session is created. Contact images use the same inbound image path. |
| Shared chats | Agent-authenticated controller HTTP endpoints manage discovery, membership, subscriptions, and messages. A queued group delivery routes a box message through the target's normal native conversation path. These coordination operations are not advertised MCP tools. |
| Delayed follow-ups | Agent-authenticated HTTP request stores a due message bound to one active non-shell task. Reconciliation creates a normal queued box message, which then uses that task's native harness path. This is distinct from Claude's native busy `queued_command`. |
| Run-time extension and email | Agent-authenticated HTTP endpoints implement role-gated time extension and email creation. `get_run_budget` is MCP, but `request_more_time`, `queue_followup`, shared-chat administration, and `create_email_address` are retired MCP tool names. |
| Agent-initiated box management | MCP box tools call authorized controller routes. `create_agent_box` chooses Codex, Claude, or OpenCode, profile/model/effort, roles, instructions, and optional tool presets; `wake_agent_box` uses the existing allocation path for hibernated boxes; `clear_agent_box_context` uses the target's normal context-clear path. |
| Chat UI and attachments | The PWA stores and displays messages, attached images, choices, and busy state independently of the client. Its composer and writing helper prepare text before submission; the helper is not a fourth managed harness. |
| Desktop and secrets | The managed session remains visible through tmux and, when available, a desktop terminal. MCP screenshot/input/secret tools act on the box desktop through shared code and policy; opening a viewer does not submit a prompt. |
| Optional tooling | The Blender preset registers its separate MCP bridge with the clients where configured. Foundry/custom setup runs inside the worker before tasks and is restored as applicable; neither changes the native chat transport. |
| One-shot execution | Codex `exec`, Claude `-p`, and OpenCode `run --auto` start separate journaled processes. Their output/exit state is not a reply from the managed Agent chat session. |
| Automatic idle and run-time limit | Controller box lifecycle can hibernate any of the three; the agent process ends, the volume and chat history persist, and the saved-conversation offer follows the harness-specific recovery check above. |

The retired coordination tool names are recognized while editing older role
data but are not granted as MCP tools. Scheduled box execution is currently a
proposal, not an implemented harness feature. Claude's native `queued_command`
is a pending input within Claude, not scheduled box execution.

The MCP tool server handles **agent-to-controller calls** for all three clients.
Only Claude also uses that MCP connection as the native **controller-to-agent
message channel**. Codex receives follow-ups through its app-server; OpenCode
through its visible TUI plugin. The shared box-local HTTP façade is for scripts
and does not replace any client's native conversation transport.

## Implementation file map

These are the current entry points. There is **no single wrapper file per
harness**: `chat.go`, `interaction.go`, and `interactive.go` still mix the
dispatch and some client-specific logic. A future per-harness adapter could own
Start/Deliver/Confirm/Reset/Restore while keeping the transport files separate.

| Area | Files |
| --- | --- |
| Shared controller dispatch, message states, and native receipt reconciliation | [`internal/controller/interaction.go`](../internal/controller/interaction.go), [`interaction_store.go`](../internal/controller/interaction_store.go), [`structured_chat.go`](../internal/controller/structured_chat.go), [`interaction_replies.go`](../internal/controller/interaction_replies.go) |
| Runtime commands, inbox, launch, reset, tmux snapshot | [`cmd/vmbox-runtime/interaction.go`](../cmd/vmbox-runtime/interaction.go), [`internal/boxruntime/chat.go`](../internal/boxruntime/chat.go), [`interactive.go`](../internal/boxruntime/interactive.go), [`interaction.go`](../internal/boxruntime/interaction.go), [`tmux.go`](../internal/boxruntime/tmux.go), [`managed_resume.go`](../internal/boxruntime/managed_resume.go) |
| Codex | [`codex_appserver.go`](../internal/boxruntime/codex_appserver.go) (queue/receipt/backend), [`codex_tui_proxy.go`](../internal/boxruntime/codex_tui_proxy.go) (visible thread), [`codex_resume.go`](../internal/boxruntime/codex_resume.go), [`codex_config.go`](../internal/boxruntime/codex_config.go); bare TUI startup and first queue submission are in [`chat.go`](../internal/boxruntime/chat.go) |
| Claude Code | [`desktop_mcp.go`](../internal/boxruntime/desktop_mcp.go) (channel), [`claude_chat.go`](../internal/boxruntime/claude_chat.go) (native receipt), [`claude_resume.go`](../internal/boxruntime/claude_resume.go), [`agent_defaults.go`](../internal/boxruntime/agent_defaults.go); reset is in [`interaction.go`](../internal/boxruntime/interaction.go) |
| OpenCode | [`opencode_tui_plugin.mjs`](../internal/boxruntime/opencode_tui_plugin.mjs) (native prompt), [`opencode_tui.go`](../internal/boxruntime/opencode_tui.go) (pane/socket), [`opencode_resume.go`](../internal/boxruntime/opencode_resume.go); Go-side send/receipt is in [`chat.go`](../internal/boxruntime/chat.go) |
| Shared MCP tools, policy, HTTP access, desktop | [`desktop_mcp.go`](../internal/boxruntime/desktop_mcp.go), [`desktop_registration.go`](../internal/boxruntime/desktop_registration.go), [`desktop_mcp_http.go`](../internal/boxruntime/desktop_mcp_http.go), [`internal/controller/agent_tool_policy.go`](../internal/controller/agent_tool_policy.go), [`desktop_terminal.go`](../internal/boxruntime/desktop_terminal.go) |
| Profiles, model/effort, CLI version, instructions, usage | [`internal/controller/login_profile_provision.go`](../internal/controller/login_profile_provision.go), [`login_profile_verify.go`](../internal/controller/login_profile_verify.go), [`profile_models.go`](../internal/controller/profile_models.go), [`profile_usage.go`](../internal/controller/profile_usage.go), [`internal/loginprofile/model.go`](../internal/loginprofile/model.go), [`internal/boxruntime/agent_cli_install.go`](../internal/boxruntime/agent_cli_install.go), [`instructions.go`](../internal/boxruntime/instructions.go) |
| Box coordination and lifecycle | [`internal/controller/agent_followups.go`](../internal/controller/agent_followups.go), [`agent_shared_chats.go`](../internal/controller/agent_shared_chats.go), [`management.go`](../internal/controller/management.go), [`agent_box_creation.go`](../internal/controller/agent_box_creation.go), [`agent_box_management.go`](../internal/controller/agent_box_management.go), [`agent_runtime_budget.go`](../internal/controller/agent_runtime_budget.go), [`codex_resume.go`](../internal/controller/codex_resume.go) (shared resume handler despite name) |

## Remaining verification

- Exercise busy-turn delivery, context clear, TUI reopen, and restore across
  supported client versions using isolated disposable boxes.
- Verify native handling of multiple Claude image paths. Codex and OpenCode
  submit each image as a native image or file part.
