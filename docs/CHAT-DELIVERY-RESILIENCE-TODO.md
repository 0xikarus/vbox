# Agent chat delivery across TUI restarts

Status: Codex correction deployed and verified on a disposable production box; cross-harness lifecycle work remains. Recorded 2026-09-25.

## Required behavior

- An owner or contact message reaches the **visible native conversation** for the selected box. Its message ID stays associated with that conversation across TUI close, reopen, context clear, and box restore.
- The chat's delivered checkmarks mean the native client accepted the input for processing. A worker command returning successfully or an app server returning a queue ID is an earlier state.
- Codex, OpenCode, and Claude can call `vmbox-desktop` MCP tools after each new TUI starts. The box-side HTTP tool facade stays available to scripts independently of the TUI; its `/prompt` route targets only a live managed conversation.
- Incoming images retain native image/file parts for each harness. Codex image delivery must **not** be replaced with a prompt containing local file paths. Outbound `chat_message` image attachments and contact routing continue to work.
- An interrupted or uncertain handoff remains inspectable and recoverable by message ID. Automatic retries must not duplicate an input that the native client already accepted.

## Evidence and likely regression

- On `projectmanager`, six owner messages on 2026-09-25 between 17:03 and 17:17 UTC were marked `delivered` without linked replies. Their chat references were absent from the Codex tmux pane. The Codex task and pane were live.
- After the owner restarted `projectmanager`, the tmux incarnation changed. An explicitly authorized diagnostic message at 17:34 UTC (`07c4dc88-503f-4878-896c-30b200000000`) was also marked `delivered`, but its text and reference were absent from the new pane and no linked reply appeared. Earlier delivered messages were not replayed.
- `vmboxdev` showed the same pattern: an earlier message appeared in its Codex pane, while several later messages were marked delivered without their references appearing there. `mapeditor` had no active chat task during this check.
- Commit `63963c3` changed Codex follow-ups from visible TUI submission to `thread/queue/add`. The earlier delivery path removed the inbox event after `queuedSubmission.id` was returned. That response proved queue acceptance, not that the visible TUI consumed the message.
- An isolated Codex 0.155.1 TUI reproduced the thread mismatch. Its visible conversation was persistent, but a later `thread/start` response on the same TUI connection described an auxiliary ephemeral thread. The proxy selected that later ID; `thread/queue/add` rejected it with `ephemeral thread does not support queued submissions`. Queueing the same diagnostic to the persistent thread reached the visible TUI and produced a native `userMessage` with the exact client message ID. The proxy now ignores ephemeral thread responses, and the Codex path waits for a matching native user item before confirming delivery.
- Deployment `58e59777-072c-41c4-bc14-96ffd9ed50fd` succeeded on `vmbox-controller`. The disposable `codex-delivery-fixed-20260925` box received an initial message, follow-up, PNG image, a message after `/new`, one after manual `/resume` to its earlier conversation, and one after `/quit` plus a new managed task. Every diagnostic appeared in its visible Codex pane, was marked delivered, and received an MCP reply linked to the matching user message. The PNG appeared as `[Image #1]`; Codex identified its red content. This verifies Codex 0.155.1 on the production runtime, not OpenCode or Claude.
- A later GitHub `main` auto deployment briefly replaced the controller image with a revision that did not contain the local Codex fix. The fixed code was merged with that latest `main` and explicitly redeployed as `d0473d7d-b3d7-446e-b811-fda1a1951e0f`. Existing `vmboxdev` and `projectmanager` boxes were hibernated and resumed with their volumes retained to replace old TUI/proxy processes. On each restored box, a fresh message and a follow-up appeared in its visible Codex pane and received an MCP reply linked to the exact user message. The earlier failed or ambiguous diagnostics were not automatically replayed.

## Implementation TODO

### Shared delivery contract

- [ ] Define separate states or acknowledgements for controller receipt, worker persistence, native client acceptance, and agent reply. Map checkmarks to the documented state.
- [ ] Fence every inbound message to the current box assignment, managed task, TUI incarnation, and native conversation identity. Invalidate stale bindings when a TUI closes or reopens, even if its tmux session name is reused.
- [ ] Keep the inbound envelope durable until the native client acknowledges that exact message ID. Reconcile uncertain submissions after controller or worker interruption without blind replay.
- [ ] Make active-session discovery fail clearly when more than one native conversation could receive the message. Do not infer the visible conversation from a global recency list alone.
- [ ] Verify that `chat_message` and `chat_ask` still publish from a reopened TUI, and that replies attach to the correct `replyTo` or contact conversation.

### Codex

- [x] Reproduce the wrong-thread delivery failure on an isolated Codex 0.155.1 TUI without logging credentials. The selected ephemeral ID rejected the queued submission; the persistent visible ID received it.
- [x] Keep the native structured queue path for visible persistent threads, and require a matching native user item before confirming delivery. Local text and image inputs were observed as native items on Codex 0.156.1; text and exact client ID were observed on 0.155.1.
- [ ] Rebind after TUI restart, app server restart, `/new`, context clear, and box restore. Production verified `/new`, manual `/resume`, and TUI close/reopen on a disposable box; hibernate/restore was verified on `vmboxdev` and `projectmanager`. A standalone app-server restart and context clear remain to test. Check the current pane incarnation and thread identity before accepting the next message; do not reuse a saved thread solely because it is newest in `thread/list`.
- [ ] Preserve Codex's native image attachment behavior for the first message and follow-ups. Test PNG, JPEG, and GIF with multiple images, and verify the visible conversation receives image input rather than file path instructions.
- [ ] Confirm that the new Codex process registers `vmbox-desktop` MCP and can call `chat_message` after each lifecycle transition. Production linked replies succeeded after first launch, `/new`, manual `/resume`, TUI close/reopen, and box restore; standalone app-server restart and context clear remain to test.

### OpenCode

- [ ] Verify the `vmbox-visible-chat` plugin socket belongs to the current tmux pane and visible OpenCode session after close/reopen. Reject or clean stale sockets and retry only after the replacement bridge is healthy.
- [ ] Confirm `/prompt` accepts the exact message ID and structured text/image parts in the visible session; a successful HTTP response must be tied to that session rather than merely to a reachable socket.
- [ ] Recheck MCP registration and outbound `chat_message`/`chat_ask` after plugin reload, TUI restart, context clear, and box restore.

### Claude

- [ ] Fence `claude/channel` readiness markers to the live MCP connection and Claude process. Remove stale owners when the TUI closes, then wait for a new channel before delivery.
- [ ] A successful write of `notifications/claude/channel` is a transport acknowledgement. Establish how to confirm that the new Claude process consumed the message before marking it delivered or removing its durable inbox event.
- [ ] Verify channel and MCP tool registration after Claude respawn, context clear, and box restore, including image metadata and contact replies.

## Verification

- [ ] Add focused tests for stale TUI identities, queue acknowledgement without consumption, interrupted handoffs, duplicate prevention, and MCP reconnects. Tests that create sessions or boxes use isolated, explicitly named disposable resources.
- [ ] On one disposable box per harness, send an initial message and follow-ups while idle and busy; verify the input in the visible TUI, the matching native acknowledgement, and a linked MCP reply. Repeat after closing and reopening the TUI, clearing context, and restoring the box.
- [ ] Exercise native image attachments on each harness and confirm they are visible as image inputs. Check that outgoing images, choices, and box-to-box contacts still work.
- [ ] Compare checkmarks with the native acknowledgement and inject a failure between worker persistence and native consumption. Confirm that the UI shows an uncertain state and that recovery does not duplicate the message.
- [ ] Stage and review a controller/runtime rollout separately. Do not restart existing user workers or delete their volumes as part of verification.

Entry points: `internal/controller/interaction.go`, `internal/controller/interaction_store.go`, `internal/boxruntime/chat.go`, `internal/boxruntime/codex_appserver.go`, `internal/boxruntime/desktop_mcp.go`, `internal/boxruntime/desktop_mcp_http.go`, `internal/boxruntime/interactive.go`, and `internal/boxruntime/opencode_tui_plugin.mjs`.
