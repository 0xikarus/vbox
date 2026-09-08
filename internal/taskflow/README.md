# General task backend integration

This package implements the backend in `docs/GENERAL-TASK-UI-CONTRACT.md`.
It has no repository, GitHub, issue, PR, deployment, or controller credential
requirement. The injected Runner is the only execution boundary. Tests use a
fake at that boundary and real private PostgreSQL for all persistence.

## Root wiring

```go
store := &taskflow.Store{DB: db}
if err := store.Migrate(ctx); err != nil {
    return err
}
service := &taskflow.Service{
    Store: store,
    GatewayToken: gatewayToken,
    Runner: realTaskRunner, // implements taskflow.Runner
    MaxWorkers: 6,         // 1–6; zero defaults to six
    Profiles: func(ctx context.Context, account string) ([]taskflow.Profile, error) {
        // Adapt the existing account-scoped profile backend.
        // Profile{Application: "codex" or "claude", Name: savedProfileName}
        return savedTaskProfiles(ctx, account)
    },
    ValidateAssets: validateAccountTaskAssets,
    Images: map[string]bool{"codex": codexImagesReady, "claude": claudeImagesReady},
}
handler := service.Handler()
mux.Handle("/v1/factory/tasks", handler)
mux.Handle("/v1/factory/tasks/", handler)
```

Exact callback types:

```go
type Profile struct {
    Application string `json:"application"`
    Name        string `json:"name"`
}
// Profiles: func(context.Context, string) ([]Profile, error)
// ValidateAssets: func(context.Context, string, []string) error
// Service.Step(context.Context) error
```

`ValidateAssets` must verify account ownership, existence, permitted media types,
size limits, and readability using the existing asset backend. The service checks
the selected agent's image capability, eight-asset bound and duplicate IDs. Both
profile and asset callbacks run at creation and again before each fresh dispatch;
they never run in a DB transaction. Idempotent create replay does not depend on
the profile or assets remaining available later. Keep `/profiles` and `/assets`
on the existing Factory handler.

Run `Step(ctx)` repeatedly from the root scheduler, for example from a one-second
ticker. One Step admits or observes one attempt. Concurrent calls/processes are
supported; up to six scheduler loops avoid one slow network call delaying all
other observation. Each call bounds runtime work to 45 seconds, renews its durable
60-second workflow lease every 15 seconds, and rejects stale lease writes. Runner
methods must honor context cancellation. Errors should be logged and scheduling
continued: transport errors preserve the active reservation and the same attempt
identity. There is no new attempt on lease expiry or observation timeout.

Gateway authentication matches Factory: `Authorization: Bearer GATEWAY_TOKEN`,
`X-Vmbox-Account`, and `X-Vmbox-User`, injected by the trusted gateway. Do not
expose that token to browsers. JSON identity fields are rejected. All mutations
require `Idempotency-Key` scoped to account; reusing a key with a different
operation/body conflicts. Replays return the originally committed Workflow.

| Method | Path suffix under `/v1/factory/tasks` | Body / response |
| --- | --- | --- |
| GET | empty | `Workflow[]` |
| POST | empty | `Create` → `Workflow` |
| GET | `/capabilities` | `{enabled,executionReady,agents:[{name,images}],maxWorkers}` |
| GET | `/{id}` | `Workflow` |
| POST | `/{id}/messages` | `{version,text}` → `Workflow` |
| POST | `/{id}/run` | `{version,planRevision}` → `Workflow` |
| POST | `/{id}/retry` | `{version,attemptId}` → `Workflow` |
| POST | `/{id}/cancel` | `{version}` → `Workflow` |

Successful reads/mutations return 200. Invalid requests return 400; missing
gateway identity 401; foreign/missing tasks 404; stale revisions, invalid state
transitions, and key collisions 409. Capabilities can be read without a Store;
task routes return 503 if it is absent. Storage/backend failures return 500 with
no raw DB error disclosure. Request bodies reject unknown fields, extra JSON,
and bodies larger than 300,000 bytes.

## Execution and history

`Runner.Start(Input)` must reconcile an existing accepted task by account and
attempt ID before any staging or submission. Pending provisioning and ambiguous
errors call Start again with the same ID. A durable non-pending receipt needs
both BoxID and TaskID. `Observe(Input)` must deliver real process termination,
exit/signal and the typed Result, never a guessed answer. Plan results require
`Result.Plan`; synthesis requires text and one of `accepted`, `needs_revision`,
or `blocked`. The service assigns the plan's revision. Workers' exit zero only
unblocks dependencies; the final coordinator determines semantic acceptance.

Synthesis receives the complete Workflow with actual outputs, exits, failures,
and dependency-blocked queued assignments. Attempts are appended in execution
order. For the current revision, start at the most recent `stage=plan` attempt;
the latest subsequent attempt for an assignment is its current retry. Use the
plan matching `ApprovedRevision`, while retaining earlier history as context.

The `general_tasks.document` stores the public Workflow plus attempt-to-revision
membership, dispatch markers, and every terminal typed Result. Accepted plans,
terminal attempts, and conversation messages are never rewritten. Replies
append a revision; retries append an attempt. Prior final text remains visible
until a new synthesis replaces it, and is also retained in the previous synthesis
attempt and coordinator message. Cancellation blocks fresh admission immediately;
an already admitted in-flight submission remains observable. Dependency-blocked
assignments are left queued rather than given invented process failures.

## Essential integration gaps in root-owned contracts/files

1. **Reciprocal global admission.** This scheduler acquires the existing
   `pg_advisory_xact_lock(1986880102,1)` and counts active general attempts,
   Factory planning rows and execution nodes before admitting work. Existing
   `factory.Store.ClaimLimited` and `execution.Store` admission only count
   Factory tables at the base commit. Root must add `taskflow.ActiveCountSQL`
   to both admission counts (guard with `to_regclass('general_tasks')` if
   migrations can be absent), under that same lock, and use bounded planning
   admission. Until then, simultaneous legacy admissions can exceed the combined
   six-worker maximum despite general-task admission being bounded.
2. **Cancellation after a lost submission receipt.** A cancelling provisioning
   attempt goes to `Runner.Observe`, never a fresh `Start`. Observe must reconcile
   by account/attempt even if BoxID/TaskID are empty. It must only report terminal
   when verified, or return a transient error and retain the reservation. The
   shared interface has no separate find-only method or verified cancellation
   method. An ordinary Start error cannot safely be interpreted as proof that
   nothing was submitted; definitive startup/auth failures need a terminal
   Observation, including the real exit when available.
3. **Public history metadata.** Shared `Attempt` has no plan revision or per-attempt
   verdict field. Both are durably retained in the private document, but the exact
   shared HTTP Workflow projection cannot expose them explicitly. Root may add
   these fields if the UI requires explicit historical revision/verdict labels;
   no shared types were changed on this branch.

## Verification

From this vmbox checkout, run `bash internal/taskflow/test-in-box.sh`. It follows
the existing bootstrap pattern, starts a private PostgreSQL cluster with TCP
disabled, and runs:

```text
go test -race -count=1 -v ./internal/taskflow
go test -count=1 ./...
go vet ./...
go build ./...
```

The suite covers the complete create → plan → approve → dependency-ready workers
→ synthesis flow, follow-up revision/history, questions, invalid DAGs/criteria,
worker failure and retry, missing planner results, semantic verdict failures,
submission uncertainty, restart, observation timeout, cancellation before/during
dispatch, idempotency and concurrent replay, strict HTTP validation, account
isolation, deleted attachments, expired leases, renewal during network calls,
stale-writer rejection, concurrent admission and existing Factory capacity.
