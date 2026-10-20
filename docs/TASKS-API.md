# Tasks API

Implementation contract for PR #492. The owner UI is at `/tasks`.

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
queued or starting ────────────▶ cancelled   (creator: cancel)
finished with a box ─▶ box hibernated ─▶ box and task deleted at deleteAt
finished without a box ───────────────▶ task deleted at deleteAt
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

### Queue and concurrency

- Each account may run at most **4** task boxes concurrently. This cap applies
  across owner-, user-, service- and box-created tasks, so another creator cannot
  bypass it. Ordinary boxes use worker slots but do not count against this
  task-specific limit.
- `POST /v1/tasks` creates a durable `queued` task and returns `201` even when
  all four task places are occupied. The controller starts it when both a task
  place and a compatible worker slot are available. A queued task has no box
  or workspace volume yet; `box` and `startedAt` are `null`. `queuePosition`
  is an approximate, changing position in the account's queue.
- The dispatcher takes the oldest runnable queued task first. It may pass a
  task whose requested `slotId`, memory or disk cannot currently be placed,
  so that other tasks can use available workers. Invalid requests fail at
  creation; temporarily unavailable capacity keeps a valid task queued.
- Reserving a task place is atomic and survives controller restarts. The place
  is held from dispatch through startup, running and hibernation. A finished
  task frees its place only when its box releases the worker slot, then the
  dispatcher starts another queued task. Physical worker capacity remains a
  separate limit. If startup fails before a worker slot is occupied, cleanup
  releases the task place.
- The running-time limit starts when the agent begins work, not while queued.
  Cancelling a queued task needs no box cleanup; it becomes `cancelled` and
  enters the normal retention period.

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
  "links": [
    { "id": "…", "kind": "control", "url": "https://ctrl.example.com/t/…", "expiresAt": "…" },
    { "id": "…", "kind": "view", "url": "https://ctrl.example.com/t/…", "expiresAt": "…" },
    { "id": "…", "kind": "screenshot", "url": "https://ctrl.example.com/v1/task-links/…/screenshot.png", "expiresAt": "…" }
  ]
}
```

- `failure` is `{ "message": "...", "reason": "agent|timeout|abandoned|cancelled|box_error" }`.
- `box` becomes `null` after the box is deleted.
- `box` and `startedAt` are `null` while queued; `queuePosition` is present
  only while queued and may change as worker availability changes.
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
| POST | `/v1/tasks` | Create and enqueue a task. `Idempotency-Key` required; returns `201` with its current state. |
| GET | `/v1/tasks` | Recent tasks retained until deletion. `?state=` (exact state, `current` or `history`), `?creator=`, `?limit=`, `?cursor=`. |
| GET | `/v1/tasks/{id}` | Return the current task state and any result immediately. |
| GET | `/v1/tasks/{id}/events` | Timeline: state changes, nudges, owner messages, result. `?after=` cursor. |
| GET | `/v1/tasks/{id}/messages` | Chat transcript while the task is retained. |
| POST | `/v1/tasks/{id}/messages` | Message a running task's agent; `409` after finish. |
| POST | `/v1/tasks/{id}/cancel` | End as `cancelled`; hibernates the box. |
| DELETE | `/v1/tasks/{id}/box` | Delete a finished task's box early; keep its result until `deleteAt`. |
| DELETE | `/v1/tasks/{id}` | Delete a finished task and its box now. |
| GET | `/v1/tasks/{id}/files/{fileId}` | Download a result file. |
| GET | `/v1/tasks/{id}/final-screenshot` | Screenshot captured at finish. |
| GET | `/v1/tasks/{id}/final-terminal` | Terminal text captured at finish. |
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

### Reusable task profiles

Owners can save a complete configuration in **Tasks → Task profiles → New task
profile**. Choose the agent/login profile, model and reasoning effort, RAM,
swap, disk, optional GitHub login, tools, instructions and time/retention limits,
then **Save task profile**. Saving a profile creates no task or box. **Use
profile** selects it for a task; **Edit** changes future tasks only.

Task profiles are different from login profiles: `/v1/tasks/profiles` lists
anonymous saved logins, while `/v1/task-profiles` lists reusable configurations.

| Method | Path | Purpose |
| --- | --- | --- |
| GET | `/v1/task-profiles` | List configurations usable by the caller. |
| GET | `/v1/task-profiles/{name-or-id}` | Get one configuration. |
| POST | `/v1/task-profiles` | Owner only: create `{ "name": "...", "settings": {...} }`. |
| PUT | `/v1/task-profiles/{name-or-id}` | Owner only: replace settings; name stays fixed. |
| DELETE | `/v1/task-profiles/{name-or-id}` | Owner only: delete the preset; existing tasks are unaffected. |

`settings` accepts the configuration fields in the custom create request below,
except `name`, `goal`, `links`, and `taskProfile`. Names use 1–63 letters, digits,
dots, underscores or hyphens, and must be unique within the account. Duplicate
creation returns 409. Deleting a saved login leaves its task profile visible to
the owner for repair, but unusable until its login selection is fixed.

Example configuration (substitute an allowed OpenRouter login ID):

```json
{
  "name": "glm5.3flash",
  "settings": {
    "agent": "opencode",
    "loginProfiles": [{"profileId":"lp_...","model":"openrouter/z-ai/glm-5.3-flash"}],
    "memoryGiB": 2,
    "swapGiB": 1,
    "diskGiB": 4,
    "tools": ["desktop"]
  }
}
```

That selects no GitHub login or optional Blender installation. The base desktop
preset remains required for task MCP tools and final captures. Task profiles
hold references to existing logins, never copies of their credentials.

Create a task with only its name, goal and task profile:

```http
POST /v1/tasks
Authorization: Bearer <TASK_SERVICE_TOKEN>
Content-Type: application/json
Idempotency-Key: <unique-job-UUID>
```

```json
{"name":"job-123","goal":"Process the supplied input and return a report.","taskProfile":"glm5.3flash"}
```

`taskProfile` accepts a name or ID. Only optional `links` can accompany these
fields; configuration overrides are rejected. Each task stores a snapshot of
the profile settings. Edits/deletion do not change queued or running tasks, and
retrying the original request with the same idempotency key returns the original
task even after a profile edit or deletion. The returned task includes the
selected profile name as `taskProfile`.

Service tokens must already allow **every login profile** referenced by the task
profile. Selecting a preset grants no extra credential or role permissions;
presets with roles are owner-only. Unauthorized presets return 404 and are
omitted from the list. Profile names and settings are visible to allowed callers,
so use descriptive names without private account information.

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

- For custom configuration, required: `name`, `goal` (1–100,000 bytes), `agent`.
  With `taskProfile`, supply `name`, `goal`, and the preset reference instead.
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
| `view` | `/t/{token}` | Same page, view-only (noVNC `viewOnly`, terminal read-only, enforced by the server). |
| `screenshot` | `/v1/task-links/{token}/screenshot.png` | Current desktop PNG. `?source=terminal` returns the terminal as text/plain. When the box isn't running it returns the final screenshot, with `X-Vbox-Captured-At`. |

Rules:
- **Tokens:** 256-bit random, stored and looked up only as SHA-256 hashes.
- **Expiry:** `ttlSeconds` defaults to 24 h and is capped at the task's `deleteAt`, or 7 days while it runs.
- **Revocation:** revoked links fail on the next request; open streams revalidate every 250 ms and close on revocation or expiry.
- **After deletion:** `control`/`view` links stop working when the box is
  deleted. If the box is deleted early, `screenshot` keeps serving the final
  screenshot until the task record is deleted. All links fail after task deletion.
- **Page links:** `/t/{token}` exchanges the token for an HttpOnly,
  `SameSite=Strict` cookie limited to `/v1/task-links/` and that box. It then
  redirects to `/t#<link-id>`, leaving the secret out of the final address
  and `Referer`. The initial capability URL is sensitive and may still appear
  in a browser history or an upstream access log. Each link gets a separate cookie.
- **Scope:** a link gives no direct access to other boxes or `/v1/` endpoints
  outside `/v1/task-links/`. A `control` link does grant input to this box's
  terminal and desktop, including access to files and credentials readable
  inside the box. A `view` or `screenshot` link can reveal anything displayed
  on screen. Treat every link as a sensitive capability.
- **Sleeping box:** a link never wakes a hibernated box; the page shows the final
  screenshot and state instead.
- **Queued task:** links minted at creation refer to the task until its box
  exists. The page shows queued state; screenshot requests return `409` until
  a desktop frame is available.
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

Use `create_agent_task` only when the requester explicitly wants a disposable,
one-goal task. Ordinary requests to create or delegate to another box use
`create_agent_box`, which creates a persistent box. `create_agent_task` accepts
the same creation settings plus a required `task` object:

```json
{ "name": "…", "agent": "codex", "idempotencyKey": "…",
  "task": { "goal": "…", "timeLimitSeconds": 14400, "retentionSeconds": 86400, "links": { "kinds": ["screenshot"] } } }
```

- The same create grant and limits apply (`maxBoxes`, `maxDiskGiB`, allowed agents).
- Older runtimes can still send `task` through `create_agent_box`; current
  runtimes advertise separate tools so the persistent choice is clear.
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
- `goal_tasks.transcript` holds a copy of the box chat before hibernation,
  because `box_messages` is removed with the box. It is removed with the task
  record at `deleteAt`. Copied creator chat notifications wait independently
  in `goal_task_notices` until an existing agent session can receive them.

## Service access and example

Owners create/revoke service tokens in **Tasks → Service API access and profile
mapping**, or via `GET/POST /v1/task-service-credentials` and
`DELETE /v1/task-service-credentials/{id}`. POST accepts `name`, `profileIds`,
optional `allowControl` (default false) and `expiresAt`. The raw `vbtask_` token
is returned once. `/v1/task-service-credentials/profiles` is the owner-only
mapping of opaque labels to saved names; task service tokens cannot access it.
Revoking a token prevents new API requests and prevents its queued tasks from
starting. Already running tasks still finish normally.

Run the standard-library Python example:

```sh
export VMBOX_CONTROLLER_URL=https://your-controller.example
# Set VMBOX_TASK_TOKEN privately to the service token.
python3 scripts/examples/tasks_api.py --list-profiles
python3 scripts/examples/tasks_api.py --profile-id lp_... --count 6 --delete
```

This creates explicitly named `api-test-*` tasks, each with a small deliverable,
polls every 30 seconds with jitter, saves results locally and requests cleanup.
Six tasks exercise the queue beyond the four-task cap. `--task-id UUID` resumes
polling without creating another task. Neither polling nor shared links wakes
boxes. Download saved files before `deleteAt`; deletion removes them permanently.

Task workspaces always include the desktop tool preset for task MCP credentials
and final captures. If the worker is unavailable at completion, a final frame
or terminal may be unavailable; the outcome and uploaded result files remain
saved, and workspace cleanup continues. No task API permission gives a service
access to unrelated controller endpoints. A control link can access the entire
workspace through its terminal, including credentials imported into that box.
