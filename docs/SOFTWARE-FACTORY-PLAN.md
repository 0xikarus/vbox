# Box-native software factory: audit and implementation plan

2026-09-08. Audit baseline: `f96c817`; implementation in progress on
`proposal/software-factory`. See `FACTORY-PROGRESS.md` for tested commits and
live task handles. This document describes the full target, not completed work.
Keep this proposal and eventual implementation on feature/diff branches only.
Do not merge to main or deploy without subsequent approval.
Builds, tests, browser checks, review, and final integration all execute inside
vmbox workers. External deployment is optional; verification must not depend on
external CI/CD. GitHub event intake is optional, but the approved goal requires a
master issue, linked feature issues and a branch/PR per feature. A tested product
running only inside a retained box is valid delivery; that does not waive those
GitHub records or the controller UI workflow.

## 1. Open-issue audit

Reviewed all five open issue bodies and their discussions against committed code,
merge history, and repository verification records. This was a source/evidence
audit, not a new production reproduction of every old bug.

| Issue | Decision | Evidence and remaining work |
|---|---|---|
| #2: Go/multi-provider rewrite epic | Delete as superseded, not “every acceptance criterion passed” | Rewrite merged in PR #5, commit `9a0f790`. Ordinary CLI is now controller-only. Sevalla removed in `31042a4`; Telegram removed in `969d18b`. Its mandatory standalone/Sevalla/Telegram architecture and old commands contradict subsequent product decisions. Still-useful hardening work remains in #6 and below. |
| #3: GitHub issue watcher | Keep; relevant objective, obsolete specification | No watcher binary exists. Durable intake/deduplication/authorization remain useful, but the dependency on the unfinished rewrite, mandatory integrations wizard/Telegram, exact old run template, and PR-centric completion need replacement. Make this the optional GitHub intake track for the factory, not a second provider scheduler. |
| #6: persistence/provider E2E/UX/cost hardening | Keep; partially completed and partially superseded | CLI pairing, provider editing, saved profiles and substantial Railway persistence/web/native tests exist. Real remote Docker/Incus evidence and factory task quotas/deadlines remain gaps. Drop standalone/Sevalla/Telegram requirements when narrowing this issue; do not discard unresolved hardening with them. |
| #70: initial Claude prompt lost during update | Keep pending targeted validation | `ee528c4` adds `DISABLE_AUTOUPDATER=1` to managed legacy Claude startup, with `TestStartTmuxTaskAcceptsClaudeTrustBeforeDeliveringPrompt`. This is a mitigation, not a recorded live proof of the issue's restart/exactly-once acceptance criteria. `/tasks` and message APIs remain registered. Modern one-shots use `claude -p` and avoid that interactive delivery path. |
| #71: active task with missing tmux session | Keep; unresolved in committed code | `HEAD:internal/controller/interaction.go` reconciles runnable tasks/messages but not existing active task/session absence. The working tree contains an uncommitted reconciliation/fencing draft and `task_reconcile_test.go`; these are not release evidence. Legacy active tasks also participate in the one-shot auto-hibernate busy check. |

Issue #2's body and comments were backed up before deletion to
`/tmp/vmbox-issue-2-audit-backup.json`. That local backup preserves text, not
GitHub issue identity or a guaranteed server-side restore path. Other issues
are retained unchanged; their proposed scope updates are recorded here.

Relevant sources: `internal/controller/server.go`, `interaction.go`, `process.go`,
`internal/boxruntime/interaction.go`, `process.go`, `internal/api/v1/process.go`,
`docs/SHELL-FIRST-VERIFICATION.md`, `docs/WEB-WORKSPACE-LIVE-0907.md`,
`docs/CLI-DESKTOP-LIVE-0907.md`, and `docs/COWORKER-OVERVIEW.md`.
Older implementation documents contain superseded rollout statements; prefer
dated release evidence and current code over treating every document as current.

## 2. Architecture: add coordination, reuse execution

```text
Controller Factory tab: repository + idea + images
                         |
                 Plan -> planning box -> editable plan
                         |
                   Approve & build
                         |
           master issue + feature issues/dependencies
                         |
                 factory coordinator + durable DB
                         |
             controller logical boxes + process tasks
                         |
           implement -> verify -> review -> integrate
                         |
             feature PRs + reviewed integrated product
                         |
             retained box/artifacts + UI evidence
```

Add a separate `vmbox-factory` service in this repository. It owns work items,
dependencies, attempts, approval and evidence—not provider resources. It uses
the controller API; it must not write controller tables directly. The controller
continues owning allocation, fencing, credentials and lifecycle. The factory can
run on a trusted machine/service; it must not be a child of a one-shot worker
that hibernates after finishing. No MCP or Telegram restoration is required.

### Repository connection: GitHub App

Support any GitHub repository the operator grants access to, including private
repositories, not just `vmbox-service`. “Any” does not bypass organization approval
or repository permissions. Install the App on the desired personal/org account,
select repositories (or all), and list permitted repositories in the Factory tab.
Installation by other users/orgs needs appropriate App visibility/distribution.
Non-GitHub sources can later use a Git/snapshot connector; App access does not
grant access to other Git hosts or private package registries.

Keep the App private key encrypted on the trusted server. Bind an installation
to a controller account only after an authorized owner completes a signed-state
setup flow and the backend verifies access; knowing an installation ID is not
ownership proof. Check controller authorization and installation scope on every
operation, including repository removal/transfer and suspended installations.

Planning needs read-only metadata/contents access. Approved execution needs
separate issues/contents/PR write permission. Installation tokens can be restricted to
selected repositories/permissions and expire after one hour: broker refresh for
active authorized attempts. Never give a coding box the App key or unrestricted
installation credentials; never embed a token in a logged clone URL/Git config.
Issue write permission is required for the master/feature records; webhook
subscriptions are needed only for optional event intake.

Verified references: [GitHub App repository selection](https://docs.github.com/en/apps/using-github-apps/installing-a-github-app-from-a-third-party)
and [installation authentication/scope/expiry](https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/authenticating-as-a-github-app-installation).

### Existing controller UI: Factory tab first

No second dashboard/login. A narrow authenticated backend gateway forwards
Factory requests with trusted account/user context; browsers cannot forge that
context or receive service credentials. GitHub logic belongs to the factory,
not the controller's provider scheduler. Use native HTML and existing light CSS:

```text
Boxes · Providers · Profiles · Factory

Repository [ permitted repo v ]   Base [ branch / revision ]
Planner    [ Claude / Codex v ]   Login [ saved profile v ]
Idea       [ multiline text                              ]
Images     [ Add images ]  thumbnail + filename + Remove
           [ Plan ]

Work item  Phase / box link / execution status
Plan       Readable sections, questions, tasks and checks
           [ Revise plan ]   [ Approve & build ]
```

Plan durably snapshots idea, images and resolved repository revision, creates a
planning attempt, and queues a box with the chosen agent/profile. Show actual
phases: uploading, waiting capacity, restoring, preparing inputs, planning,
plan ready, needs clarification, failed. No invented progress percentage.
Double clicks, refresh and lost responses recover the same attempt.

Planner instructions: inspect the real repo and images; return an implementation
plan, assumptions/questions, dependency graph, file ownership, verification
commands/toolchain requirements and expected artifacts. Plan does not authorize
implementation, pushes, publishing or spawning child workers. Read-only GitHub
scope alone does not prevent local writes/network access: use an isolated checkout
and supported planning execution policy; do not execute repository install/build
scripts without separate approval. Report unsupported policy enforcement honestly.

Persist Markdown plus versioned structured tasks/checks. Escape rendered content,
never arbitrary agent HTML. Edits/refinements create plan revisions. Approve &
build binds the chosen revision, source/input digests, worker/time cap and publishing
policy; changes require renewed approval. Planning-box cost is authorized by Plan,
not unlimited implementation spending. Preserve input and plan history.

### Images: private assets staged into the planning box

Images need durable worker access, not public URLs. Reuse one private asset
subsystem for inputs and later verification evidence:

1. Browser uploads through an account-authorized controller gateway endpoint.
   Validate decoded PNG/JPEG, dimensions and size, assign safe filenames,
   strip unnecessary metadata, and return opaque asset IDs. Proposed visible,
   configurable limits: 8 images, 10 MiB each, 40 MiB total, 25 megapixels each.
2. Store binaries in a private object store or a dedicated persistent server
   volume for MVP, not ephemeral container disk or base64 DB rows. Store account,
   digest, type, size and retention metadata. A new cloud bucket is not required.
3. Plan references only ready, same-account assets and an immutable ordered
   manifest. No arbitrary remote image URL fetching or user-selected filesystem
   targets. Revisions cannot swap attachments under a running/approved attempt.
4. Trusted runtime preparation reads account-scoped assets, verifies digests,
   and streams them over a freshly resolved SSH connection into
   `/data/workspace/.vmbox-factory/attempts/ATTEMPT_ID/images/`. The agent receives the idea,
   manifest and local files—not controller secrets or public attachment URLs.
5. Add per-agent image-input adapters. The generic one-shot API remains text-only;
   mentioning a filename in a prompt does not prove the model saw the image.
   Check the installed Claude/Codex version and use an actually supported image
   interface or confirmed local image-reading tool. Explicitly reject unsupported
   combinations; do not silently substitute OCR or drop attachments.
6. Live-test each advertised agent with an image containing visual details not
   available in its filename/prompt. Verify actual visual grounding, plus expired
   grants, cross-account denial, corrupt input, interrupted upload and restart.

UI previews/downloads remain authenticated. Keep grants out of logs/prompts and
revoke on cancellation. Reference-count assets across revisions; collect abandoned
uploads after a grace period. Removing a draft attachment must not erase evidence
referenced by approved work.

### Reuse what exists

- Logical boxes, durable volumes, slot allocation, resume and hibernation.
- Account authentication and encrypted Claude/Codex/GitHub profiles.
- `POST /v1/logical-boxes/{id}/process-tasks` and task status/output endpoints.
- Codex exec, Claude -p and shell execution; idempotent task submission,
  worker claim journal, nullable real exit code and signal reporting.
- Native/web tmux and desktop for human inspection or intervention.
- Existing generic `/v1/runs` remains a separate API. Do not rebuild the factory
  against its old resource-per-run assumptions or delete it as part of this work.

### Gaps that require real implementation

1. **Durable workflow coordinator:** planning attempts, approval and restart
   recovery are partially implemented. Feature dependencies, pause, bounded
   retries and the full execution/integration workflow still require implementation.
2. **Box reservation across stages:** assignment fencing exists, but an exclusive
   multi-stage factory reservation does not. A reservation must prevent competing
   automated writers and automatic hibernate between dependent stages. Manual
   takeover pauses the workflow; forced owner actions remain visible and invalidate
   stale reservations. Expiry pauses scheduling, not “kill all box processes.”
3. **Generic verification execution:** extend logical process tasks with optional
   exact argv and validated working directory, mutually exclusive with agent/prompt.
   Preserve current agent CLI compatibility. Never interpolate issue text into
   shell commands. Existing `shell --prompt` can bootstrap trusted fixed commands.
4. **Scoped task cancellation/deadlines:** the process-task API currently lacks
   its own cancellation endpoint and per-task deadline fields. Legacy run TTL and
   concurrency settings do not prove protection of this newer path. Add fenced
   process-group cancellation, durable outcome, and queued-start cancellation.
   Loss of SSH/heartbeat stays unknown; it is not a synthetic exit code.
5. **Structured verification and artifacts:** task output is a truncated prefix
   capped at 1 MiB, not an artifact store. Add artifact manifests, bounded upload
   and authenticated download for logs, reports, screenshots and build outputs.
   Capture evidence before stage release/hibernate. Keep metadata independent of
   box deletion, with explicit retention policy; do not silently change existing
   process-task history semantics.
6. **Service authorization:** use a dedicated restricted coordinator identity,
   permitted box/profile pool, concurrency ceiling and auditable actions. Add
   expiring per-attempt evidence grants if workers upload artifacts. No controller
   owner/provider token belongs in coding-agent prompts or worker environments.
7. **Existing UI integration:** Factory tab with App/repository selection, idea,
   images, Plan, revision/approval and stage/evidence views. Add equivalent
   `vmbox factory ...` commands later; those commands do not exist yet.

## 3. Work and result contracts (freeze before parallel coding)

Factory tables should own `work_items`, `input_assets`, `plan_revisions`,
`repository_connections`, `stage_attempts`, `dependencies`,
`evidence`, `approvals`, and an outbox for pending controller submissions.
Use separate storage/migrations, even if a PostgreSQL server is shared.

Minimum work specification:

- ID, account/requester, task description and acceptance criteria.
- Immutable idea/image manifest and approved plan revision; planning and build
  authorization are distinct records.
- Source: repository + pinned base revision, or an explicit workspace snapshot.
- Allowed box/profile pool; chosen agent per implementation/review stage.
- Trusted build/test/browser-check commands, working directory, toolchain manifest.
- Output contract: master issue, linked feature issues, per-feature branch/PR,
  inspected integrated workspace/artifacts, and all progress/evidence in the UI.
- Deadlines, active-worker cap, attempt cap, retention, and allowed external writes.

Each attempt records stage, attempt number, source/spec digests, reserved box,
assignment generation, controller task ID, stable idempotency key, timestamps,
actual execution outcome, and evidence references. Evidence records command argv,
cwd, toolchain versions, exit/signal, log digest, truncation and optional screenshots.
Record exact controller/runtime/image versions for reproducibility.

Separate execution from acceptance:

- Workflow: draft -> planning -> plan ready -> plan approved -> queued ->
  implementing -> verifying -> reviewing -> awaiting approval
  -> accepted; with explicit blocked/failed/cancelled paths.
- Process state and exit code remain exactly as reported by the controller.
- An agent's zero exit code or claim of success cannot mark verification passed.
- Required checks must all have real recorded outcomes against the candidate
  revision. Missing, stale, truncated-essential or ambiguous evidence blocks approval.
- New edits invalidate earlier verification and approval. Record who approved
  which candidate/spec; preserve earlier attempts rather than rewriting them.

Persist submission intent before sending it. On timeout/restart, retry the same
request with the same key and recover its task ID. Only start a new attempt after
the previous outcome is understood. Two coordinator instances must not both claim
one stage: use transactional leases/compare-and-swap plus controller reservations.

## 4. Verification happens inside boxes, not CI/CD

Implementation agents can run exploratory checks. Acceptance checks are executed
again by a runner using the approved verification specification, preferably in a
separate verifier box from a clean checkout of the candidate commit. For non-Git
projects, hand off a checksummed workspace snapshot. Neither approach requires
an externally deployed app. Git transport is optional; an authenticated artifact
bundle can carry the source between boxes.

The verifier must not trust a builder-authored “passed” report. Run commands and
collect actual exit codes. Pin trusted verification inputs; explicitly review
changes to tests/check configuration. A separate box is an independent rerun,
not protection against every malicious repository: executed source can still
attack its environment. Do not give verification boxes publishing credentials.

For UI work, start the app inside the verification task's process group, wait
for readiness, exercise it using an installed browser, capture screenshots and
assertions, then stop only that task's app. Desktop/VNC is for manual inspection;
it is not itself a browser automation API or proof of correct behavior.

At completion deliver workspace/box identity, candidate revision or snapshot,
verification summary, artifact references and reproducible start/test commands.
Default to retained files with compute hibernated once evidence is safe. An
explicit “keep preview running” request needs a retained reservation/time limit.
Feature PRs are required by the approved workflow. Merging to a repository's
protected/default branch, external deployment, CI execution or volume deletion
still require the corresponding explicit approval; none is implied by Plan.

For vmbox's own build, prepare Go 1.26, Git and the relevant Node/browser/tmux
tools inside workers. PostgreSQL integration tests need an isolated test DB.
Do not assume Railway boxes expose a Docker daemon: use native Go tools and an
isolated DB where possible. Docker/Incus-specific tests need approved capable
test hosts and remain explicitly blocked without them—never mount host control
sockets into an agent box to turn a test green.

## 5. Delivery phases and acceptance gates

### Phase 0: establish a reliable baseline

- Review #71's preserved draft separately; test exact session absence, sibling
  preservation, assignment races and SSH failures before deciding to ship it.
- Validate real Codex and Claude one-shots individually, then simultaneously in
  separate disposable boxes. Use real repository work/builds, not canned answers.
- Record failed authentication as blocked; use only explicitly selected profiles.
- Verify current idempotent submission, stored exit/output and controller restart
  behavior. Inventory actual toolchains/free slots without altering user boxes.
- Narrow #3/#6 and settle #70 via targeted evidence or an explicit legacy API
  retirement decision. Factory execution must not depend on legacy screen parsing.

### Phase 1A: repository + idea + images -> real plan

- Freeze gateway/account, attachment, planner and plan revision contracts.
- Connect a GitHub App and select a permitted repo in the existing Factory tab.
- Implement private durable upload/staging and actual image receipt for each
  advertised planner. Inputs must be ready before execution starts.
- Produce a persisted editable plan, not implementation. Reload/restart recovers
  the same attempt. Test an unrelated public and authorized private repository,
  image grounding, ungranted-repo denial and cross-account isolation.
- Preview only from the feature branch in isolated vmbox staging. The user has
  authorized implementing all phases; each factory work item still needs its
  own plan approval before billable implementation or GitHub publication.
  Production controller deployment and main merge remain unapproved.

### Phase 1B: approved plan -> master issue -> feature PR -> verified workspace

- Freeze versioned work/evidence/client contracts and compatibility errors.
- Implement durable coordinator + reservations + task controls + artifact path.
- Approve one plan, publish its master issue and linked feature issues, implement
  one feature on its own branch, verify it in-box and open its PR. Publish useful
  progress/review/verification comments and show them in the UI. Retain the tested
  workspace and hibernate safely once evidence is durable.
- Test real build pass/failure and an agent exit 0 with failing tests; only the
  first may reach approval-ready. Restart during submission, execution and
  artifact ingestion without duplicate work or fabricated completion.

### Phase 2: concurrent work and independent review

- Add DAG scheduling, bounded attempts and worker/profile pools.
- Distinct checkouts/branches/boxes for concurrent writers; serialize integration.
- Review and verify the integrated candidate inside a box, not just each branch.
- Test contention, cancellation races, stale evidence, out-of-capacity queueing,
  manual takeover, reservation expiry, cross-account denial and sibling safety.
- Enforce hard worker/time/attempt limits. Monetary spend is an estimate unless
  attributable provider billing is available; do not advertise a false hard dollar cap.

### Phase 3: optional GitHub event intake and polish

- Replace #3's obsolete specification with a small adapter: authorized manual
  import first, signed webhooks later; durable delivery deduplication and approval
  before billable work. GitHub-specific behavior stays outside the controller.
- Treat issue/repository text as untrusted task data, not permission to alter
  credentials, budgets, acceptance criteria, or publishing policy.
- Issue/PR feedback is part of the core workflow, not deferred to event intake.
  Keep comments meaningful and sanitized; never publish raw secrets/logs. Add
  operational diagnostics and retention controls around the core loop.

## 6. Parallel implementation using vmbox itself

Implementation is authorized and uses three isolated vmboxes, with their roles
reused between bounded tasks. Current desired fleet capacity is 4; the user
approved increasing it only as needed up to **6**, not 8. Recheck live capacity
before allocation; do not change or interrupt unrelated user boxes. Current
box/task IDs and branch evidence are in `FACTORY-PROGRESS.md`.

Bootstrap with today's `vmbox new ... --no-dialog --hibernate --profile ...`
and `vmbox task BOX codex|claude --prompt ... --idempotency-key ... --json`.
The coordinator for this bootstrap is the supervising agent using task IDs,
not the unbuilt factory. Every worker gets its own checkout and branch from
one pinned clean committed baseline; never copy this dirty local worktree into it.

First, one integrator prepares agreed API/types, migration-number allocation
and package stubs on the feature integration branch, not main. Phase 1A splits:

| Worker/branch | Scope | Proof |
|---|---|---|
| `factory-ui` / `factory/ui-plan` | Existing Factory tab/gateway, repo/idea/image form, previews, plan revisions/approval | Desktop/mobile upload, safe rendering, no duplicate Plan |
| `factory-planner` / `factory/planner` | Workflow DB/outbox, GitHub App broker, planning task scheduling/results | Repo scopes, actual repository inspection, restart/deduplication, no implementation from Plan |
| `factory-assets` / `factory/assets` | Durable assets, scoped staging, agent multimodal adapters/capability checks | Real visual grounding for both advertised agents; input privacy and grant expiry |

Integrator owns shared routes/types/migration wiring. Planner owns App tokens;
assets owns download grants; UI forwards authenticated intent only. Freeze request,
response and failure fixtures before independent coding.

For Phase 1B, reuse boxes with the following independent ownership:

| Worker/branch | Suggested agent | Owns | Required proof |
|---|---|---|---|
| `factory-control` / `factory/control` | Codex | Controller reservations, scoped grants, task cancellation/deadlines; agreed API extensions | Real processes, wrong-fence/account denial, race tests, sibling preservation |
| `factory-engine` / `factory/engine` | Claude or Codex | `cmd/vmbox-factory`, `internal/factory` workflow DB/outbox/scheduler and controller client | Real DB restart/deduplication tests; no duplicate accepted submissions |
| `factory-verify` / `factory/verify` | Codex or Claude | `cmd/vmbox-verify`, `internal/verification`, artifact transport and capture | Actual successful/failing builds and browser checks inside its box; evidence fidelity |

Agree artifact ingress ownership before this split: verifier owns capture/upload
and artifact storage code; engine owns evidence registration/acceptance. Shared
API types, controller routing and migration registration have a single integrator
owner. Workers propose changes to those contracts instead of editing them in parallel.

After these branches are integrated into the feature branch, reuse boxes for CLI
parity and independent review; add GitHub event intake only after the core passes. Do not
allocate a separate permanent box for every feature.

Every worker prompt contains: scope/files, pinned baseline, contract version,
chosen agent/profile, acceptance commands, known tool limits, branch, evidence
directory and finish condition. The agent tracks its own engineering checklist;
vmbox tracks its actual process result. Require a commit or source bundle and
verification evidence. No worker merges main, changes production controller
configuration, deploys itself, deletes volumes, or spawns additional workers.

Use unique keys such as `factory-bootstrap:engine:attempt-1`. Poll saved task IDs
with bounded waits; inspect unknown results before another attempt. Work outputs
can be pushed to isolated branches only when authorized, otherwise exchanged as
bundles. One designated integration box combines approved changes, reruns the
full relevant verification there, and requests approval for release. The tested
revision must be the revision released. A final report distinguishes local/mock
checks, real worker execution and any untested provider/platform paths.

## Current next milestone

Finish the real UI-to-plan loop: integrate private SSH staging with the existing
controller task runner and durable result inbox, configure an authorized GitHub
App installation, then test actual repository/image planning, replies, reload and
restart in isolated vmbox staging. Codex visual grounding has passed in its local
adapter; Claude authentication and the full UI loop remain unverified.

Then wire approved-plan issue publication, dependency scheduling, per-feature
PRs, independent in-box review/verification and final product integration. Keep
the conversation, questions, plan revisions, progress and evidence in the
existing controller Factory tab throughout. Do not stop at a headless backend,
an agent exit of zero, an issue/PR alone, or only a passing unit-test suite.
