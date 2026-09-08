# General task UI implementation contract

Root-owned shared Go types: `internal/taskflow/types.go`. No mandatory repository.
Use the existing Factory backend/gateway and saved profiles/assets. General routes
are `/v1/factory/tasks` (GET list / POST create), `/tasks/{id}` (GET),
`/tasks/{id}/messages`, `/run`, `/retry`, `/cancel` (POST).
All responses are Workflow, except list is Workflow[]. All mutations require
Idempotency-Key; action bodies contain version (optimistic lock).
Create body is taskflow.Create; messages also contain text; run also planRevision;
retry also attemptId (only terminal failed attempts; preserve history).
Capabilities: GET `/v1/factory/tasks/capabilities` returns
`{enabled,executionReady,agents:[{name,images}],maxWorkers}`.
Profiles and assets reuse existing `/v1/factory/profiles` and `/assets` endpoints.
Authentication is server-injected account/user via the existing gateway token;
no client-supplied account or controller token in JSON.

States: planning_queued, planning, awaiting_reply, awaiting_approval, running,
synthesizing, completed, needs_revision, failed, cancelling, cancelled.
Attempt states: queued, provisioning, submitted, running, exited, result_missing.
Plan approval must validate nonempty criteria, unique IDs, dependency DAG,
bounded assignments (1–20), no unresolved questions, exact plan revision.
Each accepted revision has immutable assignments. Workers return text; the
coordinator judges it. Exit0 alone does not mean semantic acceptance.
Stage work runs dependency-ready assignments in separate controller boxes.
Synthesis receives actual outputs and failed attempts, never fabricated answers.
Replies before approval replan; replies after a final result start a new plan
revision preserving prior conversation/results. Do not rewrite running work.
Cancel prevents new dispatch immediately; currently running processes must be
observed until terminal unless the runtime has a verified cancellation API.
No pretend cancellation or task replay from an observation timeout.

Backend worker owns taskflow files except types.go. Public integration API:
`Store{DB:*sql.DB}.Migrate(ctx)`; `Service{Store:*Store,GatewayToken:string,
Runner:Runner,MaxWorkers:int,Profiles:func(context.Context,string)([]Profile,error),
ValidateAssets:func(context.Context,string,[]string)error,Images:map[string]bool}`;
define Profile with application/name. `Service.Handler() http.Handler` and
`Service.Step(ctx) error`. Step owns durable account-scoped leased dispatch,
renewal, recovery, shared admission maximum six (include existing Factory
planning/execution where present). No DB transaction across network calls.

UI worker owns tasks.js/tasks.css and new browser fixture tests ONLY. Export
`mountTasks(root, api)` returning cleanup. Native HTML, tiny CSS. Root wires tab
and routes. UI shows create/attachments/profile, plan with Run, reply, worker
tree with actual outputs/exits/failures, retry/cancel and final synthesis.
Polling must not overwrite another selected task or newer mutation. Preserve
drafts on errors; no unsafe HTML or success claims from HTTP acceptance alone.

Runtime worker owns internal/taskflowruntime and cmd/vmbox-task-runner ONLY.
Export Runner implementing taskflow.Runner; constructor/config API documented.
Use existing factory.ControllerClient, transport.SSH and resultinbox.Inbox.
Root adds controller methods EnsureTaskBox(ctx,account,workID,attemptID,boxID,
agent,profile), SubmitTaskRunner(ctx,account,boxID,attemptID), and
FindTaskRunner(ctx,account,boxID,attemptID).
Box name `task-ATTEMPT`, create key `task-box:ATTEMPT`, resume `task-resume:ATTEMPT`.
Fixed wrapper `/data/workspace/.vmbox-tasks/bin/vmbox-task-runner` reads private
stdin `/data/workspace/.vmbox-tasks/attempts/ATTEMPT/job.json`.
No repo/source SHA required. Stage typed private job/images/binary over current
SSH; no controller or GitHub credential passed to workers. Callback capability
is scoped by existing inbox to account/work/attempt. Real Codex/Claude normal
saved login, structured result file/adapter, actual exit/signal, durable receipt
before hibernate, no transcript-marker or screen parsing. Recovery must find
accepted task before fresh staging/cap issuance. Report startup/auth failures.

All tests/builds run inside vmbox boxes. No main merge or production deployment.
This contract describes work being implemented, not a claim of existing support.
