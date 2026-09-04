# Meta-harness reference notes

These notes record patterns reviewed while shaping the vmbox controller as a
meta-harness. The referenced repositories were inspected from temporary clones;
their source is not vendored into vmbox-service.

## TeleCodex

Reference: [benedict2310/telecodex](https://github.com/benedict2310/telecodex),
reviewed at `fd2a24134f0459e15df877bd5c8c7fc7455253fd`.

Useful patterns:

- Give every conversation context a stable key and persist the binding from
  that key to its agent thread, workspace, model, and launch profile.
- Keep busy state per context so one running task does not block unrelated
  work.
- Treat model, reasoning effort, and safety profile as settings for future
  sessions rather than silently mutating an active session.
- Consume structured agent events for response text, tool progress, plans,
  errors, and token use. This is a better long-term chat feed than parsing a
  terminal screen.
- Preserve an explicit hand-back path from a remote UI to the native CLI.

For vmbox, the logical-box ID plus task ID already form the stable context.
The controller database remains the source of truth, while tmux remains the
durable interactive execution surface.

## Anthropic Telegram channel

Reference:
[anthropics/claude-plugins-official/external_plugins/telegram/server.ts](https://github.com/anthropics/claude-plugins-official/blob/main/external_plugins/telegram/server.ts),
reviewed at `1dd9951`.

Useful patterns:

- Apply access checks in both directions: accepting inbound work must not imply
  permission to send output to an arbitrary destination.
- Make group delivery deliberate. Mentions, replies, or explicit destination
  selection are safer than implicit broadcast.
- Write channel state atomically and keep credential-bearing files owner-only.
- Edit an interim progress message, then send a distinct final message so the
  completion generates a notification.
- Tie a channel process to its parent transport and clean up stale pollers
  narrowly, including verifying process identity before terminating anything.

## vmbox design boundary

The controller coordinates durable harnesses; it is not itself an agent. It
may resolve a current deployment endpoint and perform a bounded operation, but
it must not depend on a permanent SSH connection. Agent choice is stored per
logical box, individual sessions may override it, and forwarding names one
destination explicitly with optional appended instructions. Group views are
an observation and coordination surface: they default to no recipients and
show bounded live terminal snapshots that can be opened fullscreen.

Structured agent-event ingestion is the next step for first-class response
bubbles. Until then, the tmux view is the authoritative live output and the
controller's stored messages represent delivery state rather than fabricated
agent replies.
