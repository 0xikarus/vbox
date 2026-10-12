# Tasks API (proposal)

Status: draft for review. Nothing here is implemented yet.

A **task** is a box with one goal. Its agent gets two extra tools,
`completed_task` and `failed_task`. Calling either one records the result,
notifies whoever created the task, hibernates the box and deletes it after the
retention period (24 hours by default). The task record (goal, result, files,
final screenshot, transcript) stays in task history after the box is gone.

While a task runs it is an ordinary box: the owner can watch it, message it,
and control its desktop and terminal.

## Naming

`/v1/tasks/{id}` already exists: it addresses a box's agent session
(`box_tasks`). The new resource therefore lives at **`/v1/goal-tasks`** and is
stored in `goal_tasks`. The UI calls it "Tasks".

## Lifecycle

```
queued ─▶ starting ─▶ running ─┬─▶ completed   (agent: completed_task)
                               ├─▶ failed      (agent: failed_task)
                               ├─▶ timed_out   (time limit reached)
                               ├─▶ abandoned   (agent stopped without either tool)
                               └─▶ cancelled   (creator: cancel)
any finished state ─▶ box hibernated ─▶ box deleted at deleteAt
finished + reopen   ─▶ running        (deleteAt cleared)
finished + keep     ─▶ box becomes a normal box; the task stays in history
```

- **Watchdog.** If the agent is idle or stalled (the #480 activity states) for
  10 minutes without calling either tool, the controller sends one message:
  "Call completed_task or failed_task." After 10 more quiet minutes the task
  becomes `abandoned`. An open `chat_ask` waits for the owner and doesn't count.
- **Time limit.** `timeLimitSeconds` counts running time only. Reaching it
  ends the task as `timed_out`.
- On every finish, the controller captures a final desktop screenshot and the
  terminal text before it hibernates the box.

## Resource

```json
{
  "id": "6f1c…",
  "name": "fix-flaky-test",
  "goal": "Make TestFoo pass reliably and open a PR.",
  "state": "completed",
  "result": {
    "text": "PR #512 opened; 50/50 runs green.",
    "files": [{ "id": "…", "name": "report.md", "mediaType": "text/markdown", "size": 2048 }]
  },
  "failure": null,
  "creator": { "kind": "owner", "id": "…", "name": "owner" },
  "box": { "id": "…", "name": "task-fix-flaky-test", "state": "hibernated", "activity": "hibernated" },
  "agent": "codex",
  "timeLimitSeconds": 7200,
  "retentionSeconds": 86400,
  "createdAt": "…", "startedAt": "…", "finishedAt": "…", "deleteAt": "…", "boxDeletedAt": null,
  "links": {
    "control":    { "url": "https://ctrl.example.com/t/…", "expiresAt": "…" },
    "view":       { "url": "https://ctrl.example.com/t/…", "expiresAt": "…" },
    "screenshot": { "url": "https://ctrl.example.com/v1/task-links/…/screenshot.png", "expiresAt": "…" }
  }
}
```

- `failure` is `{ "message": "...", "reason": "agent|timeout|abandoned|cancelled|box_error" }`.
- `box` becomes `null` after the box is deleted.
- `creator.kind` is `owner`, `user` or `box`.
- `links` carry their full secret URLs only in the response to the call that
  minted them (create, or `POST …/links`). Elsewhere `links` lists only
  `kind`, `expiresAt` and a short id, because the controller stores token
  hashes.

## Owner/user endpoints (Bearer or browser session)

| Method | Path | Purpose |
| --- | --- | --- |
| POST | `/v1/goal-tasks` | Create and start a task. `Idempotency-Key` required. |
| GET | `/v1/goal-tasks` | Task history. `?state=`, `?creator=`, `?limit=`, `?cursor=`. |
| GET | `/v1/goal-tasks/{id}` | One task. `?wait=finished&timeout=60` long-polls until it finishes (max 60 s). |
| GET | `/v1/goal-tasks/{id}/events` | Timeline: state changes, nudges, owner messages, result. `?after=` cursor. |
| GET | `/v1/goal-tasks/{id}/messages` | Chat transcript; archived when the box is deleted. |
| POST | `/v1/goal-tasks/{id}/messages` | Message the agent. On a finished task within retention this reopens it. |
| POST | `/v1/goal-tasks/{id}/cancel` | End as `cancelled`; hibernates the box. |
| POST | `/v1/goal-tasks/{id}/reopen` | Wake the box, clear `deleteAt`, back to `running`. Optional `{ "message": "..." }`. |
| POST | `/v1/goal-tasks/{id}/keep` | Turn the box into a normal box. The task record stays. |
| DELETE | `/v1/goal-tasks/{id}/box` | Delete the box now. |
| DELETE | `/v1/goal-tasks/{id}` | Remove a finished task from history (deletes its box first if needed). |
| GET | `/v1/goal-tasks/{id}/files/{fileId}` | Download a result file. |
| GET | `/v1/goal-tasks/{id}/final-screenshot` | Screenshot captured at finish. |
| POST | `/v1/goal-tasks/{id}/links` | Mint links: `{ "kinds": ["control","view","screenshot"], "ttlSeconds": 86400 }`. |
| DELETE | `/v1/goal-tasks/{id}/links` | Revoke all links (or `?id=` for one). |

### Create request

```json
{
  "name": "fix-flaky-test",
  "goal": "Make TestFoo pass reliably and open a PR.",
  "agent": "codex",
  "loginProfiles": [
    { "application": "codex", "name": "ikarus", "model": "gpt-6-sol", "reasoningEffort": "high" },
    { "application": "github", "name": "0xikarus" }
  ],
  "tools": ["desktop"],
  "instructions": "Optional extra Markdown instructions.",
  "slotId": "…", "diskGiB": 20, "memoryGiB": 4,
  "timeLimitSeconds": 7200,
  "retentionSeconds": 86400,
  "links": { "kinds": ["control", "screenshot"], "ttlSeconds": 86400 },
  "notify": { "webhookUrl": "https://example.com/hooks/vbox" }
}
```

- Required: `name`, `goal` (1–100,000 bytes), `agent`.
- Every other field matches box creation and is optional.
- `timeLimitSeconds`: 300–604,800, default 3,600.
- `retentionSeconds`: 0–604,800, default 86,400. 0 deletes the box right after it finishes.
- `links` is optional; without it no links are minted.
- `notify.webhookUrl` is optional. The controller POSTs the task JSON on finish,
  signed like inbound mail (`X-Vbox-Signature`, `X-Vbox-Timestamp`).

The box's startup instructions are the goal plus a fixed preamble: work until
the goal is done, then call `completed_task`; call `failed_task` only when the
goal is impossible, and say why.

Response: `201` with the task resource, including full link URLs.

## Links

A link is a capability URL scoped to one task's box and one kind. Anyone who
has the URL gets that access, without logging in.

| Kind | URL | Gives |
| --- | --- | --- |
| `control` | `/t/{token}` | Page with the live desktop (noVNC) and tmux terminal, input enabled. |
| `view` | `/t/{token}` | Same page, view-only (noVNC `viewOnly`, terminal read-only). |
| `screenshot` | `/v1/task-links/{token}/screenshot.png` | Current desktop PNG. `?source=terminal` returns the terminal as text/plain. When the box isn't running it returns the final screenshot, with `X-Vbox-Captured-At`. |

Rules:
- **Tokens:** 256-bit random, stored only as SHA-256, compared in constant time.
- **Expiry:** `ttlSeconds` defaults to 24 h and is capped at the task's `deleteAt`, or 7 days while it runs.
- **Revocation:** revoked links fail at once, and open streams are closed.
- **After deletion:** `control`/`view` links stop working when the box is
  deleted. `screenshot` keeps serving the final screenshot until the task
  record is deleted.
- **Page links:** `/t/{token}` exchanges the token for an HttpOnly,
  `SameSite=Strict` cookie limited to `/v1/task-links/` and that box. It then
  removes the token from the address bar, so it doesn't leak through history
  or `Referer`.
- **Scope:** a link never gives access to secrets, browser profiles, mail, files,
  chat, other boxes, or any `/v1/` endpoint outside `/v1/task-links/`.
- **Sleeping box:** a link never wakes a hibernated box; the page shows the final
  screenshot and state instead.
- **Audit:** each link use is written to `audit_log` (kind, link id, IP).

Endpoints behind the link cookie:
`GET /v1/task-links/desktop/stream`, `GET /v1/task-links/terminal/stream`,
`GET /v1/task-links/state`.

## Agent tools (task boxes only)

| Tool | Arguments | Effect |
| --- | --- | --- |
| `completed_task` | `text` (1–20,000), optional `files` (≤ 8 workspace paths, ≤ 25 MiB each) | State `completed`; files copied into the task record. |
| `failed_task` | `message` (1–20,000) | State `failed`, reason `agent`. |

Both tools then: stop the agent, capture the final screenshot, notify the
creator, hibernate the box and set `deleteAt`. Repeated calls return the
existing result. Worker side, they call `POST /v1/agent-desktop/goal-task/complete`
and `…/fail` with the box's desktop-agent auth.

## Tasks created by boxes

There is no new MCP tool. `create_agent_box` takes an optional `task` object:

```json
{ "name": "…", "agent": "codex", "idempotencyKey": "…",
  "task": { "goal": "…", "timeLimitSeconds": 3600, "retentionSeconds": 86400, "links": { "kinds": ["screenshot"] } } }
```

- The same create grant and limits apply (`maxBoxes`, `maxDiskGiB`, allowed agents).
- A box can mint `control`/`view` links only with the `control` grant, and
  `screenshot` links only with `inspect`.
- The creator box is a contact of the task box, as with normal creation. On
  finish, it gets a chat message:
  `[Task completed] fix-flaky-test: PR #512 opened…` or `[Task failed] …`.
- `get_agent_box` on a task box adds a `task` field with the state and result.

## Notifications

- Owner-created: chat/push notification "Task completed: name" with the
  result text; it opens the task page.
- Box-created: the chat message above to the creator box. The owner sees it in
  task history.
- With `notify.webhookUrl`, the webhook is sent in addition to these.

## Storage

- `goal_tasks`: id, account, creator (kind, id), logical_box_id (nullable,
  `ON DELETE SET NULL`), name, goal, settings, state, result_text,
  failure_reason, failure_message, final_screenshot, timestamps.
- `goal_task_files`, `goal_task_links` (token_hash, kind, expires_at,
  revoked_at), `goal_task_events`.
- `goal_task_transcripts` holds a text copy of the box chat, taken when the
  box is deleted, because `box_messages` is removed with the box.

## Build order

1. Backend: tables, create/list/get/cancel/reopen/keep, the two agent tools
   (needs a worker update), watchdog, time limit, retention sweeper.
2. Links: `/t/{token}` page and the stream, screenshot and state endpoints.
3. UI: Tasks list and task page (state, result, files, final screenshot,
   transcript, Reopen/Keep/Delete now), owner notifications.
4. `create_agent_box` `task` field, the message to the creator box, webhooks.
