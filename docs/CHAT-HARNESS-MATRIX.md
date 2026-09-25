# Native chat delivery by harness

Recorded 2026-09-25. This compares the current implementation paths. A transport response and a native user item are separate evidence; the verification column states what has actually been exercised.

| Concern | Codex | OpenCode | Claude |
| --- | --- | --- | --- |
| Native inbound path | Controller → runtime inbox → app-server `thread/queue/add` on the TUI-selected thread | Controller → runtime inbox → pane-specific `vmbox-visible-chat` socket → `session.promptAsync` | Controller → runtime inbox → `notifications/claude/channel` on the MCP connection |
| First message | CLI prompt and native `-i` images on TUI startup; a fresh empty TUI can receive the first text through its composer | TUI bridge creates a visible session from Home and submits structured parts | Launches managed TUI and waits for a new channel owner before delivery |
| Follow-up thread selection | TUI WebSocket proxy observes successful persistent `thread/start` and `thread/resume` replies; ignores auxiliary ephemeral threads | Plugin reads `api.route.current` at submission time | Active channel belongs to the managed MCP process; readiness uses an owner marker |
| TUI closes and reopens | Proxy clears its thread binding when all TUI connections close; the reopened TUI binds from its own start or resume reply | Pane socket is removed on plugin disposal; stale sockets are unlinked when unreachable | Channel owner file is removed when the MCP connection closes; restart waits for a new owner |
| Images into native client | `localImage` structured item with a compatible data URL fallback; initial images use Codex `-i` | Native `file` part with a data URL | Channel metadata carries one `image_path`; multiple image parts and native visual consumption need verification |
| Delivery acknowledgement | Follow-ups poll `thread/items/list` for a `userMessage.clientId` equal to the controller message ID; an unconfirmed queue is ambiguous | Successful `/prompt` response reports a session ID after `promptAsync`; no matching native item acknowledgement yet | Successful notification write removes the inbox event; no native consumption acknowledgement yet |
| Outbound replies | `vmbox-desktop` MCP `chat_message` / `chat_ask` | Same MCP tools | Same MCP tools over the Claude channel MCP connection |
| Saved conversation recovery | `/resume` and explicit `codex resume` select a thread through the proxy; the Chat restore offer is gated by the hibernation snapshot and newer messages | TUI/plugin lifecycle retains its own session state; explicit restore behavior needs testing | Claude TUI uses its own session recovery; channel reattachment needs testing |
| Verification | Isolated Codex 0.156.1 app server and TUI: start, `/new`, explicit resume and reopen; text and native image user items observed. Isolated production-version Codex 0.155.1: ephemeral side thread reproduced; corrected proxy kept the persistent ID through a turn and rebound after reopen. Disposable production box: first and follow-up messages, PNG visual input, `/new`, manual `/resume`, and TUI close/reopen all reached the visible conversation, with linked MCP replies. | Code and existing tests inspected; disposable live lifecycle test pending. | Code and existing tests inspected; disposable live lifecycle test pending. |

## Gaps that apply across harnesses

1. Confirm the current managed pane and conversation before reporting delivery. Codex now binds to TUI requests; OpenCode uses its pane socket; Claude still needs a consumption check tied to the current channel owner.
2. Preserve an inbound envelope until the native client records the exact message ID. Codex follow-ups now use a matching user item; OpenCode and Claude still rely on transport-level acknowledgements.
3. Test initial and busy-turn messages, TUI close/reopen, `/new` or equivalent, box restore, and outbound MCP replies on disposable boxes for all three harnesses.
4. Verify native handling of all image parts. Codex and OpenCode submit structured image/file parts. Claude currently supplies only the first image path as channel metadata.

See [CHAT-DELIVERY-RESILIENCE-TODO.md](CHAT-DELIVERY-RESILIENCE-TODO.md) for the remaining work and failure-recovery cases.
