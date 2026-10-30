---
name: vbox-tasks
description: Delegate isolated, single-goal work to temporary vbox task boxes through the task-scoped HTTP API, then poll and report the result. Use when the user asks an agent to hand work to vbox tasks; use the regular vbox workflow for persistent boxes.
---

# Delegate with vbox tasks

Use the controller's [Tasks API](https://github.com/0xikarus/vbox/blob/main/docs/TASKS-API.md)
when the user wants another agent to work on a goal in a temporary box.
`POST /v1/tasks` queues work and returns a task ID immediately. Task boxes and
results expire after the retention period; a follow-up or retry after
completion is a **new task**. This differs from `vbox task BOX`, which runs a
one-shot process inside an existing persistent box.

## Access

Get the controller URL and an owner-issued **task service token** through a
secure channel. The owner creates one under **Tasks → Service API access and
profile mapping** and grants the saved login profiles this agent may use. Send
`Authorization: Bearer <task-service-token>` on each API request. A task
service token can access only its allowed profiles and tasks; an owner
controller token is unnecessary. Keep the token in a private environment or
credential store. Never put its value in a prompt, repository file, command
output, or report.

Use a normal HTTP client to call the API directly. The repository's
[standard-library Python example](https://github.com/0xikarus/vbox/blob/main/scripts/examples/tasks_api.py)
shows authentication, retries, polling, and file downloads. It creates
disposable test tasks when run, so adapt its request logic for real goals.

## Workflow

1. `GET /v1/task-profiles` lists reusable configurations available to this
   credential. Choose one that fits the goal. If none fits, use
   `GET /v1/tasks/profiles` to get allowed, anonymized login profile IDs for
   a custom task. Do not infer IDs from account names or email addresses.
2. `POST /v1/tasks` with `Content-Type: application/json` and a unique, stable
   `Idempotency-Key` header. For a saved configuration, the body needs only
   `name`, `goal`, and `taskProfile`. Include the expected deliverable and
   verification in `goal`. A task profile cannot be combined with custom
   resource or agent overrides. For a custom task, send `agent`,
   `loginProfiles` with a permitted `profileId`, and any required resource
   settings; see the API contract for exact fields and limits.
3. Save the returned task ID. An uncertain POST response should be retried
   with the **same** key and body; `GET /v1/tasks` can help recover its ID.
   A `queued` task may wait for the account's four-task concurrency limit or
   compatible worker capacity. It has no box yet.
4. Poll `GET /v1/tasks/{id}` about every 30 seconds when the user wants the
   result. `GET /v1/tasks/{id}/events` gives a progress timeline. A failed
   poll does not mean the task failed; retry with the same ID and backoff.
5. Report `result.text` when `state` is `completed`, or `failure` when it is
   `failed`, `timed_out`, `abandoned`, or `cancelled`. If still active, report
   its state and ID. Download needed files through
   `GET /v1/tasks/{id}/files/{fileId}` before `deleteAt`; task reads return
   404 after cleanup.

Example direct API request:

```http
POST /v1/tasks
Authorization: Bearer <task-service-token>
Content-Type: application/json
Idempotency-Key: review-change-123-attempt-1

{"name":"review-change-123","goal":"Review change 123, run the relevant checks, and return findings with file references.","taskProfile":"codex-review"}
```

Only create a task when delegation is intended. For a long-lived workspace,
use the regular vbox box workflow instead.
