# Earlier coworker MCP: current status

The coworker feature is **removed from main**, not merely hidden in the UI.
Telegram was removed in `969d18b`; the remaining coworker runtime, endpoints,
adapters and controls were removed in `15e6ac5`, at the owner's request.
Nothing has been re-enabled as part of the web-workspace testing.

## What the last implementation did

The controller was the shared coordination service. Each explicitly enrolled box
received a private, per-box token; both account-level and box-level opt-in were
required. Boxes did not receive the owner's controller credentials.

| MCP tool | Purpose |
| --- | --- |
| `coworkers_list` | List running, opted-in coworkers within the same account. |
| `message_send` | Durable text delivery to a box, with a retry/idempotency key. |
| `board_read` | Read the shared JSON Kanban and its revision. |
| `board_edit` | Create, move, assign or comment on a task; reject stale revisions. |
| `hibernate_self` | Queue hibernation of the caller's own box after completion; retain its volume. |

The board supported `todo`, `doing`, and `done`, with box-attributed comments.
It was deliberately small: at most 100 tasks and 100 comments per task.
Messages were treated as untrusted coworker input, not owner instructions.

Inbound delivery had two adapters:

- **Codex:** a managed App Server conversation/turn loop.
- **Claude:** an MCP channel adapter, requiring explicit consent to Claude's
  development-channel mode.

The event inbox was persisted in the controller database and polled by adapters;
this was not a peer-to-peer network or a generic arbitrary-command execution API.
The agent still needed an active, authenticated process to respond. Adding MCP
tools alone would not turn ordinary idle shell boxes into responding agents.

## What remains

- Historical code is recoverable from the parent of `15e6ac5`.
- Database tables remain to preserve history and support safe cleanup. Current
  migrations disable enrollment and erase recoverable coworker tokens.
- Normal multi-box shells, saved login profiles, one-shot process tasks and
  controller-managed hibernation remain supported independently.

This is a source/history overview, **not a new live validation** of the removed
adapters. If restored, start with an explicit opt-in gate, durable messages and
the small board; validate each agent adapter again against installed versions.
Keep Telegram separate, and do not silently enable autonomous spawning.
