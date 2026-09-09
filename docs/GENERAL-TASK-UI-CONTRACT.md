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
agent,profile,githubProfile), SubmitTaskRunner(ctx,account,boxID,attemptID), and
FindTaskRunner(ctx,account,boxID,attemptID).
Box name `task-ATTEMPT`, create key `task-box:ATTEMPT`, resume `task-resume:ATTEMPT`.
Fixed wrapper `/data/workspace/.vmbox-tasks/bin/vmbox-task-runner` reads private
stdin `/data/workspace/.vmbox-tasks/attempts/ATTEMPT/job.json`.
No repo/source SHA required. Stage typed private job/images/binary over current
SSH; no controller credential passed to workers. An optional saved GitHub profile
is provisioned by the controller, not embedded in the job or process prompt. Callback capability
is scoped by existing inbox to account/work/attempt. Real Codex/Claude normal
saved login, structured result file/adapter, actual exit/signal, durable receipt
before hibernate, no transcript-marker or screen parsing. Recovery must find
accepted task before fresh staging/cap issuance. Report startup/auth failures.

All tests/builds run inside vmbox boxes. No main merge or production deployment.
This contract describes work being implemented, not a claim of existing support.

## Current implementation handles

Shared contract/controller helpers: `f31ca0b`; launch regression coverage:
`c7f12f6`. In-box task `23a037d7-6797-4a52-84ea-de5c00000000` passed
the builder/verifier/general task controller tests and scoped vet with exit 0.

- UI worker: `64178c3a-a54e-4931-8b74-bf5500000000`,
  `factory/general-task-ui-0908`, box `factory-ui-0908`.
- Backend worker: `2cb9bb86-818d-4115-8a03-3ead00000000`,
  `factory/general-task-backend-0908`, box `factory-assets-0908`.
- Runtime worker: `e513f195-c09e-4013-8100-1cae00000000`,
  `factory/general-task-runtime-0908`, box `factory-review-0908`.

UI and backend implementations are integrated on `proposal/software-factory`.
The in-box UI regression task `d2bc557a-18d9-48da-8507-b06500000000`
exited 0: all 14 Chromium API-fixture tests passed, plus gateway tests and vet.
These fixtures are not proof of live agent execution.

Backend review added shared admission with existing factory workers, fair
round-robin observation, immutable submission bindings and rejection of retries
for missing results. Verification task `22d53690-ce5a-4fbd-8623-771b00000000`
stopped before tests (exit 127: missing PostgreSQL bootstrap); the script now
installs its required private-cluster tooling before running.

The runtime worker exited 0 and is integrated, including service startup wiring.
The general task PostgreSQL and runtime race tests passed on `7125c83`; the full
run found a remote-build SQL fixture expectation that is corrected in `7fcc32e`.
Final verification exited 0. Production deployment was subsequently approved
and completed, including the HTTPS callback backend. Full real UI-to-worker
acceptance remains unfinished. See `TASKS-UI-VERIFICATION.md` and
`TASKS-ROLLOUT.md` for exact evidence. Nothing is merged to main.
