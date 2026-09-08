# Factory implementation progress

Feature branch: `proposal/software-factory`. No main merge or production deploy.
Maximum authorized fleet capacity: **6**. Current desired capacity remains 4;
isolated implementation/test workers use existing capacity. User box `tt2` is untouched.

## 2026-09-08 bootstrap

- Read the full goal attachment and current source/state.
- Shared first-slice contract committed/pushed as `69b5072`.
- Added core work/plan types and validation tests (not yet executed in-box).
- `75c7698` adds core types and approval validation. `9c99351` adds PostgreSQL
  work/conversation storage, optimistic revisions, idempotent replies/approval,
  durable planning attempts and reclaimable fenced leases. Source is pushed;
  functionality is not yet integrated into the controller UI.
- UI worker: `factory-ui-0908`, box ID
  `1e2139db-766f-4a7e-8440-ced500000000`, task ID
  `0458ec2e-c979-4bb4-8b6b-edad00000000`, branch `factory/ui-plan-0908`.
  Finished **exit 0**, source `d6742b6`, integrated as `f1c2048`. Worker reports
  15/15 real Chromium fixture tests inside its box (not live backend proof).
- Asset worker: `factory-assets-0908`, box ID
  `1bf0d5e0-129f-4828-86f6-672200000000`, task ID
  `a23c41ab-7ed4-4312-815e-ee0200000000`, branch `factory/assets-0908`.
  Finished **exit 0**, source `67048b8`, integrated as `ae59d73`. Worker reports
  build/vet/race/concurrency tests passed inside its box. PNG/JPEG supported;
  WebP correctly rejected until a decoder dependency is integrated and tested.
- Integration box: `factory-integration-0908`, ID
  `cf712a55-c4b7-4626-86d9-28dd00000000`. Test task
  `faa411d0-a078-4901-8b3b-009b00000000` finished **exit 0**, pinned to
  `9c99351`. Installs Go 1.26.0 (official checksum verified) and isolated local
  PostgreSQL inside the box, then runs core tests/vet with real DB tests enabled.
  Real PostgreSQL tests passed, including lease recovery, deduplicated replies,
  persisted plan revisions and rejection of exit-0-without-plan; Go vet passed.
- Gateway/API/tab integration at `eb3142c` is now queued for full Go tests/vet in
  the integration box: task `8eed46c7-ab42-4719-82cd-5c0e00000000`. The box was
  hibernated before submission; if PostgreSQL packages/daemon did not survive,
  inspect the actual failure and prepare the new worker before another test.
- That combined task exited 1 before tests: replacement worker lacked
  `pg_ctlcluster`. `104d38c` adds repeatable in-box setup with an isolated private
  PostgreSQL cluster (no TCP); task `9afb096e-dd7c-4c07-80e7-c63700000000`
  then executed actual tests. Factory core/assets passed, but full suite failed:
  new all-method gateway route conflicted with `GET /`, and existing
  `TestIdleHibernateRealTmux` reported a remaining server. Vet was not reached.
- `c846166` fixes gateway routing with explicit HTTP methods and adds the service
  executable plus idempotent planner dispatcher. It is NOT deployed; repository
  and execution adapters are still unconfigured (no fake fallback). Verification
  task `e4fd512a-d13a-4544-8707-ef7400000000` is queued in the integration box,
  including five bounded repeats of the observed tmux test for diagnosis.
- GitHub App client worker reuses the UI box: task
  `518995c7-fde8-4e7f-8fc4-7c9500000000`, last observed **running**, branch
  `factory/github-app-0908`, owns `internal/factory/githubapp/**` only. Scope:
  scoped installation tokens, authorized repository listing/resolution and HTTP
  security tests. Live App credentials remain requested, not assumed available.
- Gateway strips browser/identity headers and injects account/user plus a dedicated
  service credential. New feature is disabled without explicit backend config.
  Factory HTTP handlers, private-assets adapter, service executable and dispatcher
  exist; GitHub wiring and actual box/multimodal execution remain next.
  No backend/server has been deployed. UI fixture checks are not end-to-end proof.
- Both use the saved Codex profile that passed provisioning and existing GitHub
  credentials. Other Claude/Codex profile selections were rejected at create
  (HTTP 400); those rejected requests did not create their requested workspaces.
  A current Claude profile and authorized test GitHub App configuration were
  requested securely for later live proof. These are not reasons to halt coding.

## Next actions

Latest continuation (2026-09-08):

- Publication coordinator worker `90d4a24d-f982-433f-8c88-eac600000000`
  finished with actual exit 0, source `a62ed8a`, integrated as `238ddac`.
  It recovered without a controller restart; the earlier restart question is
  obsolete. `96e0d55` wires publication into service startup behind explicit
  `VMBOX_FACTORY_ISSUES_WRITE=true` policy and gates UI/API approval accordingly.
- Combined verification task `3da3ba36-c034-46a4-8ce1-76e100000000` at
  `96e0d55` exited 1. All Factory packages (including PostgreSQL publication
  tests) passed, but `TestIdleHibernateRealTmux` failed while flushing the
  workspace filesystem (`signal: killed`, shared test context is 10 seconds).
  The exact cause of the slow flush remains unresolved. Full vet and browser
  checks were not reached; this is not a passing combined run.
- Verifier wrapper worker `baed1333-c0f3-46ba-8211-876f00000000` finished
  with actual exit 0; source `2f71e81` integrated as `b34862b`. Its compiled
  parent/child tests exercise real check exits 0/7, cancellation and SIGKILL,
  private logs, callback refusal/retries and durable redelivery without rerun.
  Wrapper delivery success, verifier child exit and individual check acceptance
  remain separate. Worker tests/build/vet passed; race could not run with CGO
  disabled. This does not prove remote candidate/log transport or scheduling.
- Follow-up task `cd2b559e-e77e-4e1d-899b-06bc00000000` is queued in the
  integration box, pinned to `b34862b`: verifier-related tests, full vet and
  the previously unreached real Chromium API-fixture suite. No result yet.
- Remote builder adapter worker `f02f715b-b973-40c3-87e9-77a900000000`
  is running in `factory-ui-0908`, branch `factory/remote-build-0908`, scoped
  to `internal/factory/remotebuild`. It must recover accepted tasks before
  staging again and refuse dependency builds without prepared-source proof.
  Durable execution scheduling, authenticated artifact transfer, PR/integration
  orchestration and full live UI-to-product acceptance remain unfinished.

- Builder staging at `af291df` passed full Go/PostgreSQL tests and vet:
  `cb52e47c-d10d-4812-8db3-e31f00000000`, actual exit 0.
- Reviewer sources `eb6aaa9`/`4c19bb3` integrated as `a3a3fd0`/`7b37407`.
  `50e05cf` adds candidate/timing/process-bound review mapping; negative reviews
  retain exit 0 without passing acceptance. Full Go/PostgreSQL/vet task
  `afae7d29-5249-4208-86a6-f20200000000` passed at that commit, actual exit 0.
- `ffc98e1` durably retains rejected review summaries/findings and displays them
  in native UI details, distinct from process failure. Its added DB/browser
  regressions are included in pending combined task below.
- Transfer task `6387138a-4b47-4342-844a-688000000000` became **unknown**, no
  observed exit. Fresh Railway endpoint plus direct SSH procfs/session inspection
  found no task/agent process or tmux session; checkout was clean at `ec33989`,
  which was also pushed. Do not report its task exit as 0. Its recorded in-box
  tests support the code, not recovery of the missing controller exit receipt.
- Build-job sources `43494aa`/`ec33989` integrated as `6d9069d`/`b4eb77b`.
  Candidate bundles now declare the exact baseline prerequisite; import verifies
  bytes/digest/header/baseline/candidate. Review found builder inspection also
  needed shallow metadata. `e19ad0e` retains that metadata in builder/verifier/
  reviewer inspections and extends the real Git transfer regression through
  actual builder.Run (fixture CLI) and independent command verification.
- Combined full Go/PostgreSQL/vet and Chromium task
  `d3e2f72d-6ee3-4249-88ee-54f900000000` passed at `b722ba2`, actual exit 0;
  all 22 Chromium fixture tests passed. This includes shallow build transfer and
  independent command verification plus persisted negative-review history.
- Next distinct worker task `baed1333-c0f3-46ba-8211-876f00000000` is queued in
  the confirmed-idle assets box, branch `factory/verify-job-0908`, scoped to
  verification child-process/receipt delivery. Initial local permission review
  timed out before executing any submission; its permitted single retry created
  this task. No uncertain controller submission was replayed.

- Task `c54642e4-fe3b-40c9-8502-a57700000000` finished **exit 0**: PR-client
  and builder tests/vet passed, followed by **22/22 real Chromium fixture tests**
  inside the integration box. These exercise UI behavior with intercepted APIs,
  not live GitHub App/planner acceptance.
- `af291df` connects controller-managed builder provisioning and fixed-wrapper
  launch/recovery. Builder boxes are keyed by durable stage attempt, distinct
  from planners and siblings; only the selected agent login is requested.
  Private staging now supports explicit planner/builder roles with separate
  pinned binaries and role-bound manifests. Full Go/PostgreSQL/vet task
  `cb52e47c-d10d-4812-8db3-e31f00000000` is queued in the integration box.
- Build-job worker `0d82c92d-8547-404e-8f13-acef00000000` exited 0, source
  `43494aa`, not integrated yet. Review identified a mismatch between its
  full-history export and actual depth-1 source staging. Follow-up
  `6387138a-4b47-4342-844a-688000000000` is running to test/correct real shallow
  baseline → candidate bundle → independent import behavior.
- Reviewer follow-up `b1024980-f25f-48a6-881a-b95300000000` is still running.
  No reruns or duplicate submissions were triggered by observation timeouts.
- Asked whether a controller-only restart is permitted to test the stalled
  publication worker's recovery. Approval is pending; no restart or deployment
  has occurred. Other implementation and verification work continues.

- Builder commits `9713c8` and `c9601d6` reviewed and integrated as `d817154`
  and `b279887`. The corrected adapter cleans owned process groups before reaping,
  filters publishing/control environment credentials, and isolates trusted Git
  inspection from repository filters and index trust flags. In-box focused tests
  and vet passed in task `f611fc27-b4d4-4765-84d6-f6eb00000000` before browser launch.
- UI at `1a38dc5` passed full Go/PostgreSQL tests and vet in task
  `9e2c475c-3ccd-4e45-8010-858d00000000`, but that task exited 1 before browser
  launch because the orchestration command used an incorrect Puppeteer path.
  The next task `f611fc27-b4d4-4765-84d6-f6eb00000000` resolved the package but
  Chromium lacked `libglib-2.0.so.0`: again no browser test passed.
  `4a941ad` adds repeatable in-box Chromium/dependency setup. Task
  `c54642e4-fe3b-40c9-8502-a57700000000` is running: PR/builder tests and vet
  completed before it entered package installation; browser outcome still pending.
- `ae0e77e` adds candidate-bound, narrowly scoped GitHub App PR publication with
  marker reconciliation and post-publication head validation. It never pushes or
  merges; durable coordinator wiring and live App acceptance are not complete.
- Build-job wrapper worker `0d82c92d-8547-404e-8f13-acef00000000` is running in
  the assets box, branch `factory/build-job-0908`, scoped to `buildjob` and
  `cmd/vmbox-builder`: durable receipt/callback before hibernation and a bounded
  candidate bundle for independent transfer. This is not integrated yet.
- Reviewer worker `33c971ab-45af-420e-8af6-7c2a00000000` exited 0 and pushed
  `eb6aaa9`. Its real Codex review rejected an arithmetic defect despite a passing
  file-existence check; Claude remained auth-unavailable. Not integrated yet:
  review found whole-repository text limits, verifier/reviewer path coupling and
  raw index-audit gaps. Follow-up `b1024980-f25f-48a6-881a-b95300000000` is queued
  on the same branch/box to correct these without changing auth or models.

- `0e0c9bd` terminal failure handling passed full Go/PostgreSQL tests and vet in
  task `05894bfe-dad0-476a-8bad-47ff00000000`, actual exit 0. No local tests ran.
- `1a38dc5` projects durable feature-attempt history into the Factory UI:
  actual exit/signal/unknown outcomes remain separate from semantic acceptance.
  Combined Go/PostgreSQL plus Chromium fixture task
  `9e2c475c-3ccd-4e45-8010-858d00000000` is queued in the integration box.
  Browser fixtures are not a substitute for live UI → App → worker acceptance.
- Created disposable `factory-review-0908`, box
  `48734647-d155-4284-8b13-671700000000`, using the existing fourth slot.
  Independent-review adapter task `33c971ab-45af-420e-8af6-7c2a00000000` is
  running on branch `factory/independent-review-0908`, scoped only to
  `internal/factory/reviewer/**`. It must inspect real source/diff and preserve
  actual process outcomes, with a real Codex defective-source review test.
  Builder review follow-up `eecb6fea-9fe1-4066-85bd-edbb00000000` is also running.
  Neither worker's pending changes are integrated. Desired fleet capacity is 4.

- `3003f4e` passed full Go tests, real PostgreSQL tests and vet inside the
  integration box: task `4bc3d356-64e7-4064-8bb1-533800000000`, actual exit 0.
  This includes durable feature intent, version/account fences, restart identity
  recovery, and shared planning/feature admission. It is not a running scheduler.
- Builder task `78464022-5b21-4e50-815f-85e100000000` exited 0 and pushed
  `9713c8`. Its evidence records a real Codex-authored commit and independent
  numeric check. Review found post-reap process-group cleanup and inherited
  credential risks; it is **not integrated yet**. Bounded follow-up task
  `eecb6fea-9fe1-4066-85bd-edbb00000000` is queued in the same worker.
- Publication task `90d4a24d-f982-433f-8c88-eac600000000` was rechecked and is
  still queued, not failed. No duplicate submission or lifecycle reset performed.
- Added terminal stage failure receipts and trusted PR-result persistence.
  Semantic rejection retains actual exit 0; missing exits cannot be treated as
  completion. Finished attempts reject late rebinding. New regression tests
  await in-box execution. Independent review, remote scheduling, PR publication,
  integration and live App/Claude acceptance remain unfinished.

- Verification worker `166cc5e` integrated as `047b39f`. Review corrected the
  check HOME from `/nonexistent` to a fresh private writable home/cache outside
  source, and aligned signals to numeric POSIX values. Task
  `227219fc-4d3e-43c8-8d9c-6bb800000000` passed verification/execution tests and
  vet at `916fac3`, including an actual Go compilation through the runner.
- `ebccb13` maps scoped verification reports into feature acceptance by checking
  actual command identity, source revision, timestamps, outcomes and log digests,
  not `AllPassed` alone. Focused task `2d6b1610-76e7-4fda-89c4-f23c00000000`
  is queued in the integration box. Persistent feature scheduling, independent
  review, PR publication and final integration are still required.
- Builder adapter worker `78464022-5b21-4e50-815f-85e100000000` is running in
  `factory-assets-0908`, branch `factory/feature-builder-0908`, scoped to actual
  agent execution and trusted Git candidate inspection (not verified completion).
- Publication task remains queued behind hibernation despite the guarded empty
  server cleanup. Do not duplicate it or infer that it failed. No production
  controller restart/deployment or direct controller DB repair was attempted.
- `4d841f0` adds the feature dependency/acceptance transition model. Builder
  exit 0 cannot unlock dependents: clean candidate, independent exact checks,
  review and matching repository PR are separate gates. Test/vet task
  `0b162987-5c5d-4d09-85fc-33bc00000000` passed inside the integration box.
  The subsequent approval-input-revision guard awaits the next combined run.
  This model is not yet a persisted/running feature execution scheduler.
- Verification runner task `a66884e8-47f2-42ea-832a-55aa00000000` is running in
  the assets worker, branch `factory/verification-runner-0908`, scoped to real
  check execution, process outcomes and evidence files.
- Publication task `90d4a24d-f982-433f-8c88-eac600000000` remains queued behind
  its box's `hibernating / saving-workspace` transition. A fresh Railway endpoint
  resolution plus read-only inspection found zero tmux sessions and an empty
  server in this disposable box. An atomic empty-session guard stopped only
  that server (SSH exit 0); volume and task were preserved. Do not resubmit the
  queued task. No production controller or runtime fix was deployed.
- Focused staging ownership/image-limit tests and vet at `17666ad` passed in
  `7294f3d0-300c-40fc-826e-6ae500000000`. The workload-user wrapper was asserted
  by a local-shell transport fixture, not a live SSH staging acceptance test.
- The planning service is now wired behind explicit configuration at `e046018`:
  controller allocation/recovery, private SSH staging, real planner command,
  scoped durable callback, result observation, and DB-backed worker admission.
  Long staging renews the exact lease and cancels on lost ownership. Full Go
  tests/vet, real PostgreSQL tests, actual built-planner transfer fixture, and
  both planner/factory builds passed in task
  `f5b9ce8f-e721-4d2d-8302-35db00000000` at that revision.
- Staging worker `d2e30cc` integrated as `3daa276`; UI worker `e01f772` integrated
  as `7e737c3` with 21 passed Chromium desktop/mobile fixture tests. No live App
  authentication or full UI-to-agent execution was exercised by those fixtures.
- Review found direct SSH staging lacked the standard workload-user wrapper.
  `17666ad` corrects ownership and enforces eight images; focused verification
  task `7294f3d0-300c-40fc-826e-6ae500000000` is queued in the integration box.
- GitHub App ID/key-file/installation configuration names are absent from the
  local secure environment files and process environment. Secure configuration
  requested for live acceptance. No fallback credential or fake App was used.
- Approved-plan publication coordinator worker
  `90d4a24d-f982-433f-8c88-eac600000000` is queued in `factory-ui-0908`, branch
  `factory/publication-coordinator-0908`, scoped to `internal/factory/publishing`.
  Feature execution/review/integration and live UI planning remain incomplete.
- `4b3d4d0` integrates the controller planning runner: pinned source authority,
  private staged job, assignment recheck, recovery of accepted tasks before
  restaging, and actual agent outcomes from the scoped inbox (not wrapper exits).
  Combined task `63fa9fb9-5f9a-4b8f-8fe9-3bfe00000000` passed full Go tests/vet
  including real PostgreSQL result-inbox tests. No full UI-to-agent proof yet.
- Result inbox worker `b75cac6` integrated as `aedcc5f`; its envelope was aligned
  with core `attemptId` and numeric POSIX signals in `4b3d4d0`. `planjob` now uses
  the shared envelope type. GitHub publication worker `c3369fd` integrated as
  `9d19438`; issue/comment writes still require workflow/outbox wiring and live
  GitHub App acceptance. Worker tests made no real issue writes.
- `756f09d` adds scoped `/result` callback configuration and truthful execution/
  image capability reporting, plus readiness and pre-launch rejection tests.
  These newest tests await the next combined in-box run.
- Private staging task `0a54b4e4-d2da-40aa-8243-203300000000` is running in
  `factory-assets-0908`, branch `factory/private-staging-0908`, scoped to the new
  staging package. UI readiness task `ff6d0f98-9caa-4f4c-8289-06d800000000` is
  running in `factory-ui-0908`, branch `factory/ui-readiness-0908`.
- The main proposal now reflects the full approved target: required master and
  feature issues, per-feature PRs, controller-visible responses and evidence,
  and in-box product verification. Only external deployment/event intake is
  optional; the feature PR workflow is not deferred or waived.
- Regression task `af61889b-0333-443c-8eda-fd2d00000000` completed with exit 0:
  full Go tests, Go vet, and the script's real PostgreSQL checks passed at
  `098131f`. This includes the formerly failing real tmux idle-hibernate test.
- Runtime worker commit is now integrated as `e017437`. `31dac0d` adds
  `vmbox-planner`: bounded private stdin job, actual agent exit/signal receipt,
  durable start marker, create-only evidence, and authenticated result delivery.
  Lost delivery responses retry the stored receipt, not the agent execution.
  New command tests/vet/build task `c9939798-f6f2-4521-813b-0d0200000000` passed
  with exit 0 in the integration box. These fixtures are not live planning proof.
  Combined regression at `49356c4` passed with exit 0 as
  `ddfe708c-4025-4220-822f-b3f100000000` in the same box.
- `fc3a4be` preserves provisioning identity/phase in the dispatcher and adds
  controller-resolved connections plus fixed-command planner submission. The
  private staging transport and concrete Start/Observe adapter remain unwired.
- Parallel publication worker `6e369119-bb53-410c-8afa-dd5400000000` is running
  on `factory/github-publication-0908` (GitHub App issue/comment primitives).
  Result inbox worker `d643ac8c-be0d-4f85-8035-528e00000000` is running on
  `factory/result-inbox-0908` (scoped capabilities and durable PostgreSQL inbox).
  Neither task's pending work is yet integrated or claimed verified.
- User reconfirmed a maximum of **6 fleet slots**, not 8. Desired capacity
  remains 4; no additional slots have been requested.
- GitHub App worker completed with exit 0; its client is integrated at
  `5bcc63f`. Encrypted App configuration is integrated at `6b90d12`.
  Live authorized-App verification remains outstanding.
- Planner runtime worker `9ff13c42-5b3c-4069-8574-e48400000000` exited 0
  and pushed `3a6bf9c` on `factory/planner-runtime-0908` (integrated as `e017437`).
  Its evidence records actual Codex image grounding (exit 0, valid structured
  result); Claude live authentication failed. This is not UI-to-box proof.
- Diagnostic `d441852c-d4ca-4a98-8db9-4beb00000000` exited 0 in the integration
  box: the tmux session-loop format distinguished live and empty servers and
  stopped only the isolated empty server. `098131f` uses that compatible format.
- Full Go tests/vet and real PostgreSQL verification of `098131f` passed in
  `af61889b-0333-443c-8eda-fd2d00000000`; later commits need combined verification.

Poll the exact task IDs above; observation failures must not cause resubmission.
Three isolated boxes now exist; total fleet remains 4, below the approved 6.
Integrate only their scoped commits into the feature branch after inspecting
actual diffs/test evidence. Continue coordinator persistence, account gateway,
repository/App broker, image staging and real planning response ingestion.
Run all builds/tests inside vmbox boxes, including core validation tests.

The UI must show idea -> agent responses/questions -> editable plan -> approval
-> master/feature issues -> per-feature PRs -> verified product. GitHub is the
linked record, not a replacement for the UI conversation. The original broader
plan still needs reconciliation with this full approved workflow.

Not complete: no running factory service yet, no live App or
multimodal planning proof, no feature build/review/integration workflow. Existing
unrelated startup/reconciliation drafts remain excluded from all factory commits.
