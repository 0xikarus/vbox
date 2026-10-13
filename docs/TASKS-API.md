# Tasks API (proposal)

Status: draft for review. Nothing here is implemented yet.

A **task** is a temporary box with one goal. Its agent gets two extra tools,
`completed_task` and `failed_task`. Calling either one records the result,
notifies whoever created the task and hibernates the box. After the retention
period (24 hours by default), the controller deletes both the box and the task
record, including its results. A finished task cannot be reopened: follow-up
work creates a new task with a new box.

While a task runs it is an ordinary box: the owner can watch it, message it,
and control its desktop and terminal.

## Naming

The resource lives at **`/v1/tasks`** and is stored in `goal_tasks`. The old
`/v1/tasks/{id}` and `/v1/logical-boxes/{id}/tasks` routes, which addressed a
box's agent session, are removed first (#494). The internal `box_tasks` table
keeps running agent sessions and is unrelated to this API.

## Lifecycle

```
queued ─▶ starting ─▶ running ─┬─▶ completed   (agent: completed_task)
                               ├─▶ failed      (agent: failed_task)
                               ├─▶ timed_out   (time limit reached)
                               ├─▶ abandoned   (agent stopped without either tool)
                               └─▶ cancelled   (creator: cancel)
any finished state ─▶ box hibernated ─▶ box and task deleted at deleteAt
```

- **Watchdog.** If the agent is idle or stalled (the #480 activity states) for
  10 minutes without calling either tool, the controller sends one message:
  "Call completed_task or failed_task." After 10 more quiet minutes the task
  becomes `abandoned`. An open `chat_ask` waits for the owner and doesn't count.
- **Time limit.** `timeLimitSeconds` counts running time only. Reaching it
  ends the task as `timed_out`.
- On every finish, the controller captures a final desktop screenshot and the
  terminal text before it hibernates the box.
- Finishing a task is final. Messages to a finished task return `409` rather
  than waking its box. A new goal, retry or follow-up is a new task. The task
  and its saved results remain readable only until `deleteAt`.
- Cleanup is retried if hibernation or deletion fails. The task remains visible
  with its finished outcome while cleanup is pending; deletion of the task
  record follows successful box deletion.
- After deletion, task reads return `404`; there is no permanent task history.

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
- `creator.kind` is `owner`, `user`, `service` or `box`.
- `links` carry their full secret URLs only in the response to the call that
  minted them (create, or `POST …/links`). Elsewhere `links` lists only
  `kind`, `expiresAt` and a short id, because the controller stores token
  hashes.

## Task API endpoints (scoped Bearer or browser session)

Third-party services use a task-scoped bearer credential. It may create and
manage only tasks created with that credential and list only the login profiles
the owner has allowed for it. Its task creator has `kind: "service"` and the
credential's ID. It cannot call the owner-only `/v1/login-profiles` API or mint
control links unless the owner grants that separate capability. Browser
sessions retain their existing account and role checks.

| Method | Path | Purpose |
| --- | --- | --- |
| GET | `/v1/tasks/profiles` | List selectable saved login profiles using opaque IDs and safe labels. |
| POST | `/v1/tasks` | Create and start a task. `Idempotency-Key` required. |
| GET | `/v1/tasks` | Recent tasks retained until deletion. `?state=`, `?creator=`, `?limit=`, `?cursor=`. |
| GET | `/v1/tasks/{id}` | Return the current task state and any result immediately. |
| GET | `/v1/tasks/{id}/events` | Timeline: state changes, nudges, owner messages, result. `?after=` cursor. |
| GET | `/v1/tasks/{id}/messages` | Chat transcript while the task is retained. |
| POST | `/v1/tasks/{id}/messages` | Message a running task's agent; `409` after finish. |
| POST | `/v1/tasks/{id}/cancel` | End as `cancelled`; hibernates the box. |
| DELETE | `/v1/tasks/{id}/box` | Delete a finished task's box early; keep its result until `deleteAt`. |
| DELETE | `/v1/tasks/{id}` | Delete a finished task and its box now. |
| GET | `/v1/tasks/{id}/files/{fileId}` | Download a result file. |
| GET | `/v1/tasks/{id}/final-screenshot` | Screenshot captured at finish. |
| POST | `/v1/tasks/{id}/links` | Mint links: `{ "kinds": ["control","view","screenshot"], "ttlSeconds": 86400 }`. |
| DELETE | `/v1/tasks/{id}/links` | Revoke all links (or `?id=` for one). |

### Selectable login profiles

`GET /v1/tasks/profiles` returns only profiles the caller may use for task
creation. For example:

```json
[
  { "id": "lp_7f3a…", "application": "codex", "label": "Codex · 7F3A" },
  { "id": "lp_20b9…", "application": "github", "label": "GitHub · 20B9" }
]
```

IDs are random, stable, account-scoped references stored with the saved
profiles. Labels are generated from the application and a short part of the
random ID; neither IDs nor labels are derived from email, username, host or
the existing profile name. The response contains no email, username, host,
original profile name or credential contents. The owner can see which saved
profile each label represents in the controller UI. Deleting and recreating a
profile gives it a new ID. A task creation request can use only IDs returned
for its caller; the controller resolves them within that account and rechecks
access at creation time.

### Create request

```json
{
  "name": "fix-flaky-test",
  "goal": "Make TestFoo pass reliably and open a PR.",
  "agent": "codex",
  "loginProfiles": [
    { "profileId": "lp_7f3a…", "model": "gpt-6-sol", "reasoningEffort": "high" },
    { "profileId": "lp_20b9…" }
  ],
  "tools": ["desktop"],
  "instructions": "Optional extra Markdown instructions.",
  "slotId": "…", "diskGiB": 20, "memoryGiB": 4, "swapGiB": 1,
  "timeLimitSeconds": 7200,
  "retentionSeconds": 86400,
  "links": { "kinds": ["control", "screenshot"], "ttlSeconds": 86400 }
}
```

- Required: `name`, `goal` (1–100,000 bytes), `agent`.
- `diskGiB` sets the task box's workspace volume size (1–1,000 GiB, default
  10 GiB). `memoryGiB` and `swapGiB` set per-box limits where the selected
  container-isolated shared worker supports them; omit them to use normal box
  defaults. `slotId` optionally selects an available worker slot. Creation
  validates these settings against the selected worker and caller's limits.
- The other optional settings follow box creation. The task's `loginProfiles`
  use `profileId` from `/v1/tasks/profiles` instead of profile names.
- `timeLimitSeconds`: 300–604,800, default 14,400 (4 hours).
- `retentionSeconds`: 3,600–604,800, default 86,400. It applies to both the
  box and task record so the result remains available for inspection.
- `links` is optional; without it no links are minted.

The box's startup instructions are the goal plus a fixed preamble: work until
the goal is done, then call `completed_task`; call `failed_task` only when the
goal is impossible, and say why.

Response: `201` with the task resource, including full link URLs.

### Polling for a result

External services poll the task resource; the controller does not call their
webhooks. After `POST /v1/tasks`, save the returned task ID and call
`GET /v1/tasks/{id}` every 30 seconds until `state` is `completed`, `failed`,
`timed_out`, `abandoned` or `cancelled`. The service can cache the latest status
between polls; a 60-second interval is also reasonable if slower completion
updates are acceptable. Each GET returns `200` with the current task resource
immediately. A disconnected or failed poll does not change the task; retry with
the same ID and backoff. Pollers should add jitter when monitoring many tasks.

The terminal response includes `result` or `failure` and `deleteAt`. The
polling service should copy any result text and download required files before
`deleteAt`; subsequent reads return `404` after the task is deleted. The
service can return its own job ID immediately to its caller and expose the
saved result through its own API. No outbound callback is required.

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
  deleted. If the box is deleted early, `screenshot` keeps serving the final
  screenshot until the task record is deleted. All links fail after task deletion.
- **Page links:** `/t/{token}` exchanges the token for an HttpOnly,
  `SameSite=Strict` cookie limited to `/v1/task-links/` and that box. It then
  removes the token from the address bar, so it doesn't leak through history
  or `Referer`.
- **Scope:** a link gives no direct access to other boxes or `/v1/` endpoints
  outside `/v1/task-links/`. A `control` link does grant input to this box's
  terminal and desktop, including access to files and credentials readable
  inside the box. A `view` or `screenshot` link can reveal anything displayed
  on screen. Treat every link as a sensitive capability.
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
existing result. Worker side, they call `POST /v1/agent-desktop/task/complete`
and `…/fail` with the box's desktop-agent auth.

## Tasks created by boxes

There is no new MCP tool. `create_agent_box` takes an optional `task` object:

```json
{ "name": "…", "agent": "codex", "idempotencyKey": "…",
  "task": { "goal": "…", "timeLimitSeconds": 14400, "retentionSeconds": 86400, "links": { "kinds": ["screenshot"] } } }
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
  result text; it opens the task page while the task is retained.
- Box-created: the chat message above to the creator box. The owner sees it in
  the Recent Tasks list until deletion.

## Storage

- `goal_tasks`: id, account, creator (kind, id), logical_box_id (nullable,
  `ON DELETE SET NULL`), name, goal, settings, state, result_text,
  failure_reason, failure_message, final_screenshot, timestamps.
- `goal_task_files`, `goal_task_links` (token_hash, kind, expires_at,
  revoked_at), `goal_task_events`.
- `goal_task_transcripts` holds a text copy of the box chat if the box is
  deleted early, because `box_messages` is removed with the box. It is removed
  with the task record at `deleteAt`.

## Build order

1. Backend: tables, task-scoped service credentials, selectable profile IDs,
   create/list/get/cancel/delete, the two agent tools (needs a worker update),
   watchdog, time limit, retention sweeper.
2. Links: `/t/{token}` page and the stream, screenshot and state endpoints.
3. UI: Recent Tasks list and task page (state, result, files, final screenshot,
   transcript, Create new task/Delete now), owner notifications.
4. `create_agent_box` `task` field and the message to the creator box.
