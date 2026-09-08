# Factory implementation progress

Feature branch: `proposal/software-factory`. No main merge or production deploy.
Maximum authorized fleet capacity: **6**. Current desired capacity remains 4;
two isolated workers use existing capacity. User box `tt2` is untouched.

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
